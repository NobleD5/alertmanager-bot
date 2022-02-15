package alertmanager

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
)

////////////////////////////////////////////////////////////////////////////////
// TESTING
////////////////////////////////////////////////////////////////////////////////

func TestStatus(t *testing.T) {

	logger := log.NewLogfmtLogger(os.Stdout)
	logger = level.NewFilter(logger, level.AllowDebug())

	statusJSON, _ := ioutil.ReadFile("../test/status.json")

	mux := http.NewServeMux()

	// Status Mock
	mux.HandleFunc("/ok/api/v1/status", func(res http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodGet:
			res.Header().Set("Content-Type", "application/json")
			res.WriteHeader(http.StatusOK)
			res.Write([]byte(statusJSON))
		default:
			res.WriteHeader(http.StatusGone)
		}
	})
	mux.HandleFunc("/wrong/api/v1/status", func(res http.ResponseWriter, req *http.Request) {
		res.WriteHeader(http.StatusNotFound)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// ---------------------------------------------------------------------------
	//  CASE: return valid status
	// ---------------------------------------------------------------------------
	routeOK, _ := url.Parse(ts.URL + "/ok")
	_, err := Status(logger, routeOK.String())
	if err != nil {
		t.Errorf("Status() : Test 1 FAILED, got error: %s", err)
	} else {
		t.Log("Status() : Test 1 PASSED.")
	}

	// ---------------------------------------------------------------------------
	//  CASE: wrong or unreachable URL
	// ---------------------------------------------------------------------------
	routeWrong, _ := url.Parse(ts.URL + "/wrong")
	_, err = Status(logger, routeWrong.String())
	if err == nil {
		t.Error("Status() : Test 2 FAILED")
	} else {
		t.Log("Status() : Test 2 PASSED.")
	}
}
