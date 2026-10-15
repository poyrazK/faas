package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/mcpmanagement"
)

func validateMCPMetricsListen(address string) error {
	if address == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("--metrics-listen requires a literal loopback IP and port")
	}
	ip := net.ParseIP(host)
	number, err := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || err != nil || number < 0 || number > 65535 {
		return errors.New("--metrics-listen requires a literal loopback IP and a port from 0 to 65535")
	}
	return nil
}

func startMCPMetrics(address string, telemetry *mcpmanagement.Telemetry) (func(), error) {
	if err := validateMCPMetricsListen(address); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("listen for management MCP metrics: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", telemetry.Handler())
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			_, _ = fmt.Fprintln(osStderr, "Management MCP metrics listener stopped unexpectedly")
		}
	}()
	_, _ = fmt.Fprintf(osStderr, "Gregale management MCP metrics listening on %s\n", listener.Addr())
	return func() { _ = server.Close() }, nil
}
