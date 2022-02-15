package main

import (
	"net"
	"os"
	"testing"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
)

func TestCloseBotOnQuit(t *testing.T) {

	logger := log.NewLogfmtLogger(os.Stdout)
	logger = level.NewFilter(logger, level.AllowDebug())

	quit := make(chan bool)

	l, err := net.Listen("tcp", ":8080")
	if err != nil {
		level.Error(logger).Log("err", err.Error())
		os.Exit(1)
	}
	defer l.Close()

	go func() {
		closeListenerOnQuit(l, quit, logger)
	}()

	// ---------------------------------------------------------------------------
	//  CASE: /help
	// ---------------------------------------------------------------------------
	quit <- true

	t.Log("closeListenerOnQuite() : Test 1 PASSED.")

}
