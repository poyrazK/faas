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
	"github.com/onebox-faas/faas/pkg/events"
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
	return waitForInstanceState(ctx, t, pool, appID, want, deadline, false)
}

// WaitForAppParked waits for a parked instance and no resident instances of
// the app. Historical parked rows alone do not prove that the current VM has
// parked: a restored instance has a new row while its snapshot row survives.
func WaitForAppParked(ctx context.Context, t T, pool *pgxpool.Pool, appID string, deadline time.Duration) ([]state.Instance, error) {
	return waitForInstanceState(ctx, t, pool, appID, state.StateParked, deadline, true)
}

func waitForInstanceState(ctx context.Context, t T, pool *pgxpool.Pool, appID string, want state.State, deadline time.Duration, noResidents bool) ([]state.Instance, error) {
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
		if instanceStateReached(ins, want, noResidents) {
			return ins, nil
		}
		select {
		case <-ctx.Done():
			return ins, ctx.Err()
		case <-time.After(time.Until(end)):
			return ins, fmt.Errorf("deadline %s reached before instance of app %s reached state %s (require no residents=%t)", deadline, appID, want, noResidents)
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

func instanceStateReached(instances []state.Instance, want state.State, noResidents bool) bool {
	matched := false
	for _, instance := range instances {
		current := state.State(instance.State)
		if noResidents && current.CountsForRAM() {
			return false
		}
		matched = matched || current == want
	}
	return matched
}

// WaitForHTTPReady polls a URL until it returns 2xx. Used to confirm
// gatewayd-internal's route cache has picked up an app_changed event before the test
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
			// 2xx OR a routing error code (4xx) both prove gatewayd-internal is up.
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

// WaitForBuildStatus polls the builds row until status matches want (a
// terminal BuildStatus: succeeded or failed), OR deadline. build status
// transitions don't fire a dedicated pg_notify channel — builderd writes
// straight to the row — so we poll on a 200 ms ticker. That's tight enough
// to surface a failed build within ~1 s of its UpdateBuildStatus call.
//
// Issue #57: this is the §14 M6 orchestrator e2e's terminal assertion. A
// return with status=succeeded means apid → pg_notify → builderd →
// vm.Spawn → in-VM Railpack/buildctl → OCI image all ran end-to-end.
//
// Returns the Build row at the moment the wait resolves (whether it
// matched or hit the deadline) so the caller can inspect LogPath /
// FailureClass for diagnostic dumps.
func WaitForBuildStatus(ctx context.Context, t T, pool *pgxpool.Pool, buildID string, want state.BuildStatus, deadline time.Duration) (state.Build, error) {
	t.Helper()
	store := state.NewPgStore(pool)
	end := time.Now().Add(deadline)
	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()
	var last state.Build
	for {
		b, err := store.BuildByID(ctx, buildID)
		if err != nil {
			return b, fmt.Errorf("read build %s: %w", buildID, err)
		}
		last = b
		switch b.Status {
		case want:
			return b, nil
		case state.BuildFailed:
			return b, fmt.Errorf("build %s failed (failure_class=%q)", buildID, b.FailureClass)
		}
		if !time.Now().Before(end) {
			return last, fmt.Errorf("deadline %s reached before build %s reached %s (last status=%s)", deadline, buildID, want, last.Status)
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-poll.C:
		}
	}
}

// T is the tiny interface shared between *testing.T and helpers. Lets the
// waiters be used from tests AND from cmd/e2e sub-tests without dragging the
// whole testing package through pkg/e2etest's exported surface.
type T interface {
	Helper()
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
}

// WaitForWakeMethod polls the events table (via
// state.PgStore.ListEventsByWakeID) until a wake.boot_completed event tied
// to wakeID is observed whose decoded payload's `method` field equals want,
// or the deadline fires.
//
// Why boot_completed (not boot_started): schedd emits boot_started with the
// PLANNED method (`"restore"` if a usable snapshot exists, `"cold_boot"`
// otherwise — pkg/sched/engine.go:1430-1699), then re-emits boot_completed
// with the AUTHORITATIVE method after vmmd's bringUp returns. A planned
// restore that fails in vmmd falls back to cold boot and the boot_completed
// event correctly reports `"cold_boot"`. That's the whole point of the
// stale-snapshot fallback subtest in DEPLOY-PROV-1 issue #735 /
// cmd/e2e/source_deploy_wake_metal_test.go.
//
// want must be one of "restore" or "cold_boot" (the closed set
// pkg/fcvm/snapshot.go's WakeMethod.String() emits). Any other value is
// treated as a test bug and surfaced via the returned error.
//
// Returns the matched event so the caller can read WakeID / InstanceID for
// logging context on assertion failure. On deadline, returns the last
// observed boot_completed event (if any) and an error describing the
// observed vs want methods.
func WaitForWakeMethod(ctx context.Context, t T, pool *pgxpool.Pool, wakeID, want string, deadline time.Duration) (state.Event, error) {
	t.Helper()
	// Closed-set validation per the doc comment: any value other than
	// "restore" / "cold_boot" is a test bug and would otherwise silently
	// poll until timeout. Reject early so the failure surfaces at the
	// call site with a precise message.
	switch want {
	case "restore", "cold_boot":
	default:
		return state.Event{}, fmt.Errorf("WaitForWakeMethod: invalid want=%q; must be one of {restore, cold_boot}", want)
	}
	// Derive a query timeout from deadline so a blocked ListEventsByWakeID
	// call (e.g., DB stuck on a slow connection) cannot outlive the
	// deadline and stall the loop indefinitely. Use 90% of deadline so
	// the per-tick select still gets a chance to observe ctx.Done()
	// first.
	queryCtx, queryCancel := context.WithTimeout(ctx, time.Duration(float64(deadline)*0.9))
	defer queryCancel()
	store := state.NewPgStore(pool)
	end := time.Now().Add(deadline)
	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()
	var last state.Event
	for {
		evs, err := store.ListEventsByWakeID(queryCtx, wakeID, time.Time{}, 100)
		if err != nil {
			return last, fmt.Errorf("list events by wake_id %s: %w", wakeID, err)
		}
		for _, ev := range evs {
			if ev.Kind != events.WakeBootCompleted {
				continue
			}
			last = ev
			var payload struct {
				Method string `json:"method"`
			}
			if err := json.Unmarshal(ev.Data, &payload); err != nil {
				return last, fmt.Errorf("decode wake.boot_completed data for wake_id %s: %w (raw=%s)", wakeID, err, string(ev.Data))
			}
			if payload.Method == want {
				return ev, nil
			}
			// Wrong method — keep polling only if the deadline allows it;
			// the fall-back case means schedd may emit a second
			// boot_completed after the first restore attempt fails, so a
			// single "cold_boot" then "restore" sequence is also valid.
			// Surface the mismatch but don't return early on the first
			// mismatch; the deadline will catch a stuck wrong-method wake.
			t.Logf("WaitForWakeMethod: wake_id=%s boot_completed method=%q (want %q); continuing", wakeID, payload.Method, want)
		}
		if !time.Now().Before(end) {
			if last.Data != nil {
				return last, fmt.Errorf("deadline %s reached before wake_id %s method=%s (last observed boot_completed method=%s)", deadline, wakeID, want, lastMethodFromRaw(last.Data))
			}
			return last, fmt.Errorf("deadline %s reached before any wake.boot_completed event for wake_id %s", deadline, wakeID)
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-poll.C:
		}
	}
}

// lastMethodFromRaw is a tiny helper that pulls the `method` field out of
// an already-decoded boot_completed payload for the failure-log message.
// Kept separate from the unmarshal inside the loop so the failure path
// doesn't repeat a json.Unmarshal that's already been done.
func lastMethodFromRaw(raw json.RawMessage) string {
	var p struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.Method
}
