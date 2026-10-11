package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type operationalSummaryTestStore struct {
	state.Store
	state.RouteMonitorStore
	report    api.RouteMonitorReport
	incident  *api.AppOperationalIncident
	rollbacks []api.RollbackOperation
	restarts  []api.RuntimeConfigRestartStatusResponse
	err       error
}

func (s operationalSummaryTestStore) GetRouteMonitorReport(context.Context, string, string) (api.RouteMonitorReport, error) {
	return s.report, s.err
}
func (s operationalSummaryTestStore) ListAppPendingRollbacks(context.Context, string, string) ([]api.RollbackOperation, error) {
	return s.rollbacks, s.err
}
func (s operationalSummaryTestStore) GetAppOpenMonitorIncident(context.Context, string, string) (*api.AppOperationalIncident, error) {
	return s.incident, s.err
}
func (s operationalSummaryTestStore) ListAppPendingRestarts(context.Context, string, string) ([]api.RuntimeConfigRestartStatusResponse, error) {
	return s.restarts, s.err
}

func TestAppOperationalSummaryKeepsUnknownHealthAndOpenRecovery(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForTimeline(t, e, "operations-summary")
	now := time.Now().UTC()
	store := operationalSummaryTestStore{Store: e.store, RouteMonitorStore: e.store,
		report: api.RouteMonitorReport{AppID: app.ID, DeploymentID: "serving", Enabled: true, Status: "unknown", Reason: "insufficient_requests", Coverage: "observed_only", CheckedAt: now,
			Routes: []api.RouteMonitorFinding{{Windows: []api.RouteMonitorWindow{{Start: now.Add(-3 * time.Minute), End: now.Add(-2 * time.Minute)}}}}},
		incident:  &api.AppOperationalIncident{ID: uuid.NewString(), DeploymentID: "serving", OpenedAt: now.Add(-time.Hour)},
		rollbacks: []api.RollbackOperation{{ID: "rollback", AppID: app.ID, Status: "blocked", Code: "binding_verification_missing", Scope: "default", TargetDeploymentID: "target", CurrentDeploymentID: "serving", Reason: "PRIVATE_REASON", Blockers: []api.BindingCheckFinding{{Message: "PRIVATE_BLOCKER"}}}},
		restarts:  []api.RuntimeConfigRestartStatusResponse{{WakeID: "restart", Status: "retrying", Attempts: 3, FailureReason: "requests_active", RequestedAt: now}},
	}
	e.s.store = store
	before, _ := e.store.ListInstancesForApp(t.Context(), app.ID)
	rec := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/operational-summary", nil, nil)
	var got api.AppOperationalSummary
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("summary: %d %s", rec.Code, rec.Body)
	}
	if got.Monitoring.Status != "unknown" || got.Monitoring.Incident == nil || got.Monitoring.WindowEnd == nil || !got.Monitoring.WindowEnd.Equal(now.Add(-2*time.Minute)) {
		t.Fatalf("health evidence changed: %+v", got.Monitoring)
	}
	if len(got.Recovery.Rollbacks) != 1 || got.Recovery.Rollbacks[0].Status != "blocked" || len(got.Recovery.Restarts) != 1 || got.Recovery.Restarts[0].Status != "retrying" {
		t.Fatalf("recovery marked complete or missing: %+v", got.Recovery)
	}
	for _, secret := range []string{"PRIVATE_REASON", "PRIVATE_BLOCKER"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("private diagnostics exposed: %s", rec.Body)
		}
	}
	after, _ := e.store.ListInstancesForApp(t.Context(), app.ID)
	if len(before) != len(after) {
		t.Fatal("summary read created an instance")
	}
	if len(got.Recommendations) != 4 {
		t.Fatalf("missing health, incident or recovery actions: %+v", got.Recommendations)
	}
}

func TestAppOperationalSummaryAvailabilityAndBounds(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForTimeline(t, e, "operations-bounds")
	store := operationalSummaryTestStore{Store: e.store, RouteMonitorStore: e.store, report: api.RouteMonitorReport{Status: "disabled"}}
	for i := 0; i <= api.AppOperationalRecoveryLimit; i++ {
		store.rollbacks = append(store.rollbacks, api.RollbackOperation{ID: uuid.NewString(), Status: "preparing"})
		store.restarts = append(store.restarts, api.RuntimeConfigRestartStatusResponse{WakeID: uuid.NewString(), Status: "queued"})
	}
	e.s.store = store
	read := func() api.AppOperationalSummary {
		t.Helper()
		rec := e.do(t, "GET", "/v1/apps/"+app.Slug+"/operational-summary", nil, nil)
		var got api.AppOperationalSummary
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			t.Fatalf("summary: %d %s", rec.Code, rec.Body)
		}
		return got
	}
	got := read()
	if len(got.Recovery.Rollbacks) != api.AppOperationalRecoveryLimit || len(got.Recovery.Restarts) != api.AppOperationalRecoveryLimit || !got.Recovery.RollbacksTruncated || !got.Recovery.RestartsTruncated {
		t.Fatalf("bounds hidden: %+v", got.Recovery)
	}
	store.err = errors.New("PRIVATE_STORE_ERROR")
	e.s.store = store
	got = read()
	if got.Monitoring.Available || got.Recovery.RollbacksAvailable || got.Recovery.RestartsAvailable || got.Monitoring.Status != "unknown" || len(got.Recovery.Rollbacks) != 0 || len(got.Recovery.Restarts) != 0 {
		t.Fatalf("unavailable evidence reported as known: %+v", got)
	}
}

func TestAppOperationalSummaryReadScopeAndOwnership(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedAppForTimeline(t, e, "operations-owned")
	key, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "read-operations", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	path := "/v1/apps/" + app.Slug + "/operational-summary"
	if rec := e.do(t, "GET", path, nil, nil); rec.Code != 200 {
		t.Fatalf("read-only scope rejected: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "GET", path+"?customer_details=true", nil, nil); rec.Code != 400 {
		t.Fatal("identity-detail query accepted")
	}
	other, err := e.store.CreateAccount(t.Context(), "other-operations@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, _ = api.GenerateAPIKey()
	if _, err = e.store.CreateAPIKey(t.Context(), other.ID, hash, "other-read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	e.key = key
	if rec := e.do(t, "GET", path, nil, nil); rec.Code != 404 {
		t.Fatalf("cross-account app disclosed: %d %s", rec.Code, rec.Body)
	}
}

func TestAppOperationalSummaryRequiresCompletedMFA(t *testing.T) {
	e := setup(t, api.PlanPro)
	if err := e.store.MarkMFAEnrolled(t.Context(), e.acct.ID); err != nil {
		t.Fatal(err)
	}
	sid := uuid.NewString()
	if _, err := e.store.CreateSession(t.Context(), sid, e.acct.ID, "192.0.2.10", "operations-test"); err != nil {
		t.Fatal(err)
	}
	token, err := e.s.sessions.IssueWithSession(sid, e.acct.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/v1/apps/mfa-app/operational-summary", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	e.h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("pending MFA read permitted: %d %s", recorder.Code, recorder.Body)
	}
}
