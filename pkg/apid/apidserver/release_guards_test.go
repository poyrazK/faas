package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261019071733 — freezes refuse production releases until overridden
// or lifted, and an after-rollback policy freezes an app Gregale rolled back.
// adr: 20261019071734 — release gates round-trip and roll back a release
// that fails one after its rollout completed.

// guardDo sends a request and decodes a 2xx response into out.
func guardDo(t *testing.T, e apidserver.TestEnv, method, path string, body, out any) *httptest.ResponseRecorder {
	t.Helper()
	rec := e.Do(t, method, path, body, nil)
	if out != nil && rec.Code < 300 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s %s response %s: %v", method, path, rec.Body, err)
		}
	}
	return rec
}

func decodeProblem(t *testing.T, body []byte) api.Problem {
	t.Helper()
	var p api.Problem
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode problem %s: %v", body, err)
	}
	return p
}

func TestReleaseFreezeRefusesProductionReleasesUntilOverriddenOrLifted(t *testing.T) {
	f := newPostReleaseFixture(t, "frz-app", true, time.Hour)
	e := f.e
	var freeze api.ReleaseFreeze
	if rec := guardDo(t, e, http.MethodPost, "/v1/freezes", api.CreateReleaseFreezeRequest{App: "frz-app", Kind: "window", Reason: "holiday"}, &freeze); rec.Code != http.StatusCreated {
		t.Fatalf("create freeze = %d %s", rec.Code, rec.Body)
	}
	if !freeze.Active || freeze.App != "frz-app" || freeze.Source != "manual" {
		t.Fatalf("freeze = %+v", freeze)
	}

	rec := guardDo(t, e, http.MethodPost, "/v1/apps/frz-app/deployments", api.CreateDeploymentRequest{Image: imageRef('a')}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("frozen deploy = %d %s, want 409", rec.Code, rec.Body)
	}
	if p := decodeProblem(t, rec.Body.Bytes()); p.Code != api.CodeReleaseFrozen || len(p.Freezes) != 1 || p.Freezes[0].ID != freeze.ID {
		t.Fatalf("problem = %+v", p)
	}

	var status api.ReleaseFreezeStatus
	if rec := guardDo(t, e, http.MethodGet, "/v1/apps/frz-app/freeze-status", nil, &status); rec.Code != http.StatusOK || !status.Frozen || len(status.Active) != 1 {
		t.Fatalf("status = %d %+v", rec.Code, status)
	}

	rec = guardDo(t, e, http.MethodPost, "/v1/apps/frz-app/deployments", api.CreateDeploymentRequest{Image: imageRef('b'), OverrideFreeze: "hotfix for incident 4711"}, nil)
	if rec.Code == http.StatusConflict {
		t.Fatalf("overridden deploy was refused: %s", rec.Body)
	}

	if rec := guardDo(t, e, http.MethodDelete, "/v1/freezes/"+freeze.ID, nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("lift = %d %s", rec.Code, rec.Body)
	}
	if rec := guardDo(t, e, http.MethodDelete, "/v1/freezes/"+freeze.ID, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("second lift = %d, want 404", rec.Code)
	}
	if rec := guardDo(t, e, http.MethodGet, "/v1/apps/frz-app/freeze-status", nil, &status); rec.Code != http.StatusOK || status.Frozen {
		t.Fatalf("status after lift = %d %+v", rec.Code, status)
	}
}

func TestReleaseFreezeValidationAndAccountWideList(t *testing.T) {
	f := newPostReleaseFixture(t, "frz-list", true, time.Hour)
	e := f.e
	bad := api.CreateReleaseFreezeRequest{Kind: "weekly", Reason: "x", Weekly: &api.ReleaseFreezeWeekly{Days: []string{"friday"}, Start: "16:00", DurationMinutes: 60, TimeZone: "UTC"}}
	if rec := guardDo(t, e, http.MethodPost, "/v1/freezes", bad, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad weekly = %d %s, want 400", rec.Code, rec.Body)
	}
	weekly := api.CreateReleaseFreezeRequest{Kind: "weekly", Reason: "no weekend releases",
		Weekly: &api.ReleaseFreezeWeekly{Days: []string{"sat", "sun"}, Start: "00:00", DurationMinutes: 1440, TimeZone: "Europe/Istanbul"}}
	if rec := guardDo(t, e, http.MethodPost, "/v1/freezes", weekly, nil); rec.Code != http.StatusCreated {
		t.Fatalf("weekly = %d %s", rec.Code, rec.Body)
	}
	var list api.ListReleaseFreezesResponse
	if rec := guardDo(t, e, http.MethodGet, "/v1/freezes?app=frz-list", nil, &list); rec.Code != http.StatusOK || len(list.Freezes) != 1 {
		t.Fatalf("list = %d %+v", rec.Code, list)
	}
	if got := list.Freezes[0]; got.App != "" || got.Weekly == nil || len(got.Weekly.Days) != 2 {
		t.Fatalf("account-wide weekly freeze = %+v", got)
	}
}

func TestAfterRollbackPolicyFreezesAnAppTheWatchRolledBack(t *testing.T) {
	f := newPostReleaseFixture(t, "frz-auto", true, time.Minute)
	e := f.e
	policy := api.CreateReleaseFreezeRequest{App: "frz-auto", Kind: "after_rollback", AfterRollbackMinutes: 60, Reason: "settle after automatic rollbacks"}
	if rec := guardDo(t, e, http.MethodPost, "/v1/freezes", policy, nil); rec.Code != http.StatusCreated {
		t.Fatalf("policy = %d %s", rec.Code, rec.Body)
	}
	var status api.ReleaseFreezeStatus
	if rec := guardDo(t, e, http.MethodGet, "/v1/apps/frz-auto/freeze-status", nil, &status); rec.Code != http.StatusOK || status.Frozen {
		t.Fatalf("a policy alone froze the app: %d %+v", rec.Code, status)
	}
	f.telemetry.windows[f.release.ID] = canary.HealthWindow{Requests: 300, ServerErrors: 1, P95LatencyMS: 700}
	f.sweep(t)
	if got := f.read(t, f.release.ID); got.LastAutoRollbackReason != string(state.AutoRollbackReasonPostReleaseRegression) {
		t.Fatalf("release was not rolled back: %q", got.LastAutoRollbackReason)
	}
	if rec := guardDo(t, e, http.MethodGet, "/v1/apps/frz-auto/freeze-status", nil, &status); rec.Code != http.StatusOK || !status.Frozen {
		t.Fatalf("status after the rollback = %d %+v", rec.Code, status)
	}
	auto := status.Active[0]
	if auto.Source != "automatic_rollback" || auto.SourceDeploymentID != f.release.ID || auto.EndsAt == "" {
		t.Fatalf("automatic freeze = %+v", auto)
	}
}

// gateEvidence stands in for Postgres release gate evidence.
type gateEvidence struct {
	groups map[string][]state.ReleaseGateErrorGroup
}

func (g gateEvidence) ReleaseGateSLO(context.Context, string, string) (state.ReleaseGateSLOTarget, error) {
	return state.ReleaseGateSLOTarget{}, state.ErrNotFound
}
func (g gateEvidence) ReleaseGateDeploymentSLI(context.Context, string, string, time.Time, time.Time, int) (state.ReleaseGateSLISample, error) {
	return state.ReleaseGateSLISample{}, nil
}
func (g gateEvidence) ReleaseGateNewErrorGroups(_ context.Context, _, deploymentID string, _ int) ([]state.ReleaseGateErrorGroup, error) {
	return g.groups[deploymentID], nil
}
func (g gateEvidence) ReleaseGateCustomMetricWindow(context.Context, string, string, time.Time, time.Time) (int64, float64, error) {
	return 0, 0, nil
}

func TestReleaseGatesRoundTripAndValidate(t *testing.T) {
	f := newPostReleaseFixture(t, "gate-crud", true, time.Hour)
	e := f.e
	if rec := guardDo(t, e, http.MethodPost, "/v1/apps/gate-crud/release-gates", api.CreateReleaseGateRequest{Name: "checkout", Kind: "slo", SLO: "missing"}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("gate on a missing SLO = %d %s, want 400", rec.Code, rec.Body)
	} else if !strings.Contains(rec.Body.String(), "gregale slos list --app gate-crud") {
		t.Fatalf("missing-SLO hint names the wrong command: %s", rec.Body)
	}
	var gate api.ReleaseGate
	if rec := guardDo(t, e, http.MethodPost, "/v1/apps/gate-crud/release-gates", api.CreateReleaseGateRequest{Name: "no-new-errors", Kind: "new_errors"}, &gate); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if gate.MaxNewGroups == nil || *gate.MaxNewGroups != 0 || gate.MinEvents != 3 || gate.MinSamples != 20 {
		t.Fatalf("defaults = %+v", gate)
	}
	if rec := guardDo(t, e, http.MethodPost, "/v1/apps/gate-crud/release-gates", api.CreateReleaseGateRequest{Name: "no-new-errors", Kind: "new_errors"}, nil); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d, want 409", rec.Code)
	}
	var list api.ListReleaseGatesResponse
	if rec := guardDo(t, e, http.MethodGet, "/v1/apps/gate-crud/release-gates", nil, &list); rec.Code != http.StatusOK || len(list.Gates) != 1 {
		t.Fatalf("list = %d %+v", rec.Code, list)
	}
	if rec := guardDo(t, e, http.MethodDelete, "/v1/apps/gate-crud/release-gates/no-new-errors", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	if rec := guardDo(t, e, http.MethodDelete, "/v1/apps/gate-crud/release-gates/no-new-errors", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", rec.Code)
	}
}

func TestPostReleaseWatchRollsBackAReleaseThatFailsAGate(t *testing.T) {
	f := newPostReleaseFixture(t, "gate-watch", true, time.Minute)
	e := f.e
	if rec := guardDo(t, e, http.MethodPost, "/v1/apps/gate-watch/release-gates", api.CreateReleaseGateRequest{Name: "no-new-errors", Kind: "new_errors"}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create gate = %d %s", rec.Code, rec.Body)
	}
	evidence := gateEvidence{groups: map[string][]state.ReleaseGateErrorGroup{}}
	(*e.S()).SetReleaseGateEvidence(evidence)
	f.sweep(t)
	if got := f.read(t, f.release.ID); got.Status != state.DeployLive || got.LastAutoRollbackReason != "" {
		t.Fatalf("release without new errors = %s %q, want it left live", got.Status, got.LastAutoRollbackReason)
	}

	evidence.groups[f.release.ID] = []state.ReleaseGateErrorGroup{{ErrorClass: "null_pointer", Events: 9}}
	f.sweep(t)
	got := f.read(t, f.release.ID)
	if got.LastAutoRollbackReason != string(state.AutoRollbackReasonPostReleaseRegression) {
		t.Fatalf("reason = %q, want a post-release rollback", got.LastAutoRollbackReason)
	}
	var decision api.RolloutDecision
	if err := json.Unmarshal(got.RolloutDecision, &decision); err != nil {
		t.Fatalf("decode decision %s: %v", got.RolloutDecision, err)
	}
	if decision.Rule != api.RolloutRuleAbortPostReleaseGate || len(decision.Gates) != 1 || decision.Gates[0].Passed ||
		len(decision.Gates[0].ErrorClasses) != 1 || decision.Gates[0].ErrorClasses[0] != "null_pointer" {
		t.Fatalf("decision = %+v", decision)
	}
}
