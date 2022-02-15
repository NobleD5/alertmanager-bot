package alertmanager

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/prometheus/alertmanager/types"
)

type alertResponse struct {
	Status string         `json:"status"`
	Data   []*types.Alert `json:"data,omitempty"`
}

// ListAlerts returns a slice of Alert and an error.
func ListAlerts(logger log.Logger, alertmanagerURL string) ([]*types.Alert, error) {

	apiEndpoint := string("/api/v1/alerts")
	getURL := alertmanagerURL + apiEndpoint
	level.Debug(logger).Log("msg", "assembled URL for GETing alerts request", "url", getURL)

	response, err := httpRetry(logger, http.MethodGet, getURL)
	if err != nil {
		return nil, err
	}

	var alertResponse alertResponse
	dec := json.NewDecoder(response.Body)
	defer response.Body.Close()
	if err := dec.Decode(&alertResponse); err != nil {
		return nil, err
	}
	level.Debug(logger).Log("msg", "decoded alerts", "slice", fmt.Sprint(alertResponse.Data))

	return alertResponse.Data, err
}
