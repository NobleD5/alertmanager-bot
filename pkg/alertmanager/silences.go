package alertmanager

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/NobleD5/alertmanager-bot/pkg/vendor"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/hako/durafmt"
)

// ListSilences returns a slice of Silence and an error.
func ListSilences(logger log.Logger, alertmanagerURL string) ([]vendor.Silence, error) {

	apiEndpoint := string("/api/v1/silences")
	getURL := alertmanagerURL + apiEndpoint
	level.Debug(logger).Log("msg", "assembled URL for GETing silences request", "url", getURL)

	response, err := httpRetry(logger, http.MethodGet, getURL)
	if err != nil {
		return nil, level.Error(logger).Log("msg", "error while GET silences from alertmanager", "err", err)
	}

	var silencesResponse vendor.SilencesResponse
	dec := json.NewDecoder(response.Body)
	defer response.Body.Close()
	if err := dec.Decode(&silencesResponse); err != nil {
		return nil, err
	}

	silences := silencesResponse.Data
	sort.Slice(silences, func(i, j int) bool {
		return silences[i].EndsAt.After(silences[j].EndsAt)
	})

	return silences, err
}

// SilenceMessage converts a silences to a message string.
func SilenceMessage(s vendor.Silence) string {

	var alertname, emoji, matchers, duration string = "Empty alertname", "", "", ""

	for _, m := range s.Matchers {
		if m.Name == "alertname" {
			alertname = m.Value
		} else {
			matchers = matchers + fmt.Sprintf(`%s="%s", `, m.Name, m.Value)
		}
	}

	resolved := Resolved(s)
	if !resolved {
		emoji = " 🔕"
		duration = fmt.Sprintf(
			"*Started*: %s ago\n*Ends:* %s\n",
			durafmt.Parse(time.Since(s.StartsAt)),
			durafmt.Parse(time.Since(s.EndsAt)),
		)
	} else {
		duration = fmt.Sprintf(
			"*Ended*: %s ago\n*Duration*: %s",
			durafmt.Parse(time.Since(s.EndsAt)),
			durafmt.Parse(s.EndsAt.Sub(s.StartsAt)),
		)
	}

	return fmt.Sprintf(
		"*%s*%s\n```%s```\n%s\n",
		alertname, emoji,
		strings.TrimSpace(matchers),
		duration,
	)
}

// Resolved returns if a silence is resolved by EndsAt.
func Resolved(s vendor.Silence) bool {
	if s.EndsAt.IsZero() {
		return false
	}
	return !s.EndsAt.After(time.Now())
}

// PostSilence used for POSTing valid silence JSON on alertmanager API endpoint.
func PostSilence(logger log.Logger, alertmanagerURL string, silence vendor.Silence) error {

	apiEndpoint := string("/api/v2/silences")
	postURL := alertmanagerURL + apiEndpoint
	level.Debug(logger).Log("msg", "assembled URL for POSTing silence request", "url", postURL)

	payLoad, err := json.Marshal(silence)
	if err != nil {
		return level.Error(logger).Log("msg", "marshalling silence to JSON", "err", err)
	}

	level.Debug(logger).Log("msg", "testing created silence", "silence", string(payLoad))

	response, err := request(logger, http.MethodPost, http.StatusOK, postURL, payLoad)
	if err != nil {
		return level.Error(logger).Log("msg", "error while POST silence to alertmanager", "err", err)
	}
	defer response.Body.Close()

	return nil
}

// DeleteSuperSilence used for DELETing supersilence from */sm*-command on alertmanager API endpoint.
func DeleteSuperSilence(logger log.Logger, alertmanagerURL string, silenceID string) error {

	apiEndpoint := string("/api/v2/silence/")
	postURL := alertmanagerURL + apiEndpoint + silenceID
	level.Debug(logger).Log("msg", "assembled URL for DELETing supersilence request", "url", postURL)

	response, err := request(logger, http.MethodDelete, http.StatusOK, postURL, []byte{})
	if err != nil {
		return level.Error(logger).Log("msg", "error while DELETE supersilence from alertmanager", "err", err)
	}
	defer response.Body.Close()

	return nil
}
