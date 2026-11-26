package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"

	"github.com/onebox-faas/faas/pkg/instancebridge"
)

// Environment vmmd sets on a bridge whose activity it reads (connection-
// survival ADR §2, P1d). Without both, the bridge serves no control socket
// and counts nothing.
const (
	envControlSocket = "FAAS_BRIDGE_CONTROL_SOCKET"
	envInstance      = "FAAS_BRIDGE_INSTANCE"
)

// startControl serves the instance's counters on the control socket vmmd
// named and returns them for the server and sessions to count into. It
// returns nil, and changes nothing, when vmmd named none or the socket
// cannot be served: vmmd then reads activity as before.
func startControl(ctx context.Context) *instancebridge.Counters {
	path, instance := os.Getenv(envControlSocket), os.Getenv(envInstance)
	if path == "" || instance == "" {
		return nil
	}
	ln, err := listenControl(path)
	if err != nil {
		slog.Warn("vmmd-stream-bridge: control socket unavailable", "err", err)
		return nil
	}
	counters := &instancebridge.Counters{}
	go func() {
		defer func() { _ = os.Remove(path) }()
		if err := instancebridge.ServeControl(ctx, ln, instance, counters); err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("vmmd-stream-bridge: control socket stopped", "err", err)
		}
	}()
	return counters
}

// listenControl binds the control socket mode 0600: only the bridge's own
// uid and root (vmmd) may read the counters or, later, set the gate.
func listenControl(path string) (net.Listener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

// countRequests counts every HTTP request from its arrival to its handler's
// return, which for a streamed response is the end of the stream.
func countRequests(next http.Handler, counters *instancebridge.Counters) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer counters.BeginRequest()()
		next.ServeHTTP(w, r)
	})
}
