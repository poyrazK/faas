//go:build metal

// worker_frozen_metal_test.go — frozen worker pools (ADR-20261019103000) on
// real Firecracker. With schedd's frozen_tier and frozen_tier_workers on, a
// pool that scales in freezes its surplus worker instead of stopping it, and
// the next message thaws the same VM instead of cold-booting a new one. Each
// cycle times the send to the worker's RUNNING row and to the message's
// lease, and records vmmd's thaw time from wake.thaw_breakdown.
//
// FAAS_WORKER_FROZEN_CYCLES sets the number of thaw cycles (default 3). Each
// cycle waits for the pool's scale-in window
// (api.WorkerScaleInStabilizationSeconds) before the worker freezes again.
//
// Requires /dev/kvm, root, Firecracker on PATH and FAAS_TEST_KERNEL.
package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkerFrozenThawMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal frozen worker test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	cycles := 3
	if raw := os.Getenv("FAAS_WORKER_FROZEN_CYCLES"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			t.Fatalf("FAAS_WORKER_FROZEN_CYCLES=%q: want a positive integer", raw)
		}
		cycles = n
	}
	rig := startWorkerPoolRig(t, "FAAS_FROZEN_TIER=1", "FAAS_FROZEN_TIER_WORKERS=1")
	// A frozen worker is resident but not live, so the pool never drains it
	// on its own within the test: delete the app, which retires every VM.
	t.Cleanup(func() { retireWorkerApp(t, rig) })

	// The deployment's prime worker is not a reliable subject: a pool
	// reconcile that runs before apid marks the deployment live sees it as a
	// retired generation and stops it. Let it settle, then boot a worker
	// with one message; once its scale-in window passes it is surplus and
	// freezes instead of stopping.
	if err := waitForIdleWorkerPool(t, rig.h, rig.appID, 4*time.Minute); err != nil {
		t.Fatal(err)
	}
	frozenID := frozenWorkerOf(t, rig)
	if frozenID == "" {
		booted := measureWorkerScaleFromZero(t, rig.h, rig.key, rig.slug, rig.appID, 1)
		frozenID = waitForFrozenWorker(t, rig, booted[0].instanceID, 4*time.Minute)
	}
	var thaws []time.Duration
	for cycle := 1; cycle <= cycles; cycle++ {
		running, lease, vmmdThaw := measureWorkerThaw(t, rig, frozenID)
		t.Logf("frozen worker cycle %d (%s): send→running=%s send→lease=%s vmmd_thaw=%s",
			cycle, frozenID, msOrDash(running), msOrDash(lease), vmmdThaw)
		thaws = append(thaws, running)
		if running >= api.CharacterizationDeadline/2 {
			t.Errorf("cycle %d: the frozen worker was RUNNING %s after the send; a thaw must not take a boot's time", cycle, msOrDash(running))
		}
		if cycle < cycles {
			if again := waitForFrozenWorker(t, rig, frozenID, 4*time.Minute); again != frozenID {
				t.Fatalf("cycle %d: worker %s froze, want the thawed worker %s to freeze again", cycle, again, frozenID)
			}
		}
	}
	t.Logf("frozen worker summary over %d thaws: send→running %s", len(thaws), durationSpread(thaws))
}

// frozenWorkerOf returns one of the pool's FROZEN workers, or "".
func frozenWorkerOf(t *testing.T, rig *workerPoolRig) string {
	t.Helper()
	rows, err := state.NewPgStore(rig.pool).ListInstancesForApp(context.Background(), rig.appID)
	if err != nil {
		t.Fatalf("list workers: %v", err)
	}
	for _, ins := range rows {
		if ins.Mode == string(state.InstanceModeWorker) && state.State(ins.State) == state.StateFrozen {
			return ins.ID
		}
	}
	return ""
}

// waitForFrozenWorker waits for one of the pool's workers to be FROZEN (want
// "" for any, else that instance) and returns its id. No worker may stop on
// the way: with the switch on a surplus worker freezes.
func waitForFrozenWorker(t *testing.T, rig *workerPoolRig, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		rows, err := state.NewPgStore(rig.pool).ListInstancesForApp(context.Background(), rig.appID)
		if err != nil {
			t.Fatalf("list workers: %v", err)
		}
		for _, ins := range rows {
			if ins.Mode != string(state.InstanceModeWorker) {
				continue
			}
			if state.State(ins.State) == state.StateFrozen && (want == "" || ins.ID == want) {
				return ins.ID
			}
			if want != "" && ins.ID == want && state.State(ins.State) == state.StateStopped {
				logWorkerTransitions(t, rig)
				t.Fatalf("worker %s stopped instead of freezing", want)
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	logWorkerTransitions(t, rig)
	t.Fatalf("no frozen worker within %s; frozen_tier_workers must freeze the pool's surplus worker", timeout)
	return ""
}

// logWorkerTransitions logs each of the app's workers with the transitions
// schedd recorded for it, which name the path that froze or stopped it.
func logWorkerTransitions(t *testing.T, rig *workerPoolRig) {
	t.Helper()
	rows, err := rig.pool.Query(context.Background(), `select i.id::text, i.state, e.at, e.kind, coalesce(e.data, '{}'::jsonb)::text
		from instances i left join events e on e.subject = i.id
		where i.app_id = $1 and i.mode = 'worker' order by i.id, e.at, e.id`, rig.appID)
	if err != nil {
		t.Logf("worker transitions: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, st string
		var at *time.Time
		var kind *string
		var data string
		if err := rows.Scan(&id, &st, &at, &kind, &data); err != nil {
			t.Logf("worker transitions: %v", err)
			return
		}
		if at == nil || kind == nil {
			t.Logf("worker %s (%s): no events", id, st)
			continue
		}
		t.Logf("worker %s (%s): %s %s %s", id, st, at.UTC().Format("15:04:05.000"), *kind, data)
	}
}

// measureWorkerThaw sends one message to a pool whose only worker is frozen
// and times the thaw: send to the worker's RUNNING row and to the message's
// lease. The pool must thaw that worker, not boot another.
func measureWorkerThaw(t *testing.T, rig *workerPoolRig, frozenID string) (running, lease time.Duration, vmmdThaw string) {
	t.Helper()
	ctx := context.Background()
	store := state.NewPgStore(rig.pool)
	before, err := store.ListInstancesForApp(ctx, rig.appID)
	if err != nil {
		t.Fatalf("list workers: %v", err)
	}
	known := make([]string, 0, len(before))
	for _, ins := range before {
		known = append(known, ins.ID)
	}
	sent := time.Now()
	body, status := doReq(t, rig.h, rig.key, http.MethodPost, "/v1/apps/"+rig.slug+"/queues/send",
		api.QueueSendRequest{QueueName: scaleFromZeroQueue, Payload: json.RawMessage(`{"thaw":true}`)})
	if status != http.StatusCreated {
		t.Fatalf("send: status=%d body=%s", status, body)
	}
	deadline := sent.Add(2 * time.Minute)
	for time.Now().Before(deadline) && running == 0 {
		rows, err := store.ListInstancesForApp(ctx, rig.appID)
		if err != nil {
			t.Fatalf("list workers: %v", err)
		}
		for _, ins := range rows {
			if ins.Mode == string(state.InstanceModeWorker) && !slices.Contains(known, ins.ID) {
				t.Fatalf("the pool booted worker %s instead of thawing frozen worker %s", ins.ID, frozenID)
			}
			if ins.ID == frozenID && state.State(ins.State) == state.StateRunning {
				running = time.Since(sent)
			}
		}
		if running == 0 {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if running == 0 {
		t.Fatalf("frozen worker %s did not thaw within 2m", frozenID)
	}
	if !leaseAndAckOne(t, rig.h, rig.key, rig.slug) {
		t.Fatal("the thawed worker's message was not ready to lease")
	}
	lease = time.Since(sent)
	return running, lease, thawBreakdownSince(t, rig, frozenID, sent)
}

// thawBreakdownSince reads vmmd's thaw time from the newest wake.thaw_breakdown
// for the instance since the send.
func thawBreakdownSince(t *testing.T, rig *workerPoolRig, instanceID string, since time.Time) string {
	t.Helper()
	var data []byte
	err := rig.pool.QueryRow(context.Background(), `select data from events
		where kind = $1 and data->>'instance_id' = $2 and at >= $3 order by at desc, id desc limit 1`,
		events.WakeThawBreakdown, instanceID, since.Add(-time.Second)).Scan(&data)
	if err != nil {
		return "-"
	}
	var payload struct {
		VmmdThawMs int64 `json:"vmmd_thaw_ms"`
		TotalMs    int64 `json:"total_ms"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return "-"
	}
	return fmt.Sprintf("%dms (schedd thaw %dms)", payload.VmmdThawMs, payload.TotalMs)
}

// retireWorkerApp deletes the app and waits until none of its VMs is
// resident, frozen ones included.
func retireWorkerApp(t *testing.T, rig *workerPoolRig) {
	t.Helper()
	_, _ = doReq(t, rig.h, rig.key, http.MethodDelete, "/v1/apps/"+rig.slug, nil)
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		rows, err := state.NewPgStore(rig.pool).ListInstancesForApp(context.Background(), rig.appID)
		if err != nil {
			t.Errorf("teardown: list instances: %v", err)
			return
		}
		resident := false
		for _, ins := range rows {
			resident = resident || state.State(ins.State).CountsForRAM()
		}
		if !resident {
			return
		}
		time.Sleep(time.Second)
	}
	t.Errorf("teardown: the deleted worker app still has resident VMs; they will leak")
}
