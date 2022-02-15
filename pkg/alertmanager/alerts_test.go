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

func TestListAlerts(t *testing.T) {

	logger := log.NewLogfmtLogger(os.Stdout)
	logger = level.NewFilter(logger, level.AllowDebug())

	alertsJSON, _ := ioutil.ReadFile("../test/alerts.json")

	mux := http.NewServeMux()

	mux.HandleFunc("/ok/api/v1/alerts", func(res http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case http.MethodGet:
			res.Header().Set("Content-Type", "application/json")
			res.WriteHeader(http.StatusOK)
			res.Write([]byte(alertsJSON))
		// case http.MethodPost:
		// 	res.Header().Set("Content-Type", "application/json")
		// 	res.WriteHeader(http.StatusCreated)
		// 	res.Write([]byte("{'data':'dummy'}"))
		default:
			res.WriteHeader(http.StatusGone)
		}
	})
	mux.HandleFunc("/wrong/api/v1/alerts", func(res http.ResponseWriter, req *http.Request) {
		res.WriteHeader(http.StatusNotFound)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// ---------------------------------------------------------------------------
	//  CASE: return valid alerts list
	// ---------------------------------------------------------------------------
	routeOK, _ := url.Parse(ts.URL + "/ok")
	_, err := ListAlerts(logger, routeOK.String())
	if err != nil {
		t.Errorf("ListAlerts() : Test 1 FAILED, got error: %s", err)
	} else {
		t.Log("ListAlerts() : Test 1 PASSED.")
	}

	// ---------------------------------------------------------------------------
	//  CASE:
	// ---------------------------------------------------------------------------
	routeWrong, _ := url.Parse(ts.URL + "/wrong")
	_, err = ListAlerts(logger, routeWrong.String())
	if err == nil {
		t.Errorf("ListAlerts() : Test 2 FAILED, got error: %s", err)
	} else {
		t.Log("ListAlerts() : Test 2 PASSED.")
	}
}
