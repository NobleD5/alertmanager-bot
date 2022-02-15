// Copyright 2017 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package vendor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/pkg/errors"
	"github.com/prometheus/common/model"
)

// MatchType is an enum for label matching types.
type MatchType int

// Possible MatchTypes.
const (
	MatchEqual MatchType = iota
	MatchNotEqual
	MatchRegexp
	MatchNotRegexp
)

var (
	re = regexp.MustCompile(
		// '=~' has to come before '=' because otherwise only the '='
		// will be consumed, and the '~' will be part of the 3rd token.
		`^\s*([a-zA-Z_:][a-zA-Z0-9_:]*)\s*(=~|=|!=|!~)\s*((?s).*?)\s*$`,
	)
	typeMap = map[string]MatchType{
		"=":  MatchEqual,
		"!=": MatchNotEqual,
		"=~": MatchRegexp,
		"!~": MatchNotRegexp,
	}
)

func (m MatchType) String() string {
	typeToStr := map[MatchType]string{
		MatchEqual:     "=",
		MatchNotEqual:  "!=",
		MatchRegexp:    "=~",
		MatchNotRegexp: "!~",
	}
	if str, ok := typeToStr[m]; ok {
		return str
	}
	panic("unknown match type")
}

// Matcher models the matching of a label.
type Matcher struct {
	Type  MatchType
	Name  string
	Value string

	re *regexp.Regexp
}

// NewMatcher returns a matcher object.
func NewMatcher(t MatchType, n, v string) (*Matcher, error) {
	m := &Matcher{
		Type:  t,
		Name:  n,
		Value: v,
	}
	if t == MatchRegexp || t == MatchNotRegexp {
		re, err := regexp.Compile("^(?:" + v + ")$")
		if err != nil {
			return nil, err
		}
		m.re = re
	}
	return m, nil
}

func (m *Matcher) String() string {
	return fmt.Sprintf(`%s%s"%s"`, m.Name, m.Type, openMetricsEscape(m.Value))
}

// Matches returns whether the matcher matches the given string value.
func (m *Matcher) Matches(s string) bool {
	switch m.Type {
	case MatchEqual:
		return s == m.Value
	case MatchNotEqual:
		return s != m.Value
	case MatchRegexp:
		return m.re.MatchString(s)
	case MatchNotRegexp:
		return !m.re.MatchString(s)
	}
	panic("labels.Matcher.Matches: invalid match type")
}

type apiV1Matcher struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	IsRegex bool   `json:"isRegex"`
	IsEqual bool   `json:"isEqual"`
}

// MarshalJSON retains backwards compatibility with types.Matcher for the v1 API.
func (m Matcher) MarshalJSON() ([]byte, error) {
	return json.Marshal(apiV1Matcher{
		Name:    m.Name,
		Value:   m.Value,
		IsRegex: m.Type == MatchRegexp || m.Type == MatchNotRegexp,
		IsEqual: m.Type == MatchRegexp || m.Type == MatchEqual,
	})
}

func (m *Matcher) UnmarshalJSON(data []byte) error {
	v1m := apiV1Matcher{
		IsEqual: true,
	}

	if err := json.Unmarshal(data, &v1m); err != nil {
		return err
	}

	var t MatchType
	switch {
	case v1m.IsEqual && !v1m.IsRegex:
		t = MatchEqual
	case !v1m.IsEqual && !v1m.IsRegex:
		t = MatchNotEqual
	case v1m.IsEqual && v1m.IsRegex:
		t = MatchRegexp
	case !v1m.IsEqual && v1m.IsRegex:
		t = MatchNotRegexp
	}

	matcher, err := NewMatcher(t, v1m.Name, v1m.Value)
	if err != nil {
		return err
	}
	*m = *matcher
	return nil
}

// openMetricsEscape is similar to the usual string escaping, but more
// restricted. It merely replaces a new-line character with '\n', a double-quote
// character with '\"', and a backslash with '\\', which is the escaping used by
// OpenMetrics.
func openMetricsEscape(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		"\n", `\n`,
		`"`, `\"`,
	)
	return r.Replace(s)
}

// Matchers is a slice of Matchers that is sortable, implements Stringer, and
// provides a Matches method to match a LabelSet against all Matchers in the
// slice. Note that some users of Matchers might require it to be sorted.
type Matchers []*Matcher

func (ms Matchers) Len() int      { return len(ms) }
func (ms Matchers) Swap(i, j int) { ms[i], ms[j] = ms[j], ms[i] }

func (ms Matchers) Less(i, j int) bool {
	if ms[i].Name > ms[j].Name {
		return false
	}
	if ms[i].Name < ms[j].Name {
		return true
	}
	if ms[i].Value > ms[j].Value {
		return false
	}
	if ms[i].Value < ms[j].Value {
		return true
	}
	return ms[i].Type < ms[j].Type
}

// Matches checks whether all matchers are fulfilled against the given label set.
func (ms Matchers) Matches(lset model.LabelSet) bool {
	for _, m := range ms {
		if !m.Matches(string(lset[model.LabelName(m.Name)])) {
			return false
		}
	}
	return true
}

// ParseMatcher parses a matcher with a syntax inspired by PromQL and
// OpenMetrics. This syntax is convenient to describe filters and selectors in
// UIs and config files. To support the interactive nature of the use cases, the
// parser is in various aspects fairly tolerant.
//
// The syntax of a matcher consists of three tokens: (1) A valid Prometheus
// label name. (2) One of '=', '!=', '=~', or '!~', with the same meaning as
// known from PromQL selectors. (3) A UTF-8 string, which may be enclosed in
// double quotes. Before or after each token, there may be any amount of
// whitespace, which will be discarded. The 3rd token may be the empty
// string. Within the 3rd token, OpenMetrics escaping rules apply: '\"' for a
// double-quote, '\n' for a line feed, '\\' for a literal backslash. Unescaped
// '"' must not occur inside the 3rd token (only as the 1st or last
// character). However, literal line feed characters are tolerated, as are
// single '\' characters not followed by '\', 'n', or '"'. They act as a literal
// backslash in that case.
func ParseMatcher(s string) (*Matcher, error) {
	ms := re.FindStringSubmatch(s)
	if len(ms) == 0 {
		return nil, errors.Errorf("bad matcher format: %s", s)
	}

	var (
		rawValue = strings.TrimPrefix(ms[3], "\"")
		value    strings.Builder
		escaped  bool
	)

	if !utf8.ValidString(rawValue) {
		return nil, errors.Errorf("matcher value not valid UTF-8: %s", rawValue)
	}

	// Unescape the rawValue:
	for i, r := range rawValue {
		if escaped {
			escaped = false
			switch r {
			case 'n':
				value.WriteByte('\n')
			case '"', '\\':
				value.WriteRune(r)
			default:
				// This was a spurious escape, so treat the '\' as literal.
				value.WriteByte('\\')
				value.WriteRune(r)
			}
			continue
		}
		switch r {
		case '\\':
			if i < len(rawValue)-1 {
				escaped = true
				continue
			}
			// '\' encountered as last byte. Treat it as literal.
			value.WriteByte('\\')
		case '"':
			if i < len(rawValue)-1 { // Otherwise this is a trailing quote.
				return nil, errors.Errorf(
					"matcher value contains unescaped double quote: %s", rawValue,
				)
			}
		default:
			value.WriteRune(r)
		}
	}

	return NewMatcher(typeMap[ms[2]], ms[1], value.String())
}

// ParseMatchers parses a comma-separated list of Matchers. A leading '{' and/or
// a trailing '}' is optional and will be trimmed before further
// parsing. Individual Matchers are separated by commas outside of quoted parts
// of the input string. Those commas may be surrounded by whitespace. Parts of the
// string inside unescaped double quotes ('"…"') are considered quoted (and
// commas don't act as separators there). If double quotes are escaped with a
// single backslash ('\"'), they are ignored for the purpose of identifying
// quoted parts of the input string. If the input string, after trimming the
// optional trailing '}', ends with a comma, followed by optional whitespace,
// this comma and whitespace will be trimmed.
//
// Examples for valid input strings:
//   {foo = "bar", dings != "bums", }
//   foo=bar,dings!=bums
//   foo=bar, dings!=bums
//   {quote="She said: \"Hi, ladies! That's gender-neutral…\""}
//   statuscode=~"5.."
//
// See ParseMatcher for details on how an individual Matcher is parsed.
func ParseMatchers(s string) ([]*Matcher, error) {
	matchers := []*Matcher{}
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")

	var (
		insideQuotes bool
		escaped      bool
		token        strings.Builder
		tokens       []string
	)
	for _, r := range s {
		switch r {
		case ',':
			if !insideQuotes {
				tokens = append(tokens, token.String())
				token.Reset()
				continue
			}
		case '"':
			if !escaped {
				insideQuotes = !insideQuotes
			} else {
				escaped = false
			}
		case '\\':
			escaped = !escaped
		default:
			escaped = false
		}
		token.WriteRune(r)
	}
	if s := strings.TrimSpace(token.String()); s != "" {
		tokens = append(tokens, s)
	}
	for _, token := range tokens {
		m, err := ParseMatcher(token)
		if err != nil {
			return nil, err
		}
		matchers = append(matchers, m)
	}

	return matchers, nil
}

func (ms Matchers) String() string {
	var buf bytes.Buffer

	buf.WriteByte('{')
	for i, m := range ms {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(m.String())
	}
	buf.WriteByte('}')

	return buf.String()
}
