//go:build linux

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

type afterRestoreRuntime struct {
	hook api.AfterRestoreHook
	port int
}

// callAfterRestoreHook invokes only the restored guest's loopback listener.
// It intentionally uses a fresh connection and never follows redirects: a
// hook is a local readiness barrier, not a general outbound HTTP request.
func callAfterRestoreHook(hook api.AfterRestoreHook, port int) error {
	if err := hook.Validate(); err != nil {
		return fmt.Errorf("after_restore config: %w", err)
	}
	if port <= 0 || port > 65535 {
		return fmt.Errorf("after_restore: invalid application port %d", port)
	}
	ctx, cancel := context.WithTimeout(context.Background(), hook.EffectiveTimeout())
	defer cancel()
	endpoint := url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Path:   hook.Path,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("after_restore request: %w", err)
	}
	// Gateway forwarding strips inbound x-faas-* headers, so a public
	// request cannot impersonate this loopback-only lifecycle call.
	req.Header.Set("X-Faas-After-Restore", "1")
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("after_restore delivery: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("after_restore returned HTTP %d", resp.StatusCode)
	}
	return nil
}
