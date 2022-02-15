package alertmanager

import (
	// "context"
	"bytes"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
)

func httpBackoff() *backoff.ExponentialBackOff {

	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 100 * time.Millisecond
	b.MaxInterval = 2 * time.Second
	b.MaxElapsedTime = 5 * time.Second // Telegram shows "typing" max 5 seconds

	return b
}

func httpRetry(logger log.Logger, method string, url string) (*http.Response, error) {

	var resp *http.Response
	var err error

	fn := func() error {
		req, err := http.NewRequest(method, url, nil)
		if err != nil {
			return err
		}

		// ctx, cancel := context.WithTimeout(context.Background(), (5 * time.Second))
		// defer cancel()
		// req = req.WithContext(ctx)

		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			return err
		}

		switch method {
		case http.MethodGet:
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("status code is %d not 200", resp.StatusCode)
			}
		case http.MethodPost:
			if resp.StatusCode == http.StatusBadRequest {
				return fmt.Errorf("status code is %d not 3xx", resp.StatusCode)
			}
		}

		return nil
	}

	notify := func(err error, dur time.Duration) {
		level.Info(logger).Log(
			"msg", "retrying",
			"duration", dur,
			"err", err,
			"url", url,
		)
	}

	if err := backoff.RetryNotify(fn, httpBackoff(), notify); err != nil {
		return nil, err
	}

	return resp, err
}

func request(logger log.Logger, method string, code int, url string, payLoad []byte) (*http.Response, error) {

	response := new(http.Response)

	level.Debug(logger).Log("msg", "will be used this request", "method", method, "msg", "awaiting this response status", "code", code)

	// Assembly request for API
	request, err := http.NewRequest(method, url, bytes.NewBuffer([]byte(payLoad)))
	// Adding necessary 'key-value' pairs to header
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Charset", "UTF-8")
	if err != nil {
		return response, level.Error(logger).Log("msg", "error while assembling http.NewRequest", "err", err)
	}

	// Client creating
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: transport}

	// Starting request, receiving response
	response, err = client.Do(request)
	if err != nil {
		return response, level.Error(logger).Log("msg", "error while doing request", "err", err)
	}
	if response.StatusCode != code {
		return response, level.Error(logger).Log("msg", "awaiting response status code", "code", code, "msg", "got this status code", "code", response.StatusCode)
	}
	level.Debug(logger).Log("msg", "request succesfull", "method", method, "msg", "got this status code", "code", response.StatusCode)

	return response, nil
}
