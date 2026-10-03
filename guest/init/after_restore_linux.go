//go:build linux

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type afterRestoreRuntime struct {
	hook api.AfterRestoreHook
	port int
}

type beforeCheckpointRuntime struct {
	hook api.BeforeCheckpointHook
	port int
}

// callAfterRestoreHook invokes only the restored guest's loopback listener.
// It intentionally uses a fresh connection and never follows redirects: a
// hook is a local readiness barrier, not a general outbound HTTP request.
func callAfterRestoreHook(hook api.AfterRestoreHook, port int) error {
	if err := hook.Validate(); err != nil {
		return fmt.Errorf("after_restore config: %w", err)
	}
	return callLifecycleHook("after_restore", "X-Faas-After-Restore", hook.Path, hook.EffectiveTimeout(), port)
}

func callBeforeCheckpointHook(hook api.BeforeCheckpointHook, port int) error {
	if err := hook.Validate(); err != nil {
		return fmt.Errorf("before_checkpoint config: %w", err)
	}
	return callLifecycleHook("before_checkpoint", "X-Faas-Before-Checkpoint", hook.Path, hook.EffectiveTimeout(), port)
}

func callLifecycleHook(name, header, path string, timeout time.Duration, port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("%s: invalid application port %d", name, port)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	endpoint := url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Path:   path,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("%s request: %w", name, err)
	}
	// Gateway forwarding strips inbound x-faas-* headers, so a public
	// request cannot impersonate this loopback-only lifecycle call.
	req.Header.Set(header, "1")
	client := &http.Client{
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s delivery: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%s returned HTTP %d", name, resp.StatusCode)
	}
	return nil
}
