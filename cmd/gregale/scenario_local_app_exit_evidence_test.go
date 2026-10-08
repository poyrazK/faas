//go:build unix

package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
)

func TestManagedLocalShutdownFailureHelper(t *testing.T) {
	if os.Getenv("GREGALE_SHUTDOWN_FAILURE_HELPER") != "1" {
		return
	}
	endpoint := strings.TrimPrefix(os.Args[len(os.Args)-1], "endpoint=")
	listener, err := net.Listen("tcp4", endpoint)
	if err != nil {
		os.Exit(9)
	}
	terminated := make(chan os.Signal, 1)
	signal.Notify(terminated, syscall.SIGTERM)
	go func() {
		_ = http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
	}()
	<-terminated
	os.Exit(7)
}

func TestManagedLocalNonzeroExitDuringShutdownRemainsFailure(t *testing.T) {
	t.Setenv("GREGALE_SHUTDOWN_FAILURE_HELPER", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec := &testLocalAppSpec{
		Command:         []string{exe, "-test.run=^TestManagedLocalShutdownFailureHelper$", "--", "endpoint=${local.host}:${local.port}"},
		Readiness:       testLocalReadinessSpec{Path: "/health", Status: http.StatusNoContent, Timeout: "3s"},
		ShutdownTimeout: "1s",
	}
	_, _ = captureManagedLocalOutput(t)
	receipt := runLocalTest(context.Background(), "shutdown-failure", testScenario{Local: spec}, t.TempDir(), "", testDataCase{})
	if receipt.Status != "failed" || !strings.Contains(receipt.Error, "local app exited before shutdown (exit code 7)") {
		t.Fatalf("nonzero child exit was accepted: %+v", receipt)
	}
	if receipt.LocalApp == nil || !receipt.LocalApp.Ready || receipt.LocalApp.ExitCode == nil || *receipt.LocalApp.ExitCode != 7 || receipt.LocalApp.Shutdown != "exited" {
		t.Fatalf("nonzero exit evidence = %+v", receipt.LocalApp)
	}
}
