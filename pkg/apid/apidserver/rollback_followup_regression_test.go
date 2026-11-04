package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestMonitorClaimRecoversAfterCrash(t *testing.T) {
	e, app, target, current, incidentID := routeMonitorRollbackFixture(t, "rollback", time.Minute)
	mem := *e.Store()
	if _, claimed, err := mem.ClaimRouteMonitorRollback(t.Context(), app.AccountID, app.ID); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	// The process dies here before preparing an operation. Its replacement
	// sees the same durable incident and tries to perform the next sweep.
	(*e.S()).ApplyRouteMonitorRollback(t.Context(), state.RouteMonitorTarget{AccountID: app.AccountID, AppID: app.ID})
	i, err := mem.GetRouteMonitorIncident(t.Context(), app.AccountID, app.ID, incidentID)
	if err != nil || i.Rollback == nil || i.Rollback.Status != "requested" {
		t.Fatalf("monitor never recovers abandoned claim: rollback=%+v err=%v", i.Rollback, err)
	}
	op, err := mem.GetCheckedRollback(t.Context(), app.AccountID, app.ID, i.Rollback.OperationID)
	if err != nil || op.TargetDeploymentID != target.ID || op.CurrentDeploymentID != current.ID {
		t.Fatalf("incident linked the wrong operation: %+v %v", op, err)
	}
	(*e.S()).ApplyRouteMonitorRollback(t.Context(), state.RouteMonitorTarget{AccountID: app.AccountID, AppID: app.ID})
	pending, err := mem.ListPendingCheckedRollbacks(t.Context())
	if err != nil || len(pending) != 1 || pending[0].ID != op.ID {
		t.Fatalf("retry duplicated rollback intent: %+v %v", pending, err)
	}
}

type transientMonitorRollbackRead struct {
	MemStore *state.MemStore
	fail     bool
}

func (s *transientMonitorRollbackRead) AppByID(ctx context.Context, id string) (state.App, error) {
	if s.fail {
		s.fail = false
		return state.App{}, errors.New("temporary app lookup failure")
	}
	return s.MemStore.AppByID(ctx, id)
}

func TestMonitorClaimRetriesTransientReadFailure(t *testing.T) {
	e, app, _, _, id := routeMonitorRollbackFixture(t, "rollback", time.Minute)
	mem := *e.Store()
	(*(*e.S()).Store()) = statetest.NewMem(&transientMonitorRollbackRead{MemStore: mem, fail: true})
	target := state.RouteMonitorTarget{AccountID: app.AccountID, AppID: app.ID}
	(*e.S()).ApplyRouteMonitorRollback(t.Context(), target)
	i, err := mem.GetRouteMonitorIncident(t.Context(), app.AccountID, app.ID, id)
	if err != nil || i.Rollback == nil || i.Rollback.Status != "claimed" {
		t.Fatalf("temporary lookup failure consumed the claim: %+v %v", i.Rollback, err)
	}
	(*e.S()).ApplyRouteMonitorRollback(t.Context(), target)
	i, err = mem.GetRouteMonitorIncident(t.Context(), app.AccountID, app.ID, id)
	if err != nil || i.Rollback == nil || i.Rollback.Status != "requested" {
		t.Fatalf("temporary lookup failure was not retried: %+v %v", i.Rollback, err)
	}
}

func TestCheckedRollbackHonorsNewFreeze(t *testing.T) {
	e, app, current, target := apidserver.PromotionFixture(t)
	mem, ctx := *e.Store(), t.Context()
	r := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/rollback", api.RollbackRequest{TargetDeploymentID: &target.ID, ExpectedCurrentDeploymentID: &current.ID}, nil)
	var accepted api.DeploymentResponse
	if r.Code != http.StatusAccepted || json.Unmarshal(r.Body.Bytes(), &accepted) != nil || accepted.RollbackOperation == nil {
		t.Fatalf("admission=%d %s", r.Code, r.Body)
	}
	if err := mem.MarkDeploymentLive(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	// The existing fixture dates its candidate one second ahead.
	// Begin the freeze after that timestamp, as a real later freeze would.
	if delay := time.Until(target.CreatedAt) + time.Millisecond; delay > 0 {
		time.Sleep(delay)
	}
	now := time.Now().UTC()
	freeze, err := mem.CreateReleaseFreeze(ctx, state.ReleaseFreeze{AccountID: app.AccountID, AppID: app.ID, Kind: state.ReleaseFreezeWindow, StartsAt: &now, Reason: "operator froze new releases during rollback readiness", CreatedBy: "ops"}, api.ReleaseFreezesMax, now)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := mem.DeploymentByID(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if problem := (*e.S()).RollbackTargetProblemForTest(ctx, app, fresh); problem == nil || problem.Code != api.CodeReleaseFrozen {
		t.Fatalf("fixture did not create a frozen never-served target: %+v", problem)
	}
	if err := (*e.S()).CheckedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	op, err := mem.GetCheckedRollback(ctx, app.AccountID, app.ID, accepted.RollbackOperation.ID)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err = mem.DeploymentByID(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if op.Status != "blocked" || op.Code != api.CodeReleaseFrozen || fresh.TrafficPercent != 0 {
		t.Fatalf("frozen target was routed: operation=%s target_traffic=%d", op.Status, fresh.TrafficPercent)
	}
	if _, err := mem.LiftReleaseFreeze(ctx, state.ReleaseFreezeOwner{AccountID: app.AccountID}, freeze.ID, "ops", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := (*e.S()).CheckedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	op, err = mem.GetCheckedRollback(ctx, app.AccountID, app.ID, accepted.RollbackOperation.ID)
	if err != nil || op.Status != "complete" {
		t.Fatalf("lifting freeze did not resume rollback: %+v %v", op, err)
	}
}

func TestCheckedRollbackKnownGoodTargetIgnoresNewFreeze(t *testing.T) {
	e, app, target, current := apidserver.CheckedRollbackAPIFixture(t)
	mem, ctx := *e.Store(), t.Context()
	op, err := mem.CreateCheckedRollback(ctx, app.AccountID, app.ID, target.ID, current.ID, "restore known-good release")
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.MarkDeploymentLive(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := mem.CreateReleaseFreeze(ctx, state.ReleaseFreeze{AccountID: app.AccountID, AppID: app.ID, Kind: state.ReleaseFreezeWindow, StartsAt: &now, Reason: "freeze forward releases", CreatedBy: "ops"}, api.ReleaseFreezesMax, now); err != nil {
		t.Fatal(err)
	}
	if err := (*e.S()).CheckedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	op, err = mem.GetCheckedRollback(ctx, app.AccountID, app.ID, op.ID)
	if err != nil || op.Status != "complete" {
		t.Fatalf("known-good rollback was held: %+v %v", op, err)
	}
}

type transientRollbackFreezeRead struct {
	MemStore *state.MemStore
	fail     bool
}

func (s *transientRollbackFreezeRead) ListReleaseFreezes(ctx context.Context, owner state.ReleaseFreezeOwner, now time.Time) ([]state.ReleaseFreeze, error) {
	if s.fail {
		s.fail = false
		return nil, errors.New("temporary freeze lookup failure")
	}
	return s.MemStore.ListReleaseFreezes(ctx, owner, now)
}

func TestCheckedRollbackWaitsForReleaseControlsToBeReadable(t *testing.T) {
	e, app, current, target := apidserver.PromotionFixture(t)
	mem, ctx := *e.Store(), t.Context()
	op, err := mem.CreateCheckedRollback(ctx, app.AccountID, app.ID, target.ID, current.ID, "verify current controls")
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.MarkDeploymentLive(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	(*(*e.S()).Store()) = statetest.NewMem(&transientRollbackFreezeRead{MemStore: mem, fail: true})
	if err := (*e.S()).CheckedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	op, err = mem.GetCheckedRollback(ctx, app.AccountID, app.ID, op.ID)
	if err != nil || op.Status != "blocked" {
		t.Fatalf("unreadable controls did not hold traffic: %+v %v", op, err)
	}
	live, err := mem.LiveDeploymentForScope(ctx, app.ID, target.Scope)
	if err != nil || live.ID != current.ID {
		t.Fatalf("unreadable controls changed traffic: %+v %v", live, err)
	}
	if err := (*e.S()).CheckedRollbackSweep(ctx); err != nil {
		t.Fatal(err)
	}
	op, err = mem.GetCheckedRollback(ctx, app.AccountID, app.ID, op.ID)
	if err != nil || op.Status != "complete" {
		t.Fatalf("control lookup was not retried: %+v %v", op, err)
	}
}
