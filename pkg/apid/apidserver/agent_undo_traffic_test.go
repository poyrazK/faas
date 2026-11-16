// adr: 20261112080601
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// undoFixture is an account with an agent key that asks about nothing and
// an admin key that plans and applies the agent's undo.
type undoFixture struct {
	t            *testing.T
	srv          http.Handler
	store        *state.MemStore
	acct         state.Account
	agent, owner string
	undoPath     string
}

func newUndoFixture(t *testing.T, srv http.Handler, store *state.MemStore, email string, scopes ...string) *undoFixture {
	t.Helper()
	acct, err := store.CreateAccount(t.Context(), email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f := &undoFixture{t: t, srv: srv, store: store, acct: acct}
	mint := func(label string, scopes ...string) (string, state.APIKey) {
		plaintext, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		key, err := store.CreateAPIKey(t.Context(), acct.ID, hash, label, scopes)
		if err != nil {
			t.Fatal(err)
		}
		return plaintext, key
	}
	var agentKey state.APIKey
	f.agent, agentKey = mint("undo-traffic-agent", append([]string{api.ScopeAgent, api.ScopeAppsRead}, scopes...)...)
	if err := store.SetAPIKeyApprovalCategories(t.Context(), agentKey.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	f.owner, _ = mint("undo-traffic-admin", api.ScopeAdmin)
	f.undoPath = "/v1/agent-connections/" + agentKey.ID + "/undo"
	return f
}

func (f *undoFixture) call(key, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+key)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	f.srv.ServeHTTP(rec, r)
	return rec
}

func (f *undoFixture) plan() api.AgentUndoPlan { return f.planWith(false) }

// planWith plans the undo; a forced apply needs a forced plan.
func (f *undoFixture) planWith(force bool) api.AgentUndoPlan {
	f.t.Helper()
	var out api.AgentUndoPlan
	body, _ := json.Marshal(api.AgentUndoRequest{Force: force})
	if rec := f.call(f.owner, http.MethodPost, f.undoPath+"/plan", string(body)); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		f.t.Fatalf("plan = %d %s", rec.Code, rec.Body)
	}
	return out
}

func (f *undoFixture) apply(digest string, force bool) api.AgentUndoResult {
	f.t.Helper()
	var out api.AgentUndoResult
	body, _ := json.Marshal(api.AgentUndoRequest{PlanDigest: digest, Force: force})
	if rec := f.call(f.owner, http.MethodPost, f.undoPath, string(body)); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		f.t.Fatalf("apply = %d %s", rec.Code, rec.Body)
	}
	return out
}

// Undoing an agent's traffic change puts back the exact split the live
// deployments had before; a split changed again since is left unless forced.
func TestHandlers_AgentUndoTraffic(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	f := newUndoFixture(t, srv, store, "undo-traffic@example.com", api.ScopeDeployWrite)
	app, err := store.CreateApp(ctx, state.App{AccountID: f.acct.ID, Slug: "split-svc", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	// Two live deployments split 90/10, as a person left them.
	var ids []string
	for i, seed := range []struct {
		digit   string
		percent int
	}{{"a", 90}, {"b", 10}} {
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:" + strings.Repeat(seed.digit, 64), Kind: state.DeploymentKindImage,
			Status: state.DeployLive, TrafficPercent: seed.percent, CreatedAt: now.Add(time.Duration(i) * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}
	stable, canary := ids[0], ids[1]
	percent := func(id string) int {
		t.Helper()
		d, err := store.DeploymentByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return d.TrafficPercent
	}
	if percent(stable) != 90 || percent(canary) != 10 {
		t.Fatalf("seed split = %d/%d, want 90/10", percent(stable), percent(canary))
	}

	// The agent shifts traffic, then undo puts back 90/10.
	if rec := f.call(f.agent, http.MethodPatch, "/v1/deployments/"+canary+"/traffic", `{"traffic_percent":60}`); rec.Code != http.StatusOK {
		t.Fatalf("agent traffic change = %d %s", rec.Code, rec.Body)
	}
	if percent(stable) != 40 {
		t.Fatalf("split after the agent = %d/%d", percent(stable), percent(canary))
	}
	plan := f.plan()
	if len(plan.Items) != 1 || plan.Items[0].Kind != api.AgentUndoKindDeploy || plan.Items[0].Status != api.AgentUndoUndoable {
		t.Fatalf("plan = %+v", plan.Items)
	}
	if res := f.apply(plan.PlanDigest, false); res.Undone != 1 || res.Failed != 0 {
		t.Fatalf("apply = %+v", res.Items)
	}
	if percent(stable) != 90 || percent(canary) != 10 {
		t.Fatalf("split after undo = %d/%d, want 90/10", percent(stable), percent(canary))
	}

	// A person's later change wins unless the undo is forced.
	if rec := f.call(f.agent, http.MethodPatch, "/v1/deployments/"+canary+"/traffic", `{"traffic_percent":100}`); rec.Code != http.StatusOK {
		t.Fatalf("agent shift to 100 = %d %s", rec.Code, rec.Body)
	}
	if _, err := store.UpdateDeploymentTraffic(ctx, canary, 70); err != nil {
		t.Fatal(err)
	}
	changed := f.plan()
	// Newest first: the agent's second change, then its first, already undone.
	if len(changed.Items) != 2 || changed.Items[0].Status != api.AgentUndoChangedSince || changed.Items[1].Status != api.AgentUndoUndone {
		t.Fatalf("plan after a person's change = %+v", changed.Items)
	}
	if res := f.apply(changed.PlanDigest, false); res.Undone != 0 {
		t.Fatalf("unforced apply = %+v", res.Items)
	}
	if percent(canary) != 70 {
		t.Fatalf("the person's split was overwritten: canary %d", percent(canary))
	}
	if res := f.apply(f.planWith(true).PlanDigest, true); res.Undone != 1 {
		t.Fatalf("forced apply = %+v", res.Items)
	}
	if percent(stable) != 90 || percent(canary) != 10 {
		t.Fatalf("split after forced undo = %d/%d, want 90/10", percent(stable), percent(canary))
	}
}

// Undoing an agent's env var change in a named environment rolls that
// environment's configuration back.
func TestHandlers_AgentUndoEnvironmentVariables(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	f := newUndoFixture(t, srv, store, "undo-env@example.com", api.ScopeEnvWrite)
	project, err := store.CreateProject(ctx, state.Project{AccountID: f.acct.ID, Slug: "shop", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: f.acct.ID, ProjectID: project.ID, Slug: "shop-api", WorkloadName: "api", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	value := func() string {
		t.Helper()
		rows, err := store.ListAppEnvInScope(ctx, f.acct.ID, app.ID, "staging")
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Key == "MODE" {
				return row.Value
			}
		}
		return ""
	}
	if rec := f.call(f.owner, http.MethodPut, "/v1/apps/shop-api/env/MODE?scope=staging", `{"value":"blue"}`); rec.Code/100 != 2 {
		t.Fatalf("owner sets MODE = %d %s", rec.Code, rec.Body)
	}
	if rec := f.call(f.agent, http.MethodPut, "/v1/apps/shop-api/env/MODE?scope=staging", `{"value":"green"}`); rec.Code/100 != 2 {
		t.Fatalf("agent sets MODE = %d %s", rec.Code, rec.Body)
	}
	plan := f.plan()
	if len(plan.Items) != 1 || plan.Items[0].Status != api.AgentUndoUndoable {
		t.Fatalf("plan = %+v", plan.Items)
	}
	if res := f.apply(plan.PlanDigest, false); res.Undone != 1 || res.Failed != 0 {
		t.Fatalf("apply = %+v", res.Items)
	}
	if got := value(); got != "blue" {
		t.Fatalf("staging MODE after undo = %q, want blue", got)
	}
}
