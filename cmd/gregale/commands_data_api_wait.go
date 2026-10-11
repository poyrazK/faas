// adr: 650 — observe the existing fresh restart before using a migrated contract.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type dataAPIRefreshReceipt struct {
	api.AppRestartResponse
	Status string `json:"status"`
	Ready  bool   `json:"ready"`
}

func dataAPIReadinessURL(app api.AppResponse) (string, error) {
	address := app.CanonicalURL
	if address == "" {
		address = app.URL
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("readiness requires an HTTPS app URL without credentials, paths, queries or fragments")
	}
	u.Path = "/healthz"
	return u.String(), nil
}

func waitDataAPIRefresh(ctx context.Context, client *api.Client, slug, wakeID, healthURL string, interval time.Duration) error {
	if wakeID == "" {
		return errors.New("control plane did not return a wake_id; cannot verify the accepted refresh")
	}
	if err := waitDataAPIRestart(ctx, client, slug, wakeID, interval); err != nil {
		return dataAPIRefreshWaitError(ctx, wakeID, "restart completion", err)
	}
	// Never send an owner key or follow a redirect to the public app endpoint.
	healthClient := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if err := waitDataAPIReadiness(ctx, healthClient, healthURL, interval); err != nil {
		return dataAPIRefreshWaitError(ctx, wakeID, "readiness", err)
	}
	return nil
}

func waitDataAPIRestart(ctx context.Context, client *api.Client, slug, wakeID string, interval time.Duration) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		status, err := client.GetRuntimeConfigRestartStatus(ctx, slug, wakeID)
		if err != nil {
			return fmt.Errorf("read restart status: %w", err)
		}
		if status.WakeID != wakeID {
			return errors.New("restart status returned a different wake_id")
		}
		switch status.Status {
		case "completed":
			return nil
		case "failed":
			reason := status.FailureReason
			if reason == "" {
				reason = "restart_attempt_failed"
			}
			return fmt.Errorf("fresh restart failed (%s)", reason)
		case "queued", "retrying", "running":
		default:
			return errors.New("control plane returned an unknown restart status")
		}
		if err := dataAPIRefreshPoll(ctx, interval); err != nil {
			return err
		}
	}
}

func waitDataAPIReadiness(ctx context.Context, client *http.Client, address string, interval time.Duration) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ready, err := probeDataAPIReadiness(ctx, client, address)
		if err != nil || ready {
			return err
		}
		if err := dataAPIRefreshPoll(ctx, interval); err != nil {
			return err
		}
	}
}

func probeDataAPIReadiness(ctx context.Context, client *http.Client, address string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return false, errors.New("invalid Data API readiness URL")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(req)
	if err != nil {
		return false, nil // Transport failures can be transient during a cold wake.
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode >= 500 || response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests {
		return false, nil
	}
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("/healthz returned HTTP %d; check the app URL and ingress settings", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (8<<10)+1))
	if err != nil {
		return false, nil
	}
	var health struct {
		Ready *bool `json:"ready"`
	}
	if len(body) > 8<<10 || json.Unmarshal(body, &health) != nil || health.Ready == nil {
		return false, errors.New("/healthz did not return a valid Data API readiness response")
	}
	return *health.Ready, nil
}

func dataAPIRefreshPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func dataAPIRefreshWaitError(ctx context.Context, wakeID, phase string, err error) error {
	if ctx.Err() != nil {
		stopped := "stopped"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			stopped = "timed out"
		}
		return fmt.Errorf("%s waiting for Data API %s (wake_id=%s); the accepted restart is not cancelled: %w", stopped, phase, wakeID, ctx.Err())
	}
	return fmt.Errorf("%s for Data API refresh (wake_id=%s): %w", phase, wakeID, err)
}

// Keep the wait phase and accepted wake ID instead of classifying a local
// deadline as a generic failure to reach the management API.
func dataAPIRefreshDiagnostic(err error) error {
	code, title := "data_api_refresh_cancelled", "Data API refresh wait stopped"
	if errors.Is(err, context.DeadlineExceeded) {
		code, title = "data_api_refresh_timeout", "Data API refresh wait timed out"
	} else if !errors.Is(err, context.Canceled) {
		return err
	}
	return &APIError{Problem: api.Problem{Status: http.StatusRequestTimeout, Code: code, Title: title, Detail: err.Error()}}
}
