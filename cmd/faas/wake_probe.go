// wake_probe.go — `faas open` cold-wake probe (UX §6.4, issue #65 D1).
//
// probeWakeState GETs the public app URL with a tight deadline and
// reports whether the server tagged the response `x-faas-wake: cold`.
// The public app URL is the gateway, not the apid API, so this is a
// raw http.Client call rather than reusing Client.do.
//
// On any error (timeout, DNS, connection reset, 5xx) the helper
// collapses to (false, nil) and lets the caller optimistically open
// the browser — surfacing the wake-state line is a transparency
// affordance, not a gate. A flaky probe must not block `faas open`.

package main

import (
	"context"
	"io"
	"net/http"
	"time"
)

// probeWakeState returns true when url responds with the
// `x-faas-wake: cold` header. Errors are reported separately so the
// caller can choose between "Opening." (silent) and a recovery path
// (logged), but in practice cmdOpen treats them as "warm enough".
func probeWakeState(url string, timeout time.Duration) (cold bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	// Drain a tiny prefix so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	return resp.Header.Get("x-faas-wake") == "cold", nil
}
