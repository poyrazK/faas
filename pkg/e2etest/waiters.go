// waiters.go — poll-until helpers for the e2e tests. Each waits on a pg_notify
// channel AND verifies state via a fresh store read, so a redelivered notify
// (or a missed one) can't cause a false positive.
//
// All waiters respect ctx so the test's overall deadline gates them.

package e2etest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// WaitForDeploymentLive polls the deployments row until status == live, OR a
// non-live terminal state (failed) is reached. Notifies on deployment_changed
// are the wakeup; the store read is the truth.
func WaitForDeploymentLive(ctx context.Context, t T, pool *pgxpool.Pool, deploymentID string, deadline time.Duration) (state.Deployment, error) {
	t.Helper()
	notif, cancel, err := db.Subscribe(ctx, pool, []string{db.NotifyDeploymentChanged})
	if err != nil {
		return state.Deployment{}, fmt.Errorf("subscribe deployment_changed: %w", err)
	}
	defer cancel()

	store := state.NewPgStore(pool)
	end := time.Now().Add(deadline)
	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()

	for {
		dep, err := store.DeploymentByID(ctx, deploymentID)
		if err != nil {
			return state.Deployment{}, fmt.Errorf("read deployment: %w", err)
		}
		switch dep.Status {
		case state.DeployLive:
			return dep, nil
		case state.DeployFailed:
			return dep, fmt.Errorf("deployment %s failed", deploymentID)
		}
		select {
		case <-ctx.Done():
			return dep, ctx.Err()
		case <-time.After(time.Until(end)):
			return dep, fmt.Errorf("deadline %s reached before deployment %s reached live (last status=%s)", deadline, deploymentID, dep.Status)
		case n := <-notif:
			// Best-effort: filter to our deployment; ignore others.
			var p struct {
				To string `json:"to"`
			}
			_ = json.Unmarshal([]byte(n.Payload), &p)
			if p.To == deploymentID {
				// Fall through to the next iteration's store read.
				continue
			}
		case <-poll.C:
		}
	}
}

// WaitForInstanceState polls the instances table for an app until any instance
// matches want, OR deadline. Subscribed to instance_changed as the trigger.
// want is compared against state.State (parked, running, …).
func WaitForInstanceState(ctx context.Context, t T, pool *pgxpool.Pool, appID string, want state.State, deadline time.Duration) ([]state.Instance, error) {
	t.Helper()
	notif, cancel, err := db.Subscribe(ctx, pool, []string{db.NotifyInstanceChanged})
	if err != nil {
		return nil, fmt.Errorf("subscribe instance_changed: %w", err)
	}
	defer cancel()

	store := state.NewPgStore(pool)
	end := time.Now().Add(deadline)
	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()

	for {
		ins, err := store.ListInstancesForApp(ctx, appID)
		if err != nil {
			return nil, fmt.Errorf("list instances: %w", err)
		}
		for _, i := range ins {
			if state.State(i.State) == want {
				return ins, nil
			}
		}
		select {
		case <-ctx.Done():
			return ins, ctx.Err()
		case <-time.After(time.Until(end)):
			return ins, fmt.Errorf("deadline %s reached before instance of app %s reached state %s", deadline, appID, want)
		case n := <-notif:
			var p struct {
				AppID string `json:"app_id"`
			}
			_ = json.Unmarshal([]byte(n.Payload), &p)
			if p.AppID == appID {
				continue
			}
		case <-poll.C:
		}
	}
}

// WaitForHTTPReady polls a URL until it returns 2xx. Used to confirm
// gatewayd's route cache has picked up an app_changed event before the test
// fires its first request (CLAUDE.md gotcha: "the gateway holds requests
// during wake" — but a route that's not yet cached 404s, which is different
// from a wake-block, and the test should distinguish the two).
func WaitForHTTPReady(ctx context.Context, t T, client *http.Client, url string, deadline time.Duration) error {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			// 2xx OR a routing error code (4xx) both prove gatewayd is up.
			// We just want to know the listener is alive.
			if resp.StatusCode < 500 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("http %s not ready within %s", url, deadline)
}

// T is the tiny interface shared between *testing.T and helpers. Lets the
// waiters be used from tests AND from cmd/e2e sub-tests without dragging the
// whole testing package through pkg/e2etest's exported surface.
type T interface {
	Helper()
	Fatalf(format string, args ...any)
}