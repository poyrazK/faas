package apidserver_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type unavailableSwitchLookup struct {
	MemStore *state.MemStore
	app      bool
}

func (s unavailableSwitchLookup) AppByID(ctx context.Context, id string) (state.App, error) {
	if s.app {
		return state.App{}, errors.New("app lookup unavailable")
	}
	return s.MemStore.AppByID(ctx, id)
}

func (s unavailableSwitchLookup) DeploymentByID(context.Context, string) (state.Deployment, error) {
	return state.Deployment{}, errors.New("deployment lookup unavailable")
}

func TestDatabaseSwitchReleaseGuardRetriesLookupErrors(t *testing.T) {
	for _, appError := range []bool{false, true} {
		t.Run(fmt.Sprint(appError), func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			d := apidserver.MustSeedDeployment(t, e, "switch-guard-read")
			(*(*e.S()).Store()) = statetest.NewMem(unavailableSwitchLookup{MemStore: *e.Store(), app: appError})
			_, err := (*e.S()).DatabaseSwitchReleaseGuard(t.Context(), state.DatabaseSwitch{AppID: d.AppID, TargetDeploymentID: d.ID})
			if err == nil {
				t.Fatal("unreadable release guard allowed activation")
			}
		})
	}
}

type retryConfigRestoreStore struct {
	MemStore *state.MemStore
	calls    int
}

func (s *retryConfigRestoreStore) ApplyConfigRestore(ctx context.Context, restore state.ConfigRestore) (state.ConfigRevision, error) {
	s.calls++
	if s.calls == 1 {
		return state.ConfigRevision{}, errors.New("transient transaction failure")
	}
	return s.MemStore.ApplyConfigRestore(ctx, restore)
}

func TestConfigGuardRetriesATransientAtomicRestoreFailure(t *testing.T) {
	f := newConfigGuardFixture(t, "guard-restore-retry", true)
	for _, key := range []string{"A", "B"} {
		f.write(http.MethodPut, "/env/"+key+"?guard=false", api.PutAppEnvRequest{Value: "old"}, http.StatusOK)
	}
	for _, key := range []string{"A", "B"} {
		f.write(http.MethodPut, "/env/"+key, api.PutAppEnvRequest{Value: "new"}, http.StatusOK)
	}
	store := &retryConfigRestoreStore{MemStore: *f.e.Store()}
	(*(*f.e.S()).Store()) = statetest.NewMem(store)
	f.telemetry.sample = state.ConfigGuardSample{NewRequests: 40, NewServerErrors: 38}
	f.sweep(time.Now())
	if f.guard().Status != state.ConfigGuardWatching {
		t.Fatal("transient error closed the guard")
	}
	for _, key := range []string{"A", "B"} {
		if value, _ := f.envValue(key); value != "new" {
			t.Fatalf("failed transaction changed %s", key)
		}
	}
	f.sweep(time.Now().Add(time.Second))
	if f.guard().Status != state.ConfigGuardRestored {
		t.Fatal("guard did not retry restoration")
	}
	for _, key := range []string{"A", "B"} {
		if value, _ := f.envValue(key); value != "old" {
			t.Fatalf("retry did not restore %s", key)
		}
	}
}

func TestDatabaseSwitchEnforcedPolicyStillRejectsUnverifiedBindings(t *testing.T) {
	f := newSwitchFixture(t)
	mem := *f.env.Store()
	app, err := mem.AppBySlug(t.Context(), f.slug)
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := mem.SetBindingReleasePolicy(t.Context(), app.AccountID, app.ID, f.v1.Scope, api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.PrepareDeploymentRollback(t.Context(), app.ID, f.v1.ID); err != nil {
		t.Fatal(err)
	}
	// Admission is allowed, but the traffic write still needs fresh binding
	// evidence. The fixture's database binding has not been qualified for v1.
	err = (*f.env.Srv()).DatabaseSwitchPromoteRelease(t.Context(), state.DatabaseSwitch{AppID: app.ID}, f.v1.ID)
	if err == nil || !strings.Contains(err.Error(), "bindings_check_failed") {
		t.Fatalf("prepared rollback did not reject missing binding evidence: %v", err)
	}
	live, err := mem.LiveDeploymentForScope(t.Context(), app.ID, f.v1.Scope)
	if err != nil || live.ID != f.v3.ID {
		t.Fatalf("failed binding check changed serving deployment: %s %v", live.ID, err)
	}
}

type concurrentReleaseTelemetry struct {
	hook    func()
	windows map[string]canary.HealthWindow
}

func (s *concurrentReleaseTelemetry) RequestTelemetryCircuitBreakerSummary(_ context.Context, _, id string, _, _ time.Time) (int64, int64, float64, int64, float64, int64, int64, error) {
	if s.hook != nil {
		hook := s.hook
		s.hook = nil
		hook()
	}
	w := s.windows[id]
	return w.Requests, w.ServerErrors, w.P95LatencyMS, 0, 0, 0, 0, nil
}

func TestPostReleaseWatchPreservesANewerHealthyRelease(t *testing.T) {
	f := newPostReleaseFixture(t, "review-watch", true, time.Minute)
	mem := *f.e.Store()
	ctx := context.Background()
	newest, err := mem.CreateDeployment(ctx, state.Deployment{AppID: f.release.AppID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:new-healthy", Status: state.DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	tele := &concurrentReleaseTelemetry{windows: map[string]canary.HealthWindow{
		f.previous.ID: {Requests: 400, ServerErrors: 1, P95LatencyMS: 40},
		f.release.ID:  {Requests: 300, ServerErrors: 200, P95LatencyMS: 900},
	}}
	tele.hook = func() {
		if err := mem.MarkDeploymentLive(ctx, newest.ID); err != nil {
			t.Fatal(err)
		}
	}
	(*f.e.S()).SetPostReleaseTelemetry(tele)
	f.sweep(t)
	previous, err := mem.DeploymentByID(ctx, f.previous.ID)
	if err != nil {
		t.Fatal(err)
	}
	if previous.Status == state.DeploySnapshotting {
		if err := mem.MarkDeploymentLive(state.WithPreparedRollbackPromotion(ctx), previous.ID); err != nil {
			t.Fatal(err)
		}
	}
	live, err := mem.LiveDeploymentForScope(ctx, f.release.AppID, f.release.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if live.ID != newest.ID {
		t.Fatalf("stale watch of %s replaced newer healthy release %s with %s", f.release.ID, newest.ID, live.ID)
	}
}

func TestDatabaseSwitchAcceptsEnforcedBindingPolicy(t *testing.T) {
	f := newSwitchFixture(t)
	mem := *f.env.Store()
	app, err := mem.AppBySlug(context.Background(), f.slug)
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := mem.SetBindingReleasePolicy(context.Background(), app.AccountID, app.ID, f.v1.Scope, api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	rec := f.do(t, http.MethodPost, "/v1/apps/"+f.slug+"/rollback", api.RollbackRequest{TargetDeploymentID: &f.v1.ID, RestoreDatabases: true, AcknowledgeReleases: true})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("database rollback blocked with enforced binding policy: %d %s", rec.Code, rec.Body)
	}
}

func TestDatabaseSwitchPromotesVerifiedPreparedTarget(t *testing.T) {
	e, app, target, current := apidserver.PromotionFixture(t)
	ctx := t.Context()
	mem := *e.Store()
	apidserver.CompletePromotionProbe(t, e, app, target, apidserver.PassedPostgresVerification)
	if _, err := mem.UpdateDeploymentTraffic(ctx, current.ID, 100); err != nil {
		t.Fatal(err)
	}
	if err := mem.MarkDeploymentLive(ctx, current.ID); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := mem.SetBindingReleasePolicy(ctx, app.AccountID, app.ID, target.Scope, api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	prepared, err := mem.PrepareDeploymentRollback(ctx, app.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Status != state.DeploySnapshotting || prepared.TrafficPercent != 0 {
		t.Fatalf("unexpected prepared target: %+v", prepared)
	}
	if err := (*e.S()).DatabaseSwitchPromoteRelease(ctx, state.DatabaseSwitch{AppID: app.ID, FromDeploymentID: current.ID, TargetDeploymentID: target.ID}, target.ID); err != nil {
		t.Fatalf("verified database rollback could not promote: %v", err)
	}
	live, err := mem.LiveDeploymentForScope(ctx, app.ID, target.Scope)
	if err != nil || live.ID != target.ID || live.TrafficPercent != 100 {
		t.Fatalf("verified target did not receive traffic: %+v %v", live, err)
	}
}

func TestDatabaseSwitchCannotPromoteAWithdrawnRollback(t *testing.T) {
	f := newSwitchFixture(t)
	ctx, mem := t.Context(), *f.env.Store()
	if _, err := mem.PrepareDeploymentRollback(ctx, f.v1.AppID, f.v1.ID); err != nil {
		t.Fatal(err)
	}
	newest, err := mem.CreateDeployment(ctx, state.Deployment{AppID: f.v1.AppID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:newer-switch-release", Status: state.DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.MarkDeploymentLive(ctx, newest.ID); err != nil {
		t.Fatal(err)
	}
	err = (*f.env.Srv()).DatabaseSwitchPromoteRelease(ctx, state.DatabaseSwitch{AppID: f.v1.AppID}, f.v1.ID)
	if !errors.Is(err, state.ErrDeploymentSuperseded) {
		t.Fatalf("withdrawn rollback was not refused: %v", err)
	}
	live, err := mem.LiveDeploymentForScope(ctx, f.v1.AppID, f.v1.Scope)
	if err != nil || live.ID != newest.ID {
		t.Fatalf("withdrawn rollback replaced newer release: %+v %v", live, err)
	}
}

type guardBatchTelemetry struct {
	seen    map[string]int
	failing string
}

func (s *guardBatchTelemetry) ConfigGuardTelemetry(_ context.Context, q state.ConfigGuardTelemetryQuery) (state.ConfigGuardSample, error) {
	s.seen[q.Scope]++
	if q.Scope == s.failing {
		return state.ConfigGuardSample{NewRequests: 40, NewServerErrors: 38}, nil
	}
	return state.ConfigGuardSample{}, nil
}

func TestConfigGuardBatchVisitsLaterFailures(t *testing.T) {
	f := newConfigGuardFixture(t, "review-guard-batch", true)
	mem := *f.e.Store()
	ctx := context.Background()
	now := time.Now().UTC()
	var last string
	for i := 0; i <= api.ConfigGuardBatchSize; i++ {
		scope := fmt.Sprintf("scope-%02d", i)
		last = scope
		at := now.Add(-time.Hour).Add(time.Duration(i) * time.Second)
		if err := mem.UpsertAppEnvInScope(ctx, f.app.AccountID, f.app.ID, scope, "MODE", "old"); err != nil {
			t.Fatal(err)
		}
		ticket, err := mem.BeginConfigChange(ctx, state.ConfigChangeStart{AccountID: f.app.AccountID, AppID: f.app.ID, Scope: scope, Guard: true, Now: at})
		if err != nil {
			t.Fatal(err)
		}
		if err := mem.UpsertAppEnvInScope(ctx, f.app.AccountID, f.app.ID, scope, "MODE", "new"); err != nil {
			t.Fatal(err)
		}
		if _, err := mem.FinishConfigChange(ctx, state.ConfigChangeFinish{AccountID: f.app.AccountID, AppID: f.app.ID, Scope: scope, Source: state.ConfigSourceEnvSet, Key: "MODE", GuardID: ticket.GuardID, Now: at}); err != nil {
			t.Fatal(err)
		}
	}
	telemetry := &guardBatchTelemetry{seen: map[string]int{}, failing: last}
	(*f.e.S()).SetConfigGuardTelemetry(telemetry)
	f.sweep(now)
	f.sweep(now.Add(time.Minute))
	if telemetry.seen[last] == 0 {
		t.Fatalf("guard %s with failing traffic was never checked across two sweeps; first %d quiet guards monopolize the batch", last, api.ConfigGuardBatchSize)
	}
}
