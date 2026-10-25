//go:build !no_pg

// adr: 570
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

type fleetReadinessCounts struct {
	Healthy, Capacity int
	LastEventID       int64
	DroppedEventID    int64
	Refresh           gateway.TargetReadinessRefreshStatus
}

// The watcher still calls the real backend setter. The fixture observes its
// completed receipt so tests do not assume instantaneous cross-process delivery.
type fleetReadinessInvalidator struct {
	*gateway.PGBackend
	lastEvent    atomic.Int64
	droppedEvent atomic.Int64
	drop         bool
}

func (i *fleetReadinessInvalidator) SetInstanceReadinessForTarget(appID, instanceID, wakeID, nodeID, source, status string, at time.Time, eventID int64) {
	if i.drop {
		i.droppedEvent.Store(eventID)
		return
	}
	i.PGBackend.SetInstanceReadinessForTarget(appID, instanceID, wakeID, nodeID, source, status, at, eventID)
	i.lastEvent.Store(eventID)
}

func publishFleetReadiness(t *testing.T, f fleetDaemonFixture, app fleetDaemonApp, instance, source, status string, at time.Time) int64 {
	t.Helper()
	kind := "wake.app_readiness"
	data := map[string]string{"app_id": app.ID, "instance_id": instance, "status": status}
	if source == "sidecar:proxy" {
		kind, data["sidecar_name"] = "wake.sidecar_health", "proxy"
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AppendEventAt(t.Context(), "vmmd", kind, &instance, encoded, at); err != nil {
		t.Fatal(err)
	}
	// The existing trigger publishes the actual committed event ID. Reading
	// the subject journal also verifies persistence of deliberately older events.
	rows, err := f.store.ListEvents(t.Context(), instance, 100)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, row := range rows {
		if row.ID > id {
			id = row.ID
		}
	}
	if id == 0 {
		t.Fatal("readiness event was not durable")
	}
	return id
}

func awaitFleetReadiness(t *testing.T, process *fleetDaemonProcess, app fleetDaemonApp, healthy int, eventID int64) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	for until := time.Now().Add(3 * time.Second); ; {
		response, err := client.Get(process.ready.ReadinessEndpoint)
		if err != nil {
			t.Fatal(err)
		}
		var counts map[string]fleetReadinessCounts
		err = json.NewDecoder(response.Body).Decode(&counts)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		current := counts[app.ID]
		if current.LastEventID >= eventID && current.Healthy == healthy && current.Capacity == len(app.Instances) {
			return
		}
		if time.Now().After(until) {
			t.Fatalf("gateway did not apply committed readiness: %+v want healthy=%d event=%d", current, healthy, eventID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func fleetReadinessProbe(t *testing.T, process *fleetDaemonProcess, want int) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodHead,
		process.ready.ServiceEndpoint+api.ServiceBindingProbePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(api.ServiceBindingProbeRequestHeader, api.ServiceBindingProbeVersion)
	request.Host = "fleet-retry.internal"
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != want {
		t.Fatalf("readiness probe: status=%d want=%d stage=%s", response.StatusCode, want, response.Header.Get(api.ServiceBindingProbeStageHeader))
	}
}

func callFleetReadiness(t *testing.T, process *fleetDaemonProcess, app fleetDaemonApp, surface string) {
	t.Helper()
	request, err := fleetAdmissionRequest(t.Context(), process, app, surface, "/work", false)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "guest served" {
		t.Fatalf("%s readiness request: status=%d body=%q err=%v", surface, response.StatusCode, body, err)
	}
}

func TestTrafficFleetDaemonReadinessWithdrawalRecoveryAndReplacement(t *testing.T) {
	runFleetReadinessRecovery(t, false)
}

func TestTrafficFleetDaemonReadinessRepairWithoutNotifications(t *testing.T) {
	runFleetReadinessRecovery(t, true)
}

func runFleetReadinessRecovery(t *testing.T, repairWithoutNotifications bool) {
	t.Helper()
	f, node := prepareFleetAdmissionReplicas(t, true)
	app := &f.apps[2]
	app.ReadinessSources = []string{"primary_app", "sidecar:proxy"}
	if len(app.Instances) != 2 {
		t.Fatal("readiness fixture needs two real node targets")
	}
	if repairWithoutNotifications {
		// Warm placement stays a fixture. Required sources now come from the
		// actual immutable deployment reader rather than a notification.
		companions, err := json.Marshal([]map[string]any{{"name": "proxy", "type": "sidecar", "port": app.Port, "primary_ingress": true, "readiness_probe": map[string]any{}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(t.Context(), `UPDATE deployments SET override_readiness_probe = '{"path":"/readyz"}'::jsonb,
			sidecars = $2::jsonb WHERE id = $1`, app.Deployment, companions); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().UTC().Add(-time.Minute)
	for _, instance := range app.Instances {
		for _, source := range app.ReadinessSources {
			publishFleetReadiness(t, f, *app, instance, source, "ready", at)
		}
	}
	start := func(index int) *fleetDaemonProcess {
		return startFleetDaemonOptions(t, f.pool, f.apps, f.nodes[0].ID, f.nodes[index].Name, f.usage, true, "", repairWithoutNotifications)
	}
	first, second := start(0), start(1)
	processes := []*fleetDaemonProcess{first, second}
	committed := time.Now()
	await := func(process *fleetDaemonProcess, healthy int, eventID int64) {
		if repairWithoutNotifications {
			awaitFleetReadinessRepair(t, process, *app, healthy, eventID, committed, false)
		} else {
			awaitFleetReadiness(t, process, *app, healthy, eventID)
		}
	}
	for _, process := range processes {
		await(process, 2, 0)
		fleetReadinessProbe(t, process, http.StatusNoContent)
		for _, surface := range []string{"public", "managed"} {
			callFleetReadiness(t, process, *app, surface)
		}
	}
	warmed := time.Now()
	id := publishFleetReadiness(t, f, *app, app.Instances[0], "primary_app", "unready", at.Add(time.Second))
	committed = time.Now()
	for _, process := range processes {
		await(process, 1, id)
	}
	before := observeFleetAdmission(t, node)
	for _, process := range processes {
		for _, surface := range []string{"public", "managed"} {
			callFleetReadiness(t, process, *app, surface)
		}
	}
	after := observeFleetAdmission(t, node)
	if time.Since(warmed) >= 5*time.Second || after.RPCInstances[app.Instances[0]] != before.RPCInstances[app.Instances[0]] ||
		after.RPCInstances[app.Instances[1]] != before.RPCInstances[app.Instances[1]]+4 || after.GuestCalls != before.GuestCalls+4 {
		t.Fatalf("warm routes did not avoid withdrawn target before lease expiry: before=%+v after=%+v elapsed=%s", before, after, time.Since(warmed))
	}
	// Primary readiness cannot compensate for an independently failed sidecar.
	id = publishFleetReadiness(t, f, *app, app.Instances[1], "sidecar:proxy", "unready", at.Add(2*time.Second))
	committed = time.Now()
	for _, process := range processes {
		await(process, 0, id)
		fleetReadinessProbe(t, process, http.StatusServiceUnavailable)
	}
	// A later journal ID with an older observation time must remain ignored.
	id = publishFleetReadiness(t, f, *app, app.Instances[0], "primary_app", "ready", at)
	committed = time.Now()
	for _, process := range processes {
		await(process, 0, id)
		fleetReadinessProbe(t, process, http.StatusServiceUnavailable)
	}
	if observed := observeFleetAdmission(t, node); observed.RPCs != after.RPCs || observed.GuestCalls != after.GuestCalls {
		t.Fatalf("no-wake probes reached node or guest: before=%+v after=%+v", after, observed)
	}
	id = publishFleetReadiness(t, f, *app, app.Instances[0], "primary_app", "ready", at.Add(3*time.Second))
	committed = time.Now()
	for _, process := range processes {
		await(process, 1, id)
		fleetReadinessProbe(t, process, http.StatusNoContent)
		for _, surface := range []string{"public", "managed"} {
			callFleetReadiness(t, process, *app, surface)
		}
	}
	recovered := observeFleetAdmission(t, node)
	if recovered.RPCInstances[app.Instances[0]] != after.RPCInstances[app.Instances[0]]+4 ||
		recovered.RPCInstances[app.Instances[1]] != after.RPCInstances[app.Instances[1]] || recovered.GuestCalls != after.GuestCalls+4 {
		t.Fatalf("recovery failed or sidecar withdrawal was lost: before=%+v after=%+v", after, recovered)
	}
	// Replacement hydrates the durable AND of both sources before serving.
	second.stop(t)
	replacement := start(1)
	if replacement.ready.Generation <= second.ready.Generation {
		t.Fatal("readiness replacement did not advance its serving generation")
	}
	await(replacement, 1, 0)
	before = observeFleetAdmission(t, node)
	for _, surface := range []string{"public", "managed"} {
		callFleetReadiness(t, replacement, *app, surface)
	}
	after = observeFleetAdmission(t, node)
	if after.RPCInstances[app.Instances[1]] != before.RPCInstances[app.Instances[1]] || after.GuestCalls != before.GuestCalls+2 {
		t.Fatalf("replacement routed to durable unready target: before=%+v after=%+v", before, after)
	}
	for _, instance := range app.Instances {
		if after.Instances[instance].Inflight != 0 {
			t.Fatalf("readiness requests retained node permits: %+v", after)
		}
	}
	if repairWithoutNotifications {
		verifyFleetReadinessStoreOutage(t, f, node, *app, first, replacement)
		t.Log("durable readiness repair with notification application dropped and real SQL timeout/recovery preserves capacity and routes through real node/bridge exchanges; placement/probes/guest/namespace are fixtures")
	} else {
		t.Log("durable readiness events, production LISTEN and real node/bridge exchanges exclude withdrawn targets, preserve resident capacity, reject stale events and hydrate replacement; placement/probes/guest/namespace are fixtures")
	}
}

// Unlike notification receipts, this barrier requires a completed durable read
// that started after the transition committed. The control endpoint only reads
// counters; it cannot cause repair. Drop receipts establish the fixture loss.
func awaitFleetReadinessRepair(t *testing.T, process *fleetDaemonProcess, app fleetDaemonApp, healthy int, eventID int64, committed time.Time, outage bool) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	wantUnavailable := 0
	if outage {
		wantUnavailable = len(app.Instances)
	}
	for until := time.Now().Add(3 * time.Second); ; {
		response, err := client.Get(process.ready.ReadinessEndpoint)
		if err != nil {
			t.Fatal(err)
		}
		var counts map[string]fleetReadinessCounts
		err = json.NewDecoder(response.Body).Decode(&counts)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		current := counts[app.ID]
		if !current.Refresh.StartedAt.Before(committed) && !current.Refresh.At.IsZero() && current.Refresh.Checked == len(app.Instances) &&
			current.Refresh.Succeeded != outage && current.Refresh.Unavailable == wantUnavailable && current.Healthy == healthy &&
			current.Capacity == len(app.Instances) && current.DroppedEventID >= eventID && current.LastEventID == 0 {
			return
		}
		if time.Now().After(until) {
			t.Fatalf("durable readiness repair missing: %+v want healthy=%d event=%d outage=%t committed=%s", current, healthy, eventID, outage, committed)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func verifyFleetReadinessStoreOutage(t *testing.T, f fleetDaemonFixture, node fleetAdmissionNode, app fleetDaemonApp, processes ...*fleetDaemonProcess) {
	t.Helper()
	locked, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locked.Rollback(t.Context()) }()
	// This private fixture database lock stalls the actual SQLC configuration
	// query until its production read deadline. No shared cluster is stopped.
	if _, err := locked.Exec(t.Context(), "LOCK TABLE deployments IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	blockedAt := time.Now()
	before := observeFleetAdmission(t, node)
	for _, process := range processes {
		awaitFleetReadinessRepair(t, process, app, 0, 0, blockedAt, true)
		fleetReadinessProbe(t, process, http.StatusServiceUnavailable)
	}
	after := observeFleetAdmission(t, node)
	if after.RPCs != before.RPCs || after.GuestCalls != before.GuestCalls {
		t.Fatalf("failed verification reached node: before=%+v after=%+v", before, after)
	}
	if err := locked.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	releasedAt := time.Now()
	for _, process := range processes {
		awaitFleetReadinessRepair(t, process, app, 1, 0, releasedAt, false)
		fleetReadinessProbe(t, process, http.StatusNoContent)
		for _, surface := range []string{"public", "managed"} {
			callFleetReadiness(t, process, app, surface)
		}
	}
	after = observeFleetAdmission(t, node)
	if after.RPCInstances[app.Instances[0]] != before.RPCInstances[app.Instances[0]]+4 || after.RPCInstances[app.Instances[1]] != before.RPCInstances[app.Instances[1]] || after.GuestCalls != before.GuestCalls+4 {
		t.Fatalf("readiness verification failed to recover exactly the resident target: before=%+v after=%+v", before, after)
	}
	for _, instance := range app.Instances {
		if after.Instances[instance].Inflight != 0 {
			t.Fatal("readiness recovery retained node permit")
		}
	}
}
