package alertmanager

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/NobleD5/alertmanager-bot/pkg/vendor"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/stretchr/testify/assert"
)

////////////////////////////////////////////////////////////////////////////////
// TESTING
////////////////////////////////////////////////////////////////////////////////

func TestListSilencesAndPosting(t *testing.T) {

	logger := log.NewLogfmtLogger(os.Stdout)
	logger = level.NewFilter(logger, level.AllowDebug())

	silencesJSON, _ := ioutil.ReadFile("../test/silences.json")

	silence := &vendor.Silence{
		ID: "acf620d5-0239-4f7b-ab83-249b4da88d43",
		Matchers: vendor.Matchers{
			0: &vendor.Matcher{Name: "alertname", Value: "alertname_1", Type: vendor.MatchEqual},
			1: &vendor.Matcher{Name: "environment", Value: "monitoring", Type: vendor.MatchEqual},
		},
		StartsAt:  time.Now(),
		EndsAt:    time.Now().Add(time.Hour * 2),
		UpdatedAt: time.Now(),
		CreatedBy: "alertmanager-bot",
		Comment:   "Enacted by administrator command",
		Status:    vendor.SilenceStatus{State: "active"},
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/ok/api/v1/silences", func(res http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodGet:
			res.Header().Set("Content-Type", "application/json")
			res.WriteHeader(http.StatusOK)
			res.Write([]byte(silencesJSON))
		case http.MethodPost:
			res.Header().Set("Content-Type", "application/json")
			res.WriteHeader(http.StatusOK)
		default:
		}
	})
	mux.HandleFunc("/wrong/api/v1/silences", func(res http.ResponseWriter, req *http.Request) {
		res.WriteHeader(http.StatusNotFound)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// ---------------------------------------------------------------------------
	//  CASE: return valid silences list
	// ---------------------------------------------------------------------------
	routeOK, _ := url.Parse(ts.URL + "/ok")
	_, err := ListSilences(logger, routeOK.String())
	if err != nil {
		t.Errorf("ListSilences() : Test 1 FAILED, got error: %s", err)
	} else {
		t.Log("ListSilences() : Test 1 PASSED.")
	}

	// ---------------------------------------------------------------------------
	//  CASE: wrong or unreachable URL
	// ---------------------------------------------------------------------------
	routeWrong, _ := url.Parse(ts.URL + "/wrong")
	_, err = ListSilences(logger, routeWrong.String())
	if err != nil {
		t.Errorf("ListSilences() : Test 2 FAILED, got error: %s", err)
	} else {
		t.Log("ListSilences() : Test 2 PASSED.")
	}

	// ---------------------------------------------------------------------------
	//  CASE: PostSilence
	// ---------------------------------------------------------------------------
	routePost, _ := url.Parse(ts.URL + "/post")
	err = PostSilence(logger, routePost.String(), *silence)
	if err != nil {
		t.Errorf("PostSilence() : Test 1 FAILED, got error: %s", err)
	} else {
		t.Log("PostSilence() : Test 1 PASSED.")
	}

	// ---------------------------------------------------------------------------
	//  CASE: active silence
	// ---------------------------------------------------------------------------
	s := SilenceMessage(*silence)
	if s == "" {
		t.Error("SilenceMessage() : Test 1 FAILED, nil string")
	} else {
		t.Logf("SilenceMessage() : Test 1 PASSED, s=%s", s)
	}

	silence.StartsAt = time.Now()
	silence.EndsAt = time.Now().Add(-1 * time.Minute)

	// ---------------------------------------------------------------------------
	//  CASE: expired silence
	// ---------------------------------------------------------------------------
	s = SilenceMessage(*silence)
	if s == "" {
		t.Error("SilenceMessage() : Test 2 FAILED, nil string")
	} else {
		t.Logf("SilenceMessage() : Test 2 PASSED, s=%s", s)
	}
}

func TestResolved(t *testing.T) {
	s := vendor.Silence{}
	assert.False(t, Resolved(s))

	s.EndsAt = time.Now().Add(time.Minute)
	assert.False(t, Resolved(s))

	s.EndsAt = time.Now().Add(-1 * time.Minute)
	assert.True(t, Resolved(s))
}
