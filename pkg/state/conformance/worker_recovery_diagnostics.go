// adr: 20261017223000
package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func testWorkerRecoveryObservations(t *testing.T, fx *Fixture) {
	recoveryWorker(t, fx)
	now := time.Now().UTC().Truncate(time.Microsecond)
	r, claimed, err := fx.Store.ClaimWorkerRecovery(fx.Ctx, fx.App.ID, "", "r", uuid.NewString(), now, now.Add(time.Minute))
	if err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	r.Status, r.NextAttemptAt = "waiting_capacity", now.Add(5*time.Second)
	requested := int64(512)
	r.Pools = []state.WorkerRecoveryPool{{Scope: "default", Revision: "r", DeploymentID: fx.Deployment.ID,
		ObservedAt: now, Desired: 2, Starting: 1,
		Blockers: []api.WorkerAdmissionBlocker{{Code: "node_ram", Resource: "ram_mb", Requested: &requested, ObservedAt: now}}}}
	if err := fx.Store.CompleteWorkerRecovery(fx.Ctx, r.ClaimToken, r); err != nil {
		t.Fatal(err)
	}
	r.Pools[0].Desired = 100
	*r.Pools[0].Blockers[0].Requested = 999
	got, err := fx.Store.WorkerRecoveryByApp(fx.Ctx, fx.App.ID)
	if err != nil || len(got.Pools) != 1 || got.Pools[0].Desired != 2 || got.Pools[0].Starting != 1 || !got.Pools[0].ObservedAt.Equal(now) {
		t.Fatalf("observations lost or caller alias persisted: %+v %v", got, err)
	}
	if len(got.Pools[0].Blockers) != 1 || got.Pools[0].Blockers[0].Requested == nil || *got.Pools[0].Blockers[0].Requested != 512 {
		t.Fatalf("nested admission evidence aliased or lost: %+v", got.Pools)
	}
	got.Pools[0].Running = 100
	*got.Pools[0].Blockers[0].Requested = 888
	held, claimed, err := fx.Store.ClaimWorkerRecovery(fx.Ctx, fx.App.ID, "", "r", uuid.NewString(), now, now.Add(time.Minute))
	if err != nil || !claimed || len(held.Pools) != 1 || held.Pools[0].Running != 0 {
		t.Fatalf("claim lost observation or read aliased store: %+v %v %v", held, claimed, err)
	}
	if *held.Pools[0].Blockers[0].Requested != 512 {
		t.Fatalf("read changed stored admission evidence: %+v", held.Pools)
	}
	*held.Pools[0].Blockers[0].Requested = 777
	read, err := fx.Store.WorkerRecoveryByApp(fx.Ctx, fx.App.ID)
	if err != nil || *read.Pools[0].Blockers[0].Requested != 512 {
		t.Fatalf("claim aliased stored admission evidence: %+v %v", read, err)
	}
	for _, tc := range []struct {
		name string
		pool state.WorkerRecoveryPool
	}{
		{"negative counts", state.WorkerRecoveryPool{Scope: "default", Revision: "r", ObservedAt: now, Desired: -1}},
		{"different revision", state.WorkerRecoveryPool{Scope: "default", Revision: "obsolete", ObservedAt: now}},
		{"missing time", state.WorkerRecoveryPool{Scope: "default", Revision: "r"}},
		{"future time", state.WorkerRecoveryPool{Scope: "default", Revision: "r", ObservedAt: now.Add(time.Second)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := held
			bad.Pools = []state.WorkerRecoveryPool{tc.pool}
			if err := fx.Store.CompleteWorkerRecovery(fx.Ctx, held.ClaimToken, bad); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("invalid observations accepted: %v", err)
			}
		})
	}
	for _, b := range []api.WorkerAdmissionBlocker{
		{Code: "PRIVATE_RAW_ERROR", ObservedAt: now},
		{Code: "node_ram", ObservedAt: now.Add(time.Second)},
		{Code: "retry_cooldown", ObservedAt: now},
		{Code: "node_ram", ObservedAt: now, Resource: "private-resource"},
	} {
		bad := held
		bad.Pools = []state.WorkerRecoveryPool{{Scope: "default", Revision: "r", ObservedAt: now, Blockers: []api.WorkerAdmissionBlocker{b}}}
		if err := fx.Store.CompleteWorkerRecovery(fx.Ctx, held.ClaimToken, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid admission evidence accepted: %+v %v", b, err)
		}
	}
	// A fresh neighbor cannot make an environment with stale evidence healthy.
	held.Pools[0].ObservedAt = now.Add(-time.Duration(api.WorkerRecoveryObservationFreshnessSeconds+1) * time.Second)
	held.Pools[0].Blockers[0].ObservedAt = held.Pools[0].ObservedAt
	held.Pools = append(held.Pools, state.WorkerRecoveryPool{Scope: "staging", Revision: "r", ObservedAt: now, Running: 3})
	if err := fx.Store.CompleteWorkerRecovery(fx.Ctx, held.ClaimToken, held); err != nil {
		t.Fatal(err)
	}
	if summary, err := fx.Store.WorkerRecoverySummary(fx.Ctx, "", now); err != nil || summary.Unobserved != 1 || summary.Desired != 0 || summary.Running != 3 {
		t.Fatalf("fresh neighbor concealed stale evidence: %+v %v", summary, err)
	}
	reset, claimed, err := fx.Store.ClaimWorkerRecovery(fx.Ctx, fx.App.ID, "", "new", uuid.NewString(), now, now.Add(time.Minute))
	if err != nil || !claimed || len(reset.Pools) != 0 {
		t.Fatalf("new intent retained obsolete counts: %+v %v %v", reset, claimed, err)
	}
}

func testWorkerRecoveryDiagnostics(t *testing.T, fx *Fixture) {
	recoveryWorker(t, fx)
	now := time.Now().UTC().Truncate(time.Microsecond)
	read := func(owner string, at time.Time) state.WorkerRecoverySummary {
		t.Helper()
		summary, err := fx.Store.WorkerRecoverySummary(fx.Ctx, owner, at)
		if err != nil {
			t.Fatal(err)
		}
		return summary
	}
	if s := read("", now); s.Apps != 1 || s.Unobserved != 1 || s.Missing != 0 || s.Overdue != 0 {
		t.Fatalf("unclaimed worker became healthy zero: %+v", s)
	}
	r, claimed, err := fx.Store.ClaimWorkerRecovery(fx.Ctx, fx.App.ID, "", "r", uuid.NewString(), now, now.Add(10*time.Minute))
	if err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	r.Status, r.NextAttemptAt = "waiting_capacity", now.Add(5*time.Minute)
	r.Pools = []state.WorkerRecoveryPool{
		{Scope: "default", Revision: "r", DeploymentID: fx.Deployment.ID, ObservedAt: now, Desired: 2},
		{Scope: "staging", Revision: "r", ObservedAt: now, Desired: 0, Running: 5},
	}
	if err := fx.Store.CompleteWorkerRecovery(fx.Ctx, r.ClaimToken, r); err != nil {
		t.Fatal(err)
	}
	if s := read("", now.Add(time.Minute)); s.Apps != 1 || s.Desired != 2 || s.Running != 5 || s.Missing != 2 || s.Unobserved != 0 || s.Overdue != 0 {
		t.Fatalf("surplus hid neighboring deficit or future retry was overdue: %+v", s)
	}
	grace := time.Duration(api.WorkerRecoveryOverdueGraceSeconds) * time.Second
	if s := read("", r.NextAttemptAt.Add(grace)); s.Overdue != 0 {
		t.Fatalf("deadline grace boundary: %+v", s)
	}
	if s := read("", r.NextAttemptAt.Add(grace+time.Second)); s.Overdue != 1 {
		t.Fatalf("stalled reconciliation was invisible: %+v", s)
	}
	if _, claimed, err := fx.Store.ClaimWorkerRecovery(fx.Ctx, fx.App.ID, "", "r", uuid.NewString(), now.Add(time.Second), now.Add(10*time.Minute)); err != nil || !claimed {
		t.Fatalf("reclaim: %v %v", claimed, err)
	}
	if s := read("", now.Add(7*time.Minute)); s.Overdue != 0 {
		t.Fatalf("live reconciliation lease was overdue: %+v", s)
	}
	if s := read("", now.Add(10*time.Minute+grace+time.Second)); s.Overdue != 1 {
		t.Fatalf("expired lease was invisible: %+v", s)
	}
	if s := read("", now.Add(time.Duration(api.WorkerRecoveryObservationFreshnessSeconds+1)*time.Second)); s.Unobserved != 1 || s.Missing != 0 || s.Running != 0 {
		t.Fatalf("stale observations appeared current: %+v", s)
	}
	if err := fx.Store.SetAppNodeID(fx.Ctx, fx.App.ID, fx.Node.ID); err != nil {
		t.Fatal(err)
	}
	if s := read(uuid.NewString(), now); s != (state.WorkerRecoverySummary{}) {
		t.Fatalf("neighboring owner counted: %+v", s)
	}
	if err := fx.Store.UpdateAccountStatus(fx.Ctx, fx.Account.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if s := read("", now); s != (state.WorkerRecoverySummary{}) {
		t.Fatalf("suspended account retained alerts: %+v", s)
	}
	if err := fx.Store.UpdateAccountStatus(fx.Ctx, fx.Account.ID, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	inactive := state.AppEvictedCold
	if _, err := fx.Store.UpdateApp(fx.Ctx, fx.App.ID, state.UpdateAppParams{Status: &inactive}); err != nil {
		t.Fatal(err)
	}
	if s := read("", now); s != (state.WorkerRecoverySummary{}) {
		t.Fatalf("parked app retained alerts: %+v", s)
	}
}
