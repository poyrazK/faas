// adr: 20261112080601
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agentundo"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A person undoes what their agent changed: the plan lists each change
// newest first, with what undo will do; applying the reviewed plan restores
// the app's settings and env vars. A change someone made again since is
// left unless forced; a secret change cannot be undone; a change is undone
// once; the agent cannot undo.
func TestHandlers_AgentUndo(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	defer apidserver.WithTestRecipient(t)()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "undo-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "undo-svc", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60}); err != nil {
		t.Fatal(err)
	}
	mint := func(label string, scopes ...string) (string, state.APIKey) {
		t.Helper()
		plaintext, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		key, err := store.CreateAPIKey(ctx, acct.ID, hash, label, scopes)
		if err != nil {
			t.Fatalf("CreateAPIKey %s: %v", label, err)
		}
		return plaintext, key
	}
	agent, agentKey := mint("undo-agent", api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite, api.ScopeEnvWrite, api.ScopeSecretsWrite)
	if err := store.SetAPIKeyApprovalCategories(ctx, agentKey.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	owner, _ := mint("undo-admin", api.ScopeAdmin)
	call := func(key, method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		srv.ServeHTTP(rec, r)
		return rec
	}
	must := func(rec *httptest.ResponseRecorder, what string) {
		t.Helper()
		if rec.Code < 200 || rec.Code > 299 {
			t.Fatalf("%s = %d %s", what, rec.Code, rec.Body)
		}
	}
	undoPath := "/v1/agent-connections/" + agentKey.ID + "/undo"
	plan := func(body string) api.AgentUndoPlan {
		t.Helper()
		rec := call(owner, http.MethodPost, undoPath+"/plan", body)
		var out api.AgentUndoPlan
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("plan = %d %s", rec.Code, rec.Body)
		}
		return out
	}
	apply := func(req api.AgentUndoRequest) api.AgentUndoResult {
		t.Helper()
		body, _ := json.Marshal(req)
		rec := call(owner, http.MethodPost, undoPath, string(body))
		var out api.AgentUndoResult
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("apply = %d %s", rec.Code, rec.Body)
		}
		return out
	}
	app := func() state.App {
		t.Helper()
		a, err := store.AppBySlug(ctx, "undo-svc")
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	envValue := func(key string) string {
		t.Helper()
		rows, err := store.ListAppEnvInScope(ctx, acct.ID, app().ID, "default")
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Key == key {
				return row.Value
			}
		}
		return ""
	}
	must(call(owner, http.MethodPut, "/v1/agent-connections/"+agentKey.ID+"/budget", `{"unlimited":true}`), "unlimited budget")
	must(call(owner, http.MethodPut, "/v1/apps/undo-svc/env/MODE", `{"value":"blue"}`), "owner sets MODE")

	// The agent's session: two settings changes, an env var and a secret.
	must(call(agent, http.MethodPatch, "/v1/apps/undo-svc", `{"idle_timeout_s":120}`), "agent idle timeout")
	must(call(agent, http.MethodPatch, "/v1/apps/undo-svc", `{"idle_timeout_s":180,"max_concurrency":3}`), "agent second change")
	must(call(agent, http.MethodPut, "/v1/apps/undo-svc/env/MODE", `{"value":"green"}`), "agent sets MODE")
	must(call(agent, http.MethodPut, "/v1/apps/undo-svc/secrets/TOKEN", `{"value":"s3cret-value"}`), "agent sets a secret")
	if a := app(); a.IdleTimeoutS != 180 || a.MaxConcurrency != 3 || envValue("MODE") != "green" {
		t.Fatalf("after the agent: idle %d, concurrency %d, MODE %q", a.IdleTimeoutS, a.MaxConcurrency, envValue("MODE"))
	}

	// The agent cannot undo.
	if rec := call(agent, http.MethodPost, undoPath+"/plan", `{}`); rec.Code != http.StatusForbidden {
		t.Fatalf("agent plans its own undo = %d; want 403", rec.Code)
	}

	got := plan(`{}`)
	if len(got.Items) != 4 || got.Undoable != 3 || got.PlanDigest == "" {
		t.Fatalf("plan = %+v; want 4 changes, 3 undoable", got)
	}
	secret := got.Items[0]
	if secret.Status != api.AgentUndoNotUndoable || !strings.Contains(secret.Reason, "secret") || !strings.Contains(secret.Summary, "TOKEN") {
		t.Fatalf("newest change = %+v; want the secret, not undoable", secret)
	}
	if strings.Contains(string(mustMarshal(t, got)), "s3cret-value") {
		t.Fatal("the plan shows a secret's value")
	}
	for _, item := range got.Items[1:] {
		if item.Status != api.AgentUndoUndoable || item.ActivityID == "" {
			t.Fatalf("change %+v; want undoable with its activity id", item)
		}
	}

	// A stale digest is refused and changes nothing.
	if rec := call(owner, http.MethodPost, undoPath, `{"plan_digest":"nope"}`); rec.Code != http.StatusConflict {
		t.Fatalf("apply with a stale digest = %d %s; want 409", rec.Code, rec.Body)
	}

	// Undo only the env var change, by its activity entry.
	envEntry := got.Items[1].ActivityID
	one := plan(`{"entries":["` + envEntry + `"]}`)
	if len(one.Items) != 1 || one.Items[0].Kind != api.AgentUndoKindSettings || !strings.Contains(one.Items[0].Summary, "MODE") {
		t.Fatalf("plan of one entry = %+v", one)
	}
	res := apply(api.AgentUndoRequest{Entries: []string{envEntry}, PlanDigest: one.PlanDigest})
	if res.Undone != 1 || res.Failed != 0 || envValue("MODE") != "blue" {
		t.Fatalf("undo MODE = %+v, MODE %q; want blue again", res, envValue("MODE"))
	}

	// Someone changes max_concurrency again: the agent's newest settings
	// change is left, and so is the older one of the same app.
	must(call(owner, http.MethodPatch, "/v1/apps/undo-svc", `{"max_concurrency":4}`), "owner changes concurrency")
	again := plan(`{}`)
	if again.Items[1].Status != api.AgentUndoUndone || again.Items[2].Status != api.AgentUndoChangedSince {
		t.Fatalf("plan after the owner's change = %+v; want env undone, newest settings changed since", again.Items)
	}
	res = apply(api.AgentUndoRequest{PlanDigest: again.PlanDigest})
	if res.Undone != 0 || res.Items[2].Result != api.AgentUndoResultChangedSince || res.Items[3].Result != api.AgentUndoResultSkipped {
		t.Fatalf("apply without force = %+v; want nothing undone", res.Items)
	}
	if a := app(); a.IdleTimeoutS != 180 || a.MaxConcurrency != 4 {
		t.Fatalf("app after a refused undo: idle %d, concurrency %d", a.IdleTimeoutS, a.MaxConcurrency)
	}

	// Forced, both settings changes are undone, newest first.
	forced := plan(`{"force":true}`)
	if forced.Undoable != 2 {
		t.Fatalf("forced plan = %+v; want 2 to undo", forced)
	}
	res = apply(api.AgentUndoRequest{Force: true, PlanDigest: forced.PlanDigest})
	if res.Undone != 2 || res.Failed != 0 {
		t.Fatalf("forced apply = %+v", res.Items)
	}
	if a := app(); a.IdleTimeoutS != 60 || a.MaxConcurrency != 5 {
		t.Fatalf("app after undo: idle %d, concurrency %d; want 60 and 5", a.IdleTimeoutS, a.MaxConcurrency)
	}

	// Undone once: a later plan shows them undone and applies nothing.
	final := plan(`{}`)
	for _, item := range final.Items[1:] {
		if item.Status != api.AgentUndoUndone {
			t.Fatalf("after undo, %+v; want undone", item)
		}
	}
	if res := apply(api.AgentUndoRequest{PlanDigest: final.PlanDigest}); res.Undone != 0 {
		t.Fatalf("second undo = %+v; want nothing", res)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Undo restores what a reviewed scaling change replaced, against the
// scaling revision it finds, and cancels a prewarm the agent asked for.
func TestHandlers_AgentUndoScalingAndPrewarm(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "undo-scale@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "scale-svc", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	mint := func(label string, scopes ...string) (string, state.APIKey) {
		t.Helper()
		plaintext, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		key, err := store.CreateAPIKey(ctx, acct.ID, hash, label, scopes)
		if err != nil {
			t.Fatal(err)
		}
		return plaintext, key
	}
	agent, agentKey := mint("scale-agent", api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite)
	if err := store.SetAPIKeyApprovalCategories(ctx, agentKey.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	owner, _ := mint("scale-admin", api.ScopeAdmin)
	call := func(key, method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		srv.ServeHTTP(rec, r)
		return rec
	}
	if rec := call(owner, http.MethodPut, "/v1/agent-connections/"+agentKey.ID+"/budget", `{"unlimited":true}`); rec.Code != http.StatusOK {
		t.Fatalf("unlimited budget = %d %s", rec.Code, rec.Body)
	}
	var snapshot api.AppScalingSnapshot
	if rec := call(agent, http.MethodGet, "/v1/apps/scale-svc/scaling", ""); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &snapshot) != nil {
		t.Fatalf("GET scaling = %d %s", rec.Code, rec.Body)
	}
	scale := `{"expected_app_id":"` + created.ID + `","request_id":"` + newUUID(t) + `","expected_revision":` + itoa(snapshot.Revision) + `,"max_concurrency":2}`
	if rec := call(agent, http.MethodPost, "/v1/apps/scale-svc/scaling", scale); rec.Code != http.StatusOK {
		t.Fatalf("agent scaling = %d %s", rec.Code, rec.Body)
	}
	wake := time.Now().Add(time.Hour).UTC()
	prewarmBody := `{"count":1,"wake_at":"` + wake.Format(time.RFC3339) + `","expires_at":"` + wake.Add(30*time.Minute).Format(time.RFC3339) + `"}`
	if rec := call(agent, http.MethodPost, "/v1/apps/scale-svc/prewarm", prewarmBody); rec.Code != http.StatusAccepted {
		t.Fatalf("agent prewarm = %d %s", rec.Code, rec.Body)
	}
	undoPath := "/v1/agent-connections/" + agentKey.ID + "/undo"
	var plan api.AgentUndoPlan
	if rec := call(owner, http.MethodPost, undoPath+"/plan", `{}`); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &plan) != nil {
		t.Fatalf("plan = %d %s", rec.Code, rec.Body)
	}
	const wantUndoable = 2
	if len(plan.Items) != 2 || plan.Undoable != wantUndoable || !strings.HasPrefix(plan.Items[0].Summary, "Prewarmed") {
		t.Fatalf("plan = %+v; want the prewarm and the scaling change", plan)
	}
	body, _ := json.Marshal(api.AgentUndoRequest{PlanDigest: plan.PlanDigest})
	var res api.AgentUndoResult
	if rec := call(owner, http.MethodPost, undoPath, string(body)); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &res) != nil {
		t.Fatalf("apply = %d %s", rec.Code, rec.Body)
	}
	if res.Undone != wantUndoable || res.Failed != 0 {
		t.Fatalf("apply = %+v", res.Items)
	}
	if app, err := store.AppBySlug(ctx, "scale-svc"); err != nil || app.MaxConcurrency != 5 {
		t.Fatalf("max_concurrency after undo = %d, %v; want 5", app.MaxConcurrency, err)
	}
	var prewarms []api.PrewarmIntentResponse
	rec := call(owner, http.MethodGet, "/v1/apps/scale-svc/prewarms", "")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &prewarms) != nil || len(prewarms) != 1 || prewarms[0].Status != "cancelled" {
		t.Fatalf("prewarms after undo = %d %s; want the agent's cancelled", rec.Code, rec.Body)
	}
}

// Undoing an agent's rollback rolls the app back to the deployment that
// served before it, once the agent's has started serving; while it starts,
// undo leaves it and says why.
func TestHandlers_AgentUndoRollback(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	defer apidserver.WithTestRollbackArtifacts(t, srv)()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "undo-deploy@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "deploy-svc", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	seed := func(digit string, at time.Time) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:" + strings.Repeat(digit, 64), Kind: state.DeploymentKindImage, Status: state.DeployBuilding, CreatedAt: at})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, d.ID, "/srv/fc/apps/deploy-svc/"+d.ID+".ext4", "apps/deploy-svc/"+d.ID+".ext4", 1); err != nil {
			t.Fatal(err)
		}
		return d
	}
	now := time.Now().UTC()
	older, current := seed("a", now.Add(-time.Hour)), seed("b", now)
	for _, step := range []func() error{
		func() error { return store.MarkDeploymentLive(ctx, older.ID) },
		func() error { return store.MarkDeploymentLive(ctx, current.ID) },
		func() error { return store.MarkDeploymentSuperseded(ctx, older.ID) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	mint := func(label string, scopes ...string) (string, state.APIKey) {
		t.Helper()
		plaintext, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		key, err := store.CreateAPIKey(ctx, acct.ID, hash, label, scopes)
		if err != nil {
			t.Fatal(err)
		}
		return plaintext, key
	}
	agent, agentKey := mint("deploy-agent", api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite)
	if err := store.SetAPIKeyApprovalCategories(ctx, agentKey.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	owner, _ := mint("deploy-admin", api.ScopeAdmin)
	call := func(key, method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		srv.ServeHTTP(rec, r)
		return rec
	}
	if rec := call(agent, http.MethodPost, "/v1/apps/deploy-svc/rollback", `{"target_deployment_id":"`+older.ID+`"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("agent rollback = %d %s", rec.Code, rec.Body)
	}
	undoPath := "/v1/agent-connections/" + agentKey.ID + "/undo"
	plan := func() api.AgentUndoPlan {
		t.Helper()
		var out api.AgentUndoPlan
		if rec := call(owner, http.MethodPost, undoPath+"/plan", `{}`); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("plan = %d %s", rec.Code, rec.Body)
		}
		return out
	}
	apply := func(digest string) api.AgentUndoResult {
		t.Helper()
		var out api.AgentUndoResult
		body, _ := json.Marshal(api.AgentUndoRequest{PlanDigest: digest})
		if rec := call(owner, http.MethodPost, undoPath, string(body)); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("apply = %d %s", rec.Code, rec.Body)
		}
		return out
	}

	// While the agent's rollback starts, undo leaves it.
	starting := plan()
	if len(starting.Items) != 1 || starting.Items[0].Kind != api.AgentUndoKindDeploy || !strings.Contains(starting.Items[0].Reason, "still starting") {
		t.Fatalf("plan while starting = %+v", starting.Items)
	}
	if res := apply(starting.PlanDigest); res.Undone != 0 || res.Failed != 1 || !strings.Contains(res.Items[0].Error, "still starting") {
		t.Fatalf("apply while starting = %+v", res.Items)
	}

	// Once it serves, undo rolls back to the deployment before it.
	for _, step := range []func() error{
		func() error { return store.MarkDeploymentLive(ctx, older.ID) },
		func() error { return store.MarkDeploymentSuperseded(ctx, current.ID) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	served := plan()
	if res := apply(served.PlanDigest); res.Undone != 1 || res.Failed != 0 {
		t.Fatalf("apply once served = %+v", res.Items)
	}
	if d, err := store.DeploymentByID(ctx, current.ID); err != nil || d.Status == state.DeploySuperseded {
		t.Fatalf("the earlier deployment after undo = %+v, %v; want it starting again", d.Status, err)
	}
}

// Undoing what an agent created deletes it through its own delete route: the
// app is parked (restorable), the cron is gone; a thing already deleted
// counts as undone.
func TestHandlers_AgentUndoCreated(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "undo-create@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	host, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "cron-host", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	mint := func(label string, scopes ...string) (string, state.APIKey) {
		t.Helper()
		plaintext, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		key, err := store.CreateAPIKey(ctx, acct.ID, hash, label, scopes)
		if err != nil {
			t.Fatal(err)
		}
		return plaintext, key
	}
	agent, agentKey := mint("create-agent", api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite)
	if err := store.SetAPIKeyApprovalCategories(ctx, agentKey.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	owner, _ := mint("create-admin", api.ScopeAdmin)
	call := func(key, method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		srv.ServeHTTP(rec, r)
		return rec
	}
	created := call(agent, http.MethodPost, "/v1/apps", `{"slug":"agent-made"}`)
	if created.Code < 200 || created.Code > 299 {
		t.Fatalf("agent creates an app = %d %s", created.Code, created.Body)
	}
	cron := call(agent, http.MethodPost, "/v1/crons", `{"app_id":"`+host.ID+`","schedule":"0 3 * * *","path":"/jobs/sync"}`)
	if cron.Code < 200 || cron.Code > 299 {
		t.Fatalf("agent creates a cron = %d %s", cron.Code, cron.Body)
	}
	var cronOut struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(cron.Body.Bytes(), &cronOut)

	undoPath := "/v1/agent-connections/" + agentKey.ID + "/undo"
	var plan api.AgentUndoPlan
	if rec := call(owner, http.MethodPost, undoPath+"/plan", `{}`); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &plan) != nil {
		t.Fatalf("plan = %d %s", rec.Code, rec.Body)
	}
	if len(plan.Items) != 2 || plan.Undoable != 2 || plan.Items[0].Kind != api.AgentUndoKindCreate ||
		!strings.Contains(plan.Items[1].Summary, "app agent-made") {
		t.Fatalf("plan = %+v; want the cron and the app, both undoable", plan.Items)
	}
	body, _ := json.Marshal(api.AgentUndoRequest{PlanDigest: plan.PlanDigest})
	var res api.AgentUndoResult
	if rec := call(owner, http.MethodPost, undoPath, string(body)); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &res) != nil {
		t.Fatalf("apply = %d %s", rec.Code, rec.Body)
	}
	if res.Undone != 2 || res.Failed != 0 {
		t.Fatalf("apply = %+v", res.Items)
	}
	if _, err := store.AppBySlug(ctx, "agent-made"); err == nil {
		t.Fatal("the agent's app is still active after undo")
	}
	if rec := call(owner, http.MethodGet, "/v1/crons/"+cronOut.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("the agent's cron after undo = %d; want 404", rec.Code)
	}
}

// Undoing what an agent deleted restores an app within its window and
// creates a cron again from what it was.
func TestHandlers_AgentUndoDeleted(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "undo-delete@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	host, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "keep-host", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "doomed", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 5}); err != nil {
		t.Fatal(err)
	}
	mint := func(label string, scopes ...string) (string, state.APIKey) {
		t.Helper()
		plaintext, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		key, err := store.CreateAPIKey(ctx, acct.ID, hash, label, scopes)
		if err != nil {
			t.Fatal(err)
		}
		return plaintext, key
	}
	agent, agentKey := mint("delete-agent", api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite)
	if err := store.SetAPIKeyApprovalCategories(ctx, agentKey.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	owner, _ := mint("delete-admin", api.ScopeAdmin)
	call := func(key, method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		srv.ServeHTTP(rec, r)
		return rec
	}
	cron := call(owner, http.MethodPost, "/v1/crons", `{"app_id":"`+host.ID+`","schedule":"15 4 * * *","path":"/jobs/nightly"}`)
	var cronOut api.CronResponse
	if cron.Code < 200 || cron.Code > 299 || json.Unmarshal(cron.Body.Bytes(), &cronOut) != nil {
		t.Fatalf("owner creates a cron = %d %s", cron.Code, cron.Body)
	}
	if rec := call(agent, http.MethodDelete, "/v1/crons/"+cronOut.ID, ""); rec.Code < 200 || rec.Code > 299 {
		t.Fatalf("agent deletes the cron = %d %s", rec.Code, rec.Body)
	}
	if rec := call(agent, http.MethodDelete, "/v1/apps/doomed", ""); rec.Code < 200 || rec.Code > 299 {
		t.Fatalf("agent deletes the app = %d %s", rec.Code, rec.Body)
	}

	undoPath := "/v1/agent-connections/" + agentKey.ID + "/undo"
	var plan api.AgentUndoPlan
	if rec := call(owner, http.MethodPost, undoPath+"/plan", `{}`); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &plan) != nil {
		t.Fatalf("plan = %d %s", rec.Code, rec.Body)
	}
	if len(plan.Items) != 2 || plan.Undoable != 2 || plan.Items[0].Kind != api.AgentUndoKindDelete {
		t.Fatalf("plan = %+v; want the app and the cron, both undoable", plan.Items)
	}
	body, _ := json.Marshal(api.AgentUndoRequest{PlanDigest: plan.PlanDigest})
	var res api.AgentUndoResult
	if rec := call(owner, http.MethodPost, undoPath, string(body)); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &res) != nil {
		t.Fatalf("apply = %d %s", rec.Code, rec.Body)
	}
	if res.Undone != 2 || res.Failed != 0 {
		t.Fatalf("apply = %+v", res.Items)
	}
	if app, err := store.AppBySlug(ctx, "doomed"); err != nil || app.Status != state.AppActive {
		t.Fatalf("the deleted app after undo = %+v, %v; want restored", app.Status, err)
	}
	var crons []api.CronResponse
	rec := call(owner, http.MethodGet, "/v1/crons?app_id="+host.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list crons = %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &crons); err != nil {
		var page struct {
			Items []api.CronResponse `json:"items"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &page)
		crons = page.Items
	}
	found := false
	for _, c := range crons {
		if c.Schedule == "15 4 * * *" && c.Path == "/jobs/nightly" {
			found = true
		}
	}
	if !found {
		t.Fatalf("crons after undo = %s; want the nightly cron again", rec.Body)
	}
}

// An agent undoes its own changes only with a person's approval, whatever
// approval categories it chose: the apply waits (category undo), and once
// approved the same request applies the plan once.
func TestHandlers_AgentSelfUndo(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	const email = "self-undo@example.com"
	acct, err := store.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "self-svc", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60}); err != nil {
		t.Fatal(err)
	}
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(ctx, acct.ID, hash, "self-agent", []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAPIKeyApprovalCategories(ctx, key.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+plaintext)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		srv.ServeHTTP(rec, r)
		return rec
	}
	if rec := call(http.MethodPatch, "/v1/apps/self-svc", `{"idle_timeout_s":300}`); rec.Code != http.StatusOK {
		t.Fatalf("agent change = %d %s", rec.Code, rec.Body)
	}
	var plan api.AgentUndoPlan
	if rec := call(http.MethodPost, "/v1/agent-connections/self/undo/plan", `{}`); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &plan) != nil {
		t.Fatalf("self plan = %d %s", rec.Code, rec.Body)
	}
	if plan.Undoable != 1 {
		t.Fatalf("self plan = %+v", plan.Items)
	}
	apply, _ := json.Marshal(api.AgentUndoRequest{PlanDigest: plan.PlanDigest})
	rec := call(http.MethodPost, "/v1/agent-connections/self/undo", string(apply))
	var problem api.Problem
	if rec.Code != http.StatusPreconditionRequired || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.AgentApproval == nil || problem.AgentApproval.Category != api.AgentApprovalUndo {
		t.Fatalf("self apply = %d %s; want 428 undo", rec.Code, rec.Body)
	}
	if app, _ := store.AppBySlug(ctx, "self-svc"); app.IdleTimeoutS != 300 {
		t.Fatalf("idle timeout before approval = %d; want unchanged 300", app.IdleTimeoutS)
	}
	page, cookie, form := apidserver.RenderAgentApprovalForTestAs(t, srv, problem.AgentApproval.ID, email)
	if !strings.Contains(page.Body.String(), "Undo the agent") {
		t.Fatalf("review page does not name the undo:\n%s", page.Body)
	}
	if approved := apidserver.DecideAgentApprovalForTest(t, srv, problem.AgentApproval.ID, cookie, form, "approve"); approved.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", approved.Code, approved.Body)
	}
	var res api.AgentUndoResult
	if rec := call(http.MethodPost, "/v1/agent-connections/self/undo", string(apply)); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &res) != nil || res.Undone != 1 {
		t.Fatalf("approved self apply = %d %s", rec.Code, rec.Body)
	}
	if app, _ := store.AppBySlug(ctx, "self-svc"); app.IdleTimeoutS != 60 {
		t.Fatalf("idle timeout after undo = %d; want 60", app.IdleTimeoutS)
	}
	// The agent cannot use the person's routes for its own key.
	if rec := call(http.MethodPost, "/v1/agent-connections/"+key.ID+"/undo/plan", `{}`); rec.Code != http.StatusForbidden {
		t.Fatalf("agent on the person's route = %d; want 403", rec.Code)
	}
}

func newUUID(t *testing.T) string {
	t.Helper()
	return uuid.NewString()
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// Each app setting undo restores reads back from GET /v1/apps/{slug} in the
// form PATCH takes: writing back what was read (or the empty value, when the
// read leaves the field out) is accepted and changes nothing.
func TestAgentUndoAppSettingsRoundTrip(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "undo-fields@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "fields-svc", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60}); err != nil {
		t.Fatal(err)
	}
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "fields-admin", []string{api.ScopeAdmin}); err != nil {
		t.Fatal(err)
	}
	call := func(method, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/v1/apps/fields-svc", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+plaintext)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		srv.ServeHTTP(rec, r)
		return rec
	}
	read := func() map[string]json.RawMessage {
		t.Helper()
		rec := call(http.MethodGet, "")
		var m map[string]json.RawMessage
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &m) != nil {
			t.Fatalf("GET app = %d %s", rec.Code, rec.Body)
		}
		return m
	}
	for _, field := range agentundo.UndoableAppSettings() {
		before := read()
		value, ok := before[field]
		if !ok {
			value = agentundo.ZeroAppSetting(field)
		}
		body := `{"` + field + `":` + string(value) + `}`
		if rec := call(http.MethodPatch, body); rec.Code != http.StatusOK {
			t.Errorf("%s: PATCH %s = %d %s", field, body, rec.Code, rec.Body)
			continue
		}
		after := read()
		if string(mustMarshal(t, before[field])) != string(mustMarshal(t, after[field])) {
			t.Errorf("%s: read %s, wrote it back, now reads %s", field, before[field], after[field])
		}
	}
}

// Every route undo captures or runs through is one apid serves: a rename
// would otherwise silently turn every capture into "not undoable".
func TestAgentUndoRoutesAreServed(t *testing.T) {
	srv, _ := apidserver.NewCliAuthTestServer(t)
	patterns := append(agentundo.Routes(), apidserver.AgentUndoHandlerPatternsForTest(t, srv)...)
	for _, pattern := range patterns {
		method, path, _ := strings.Cut(pattern, " ")
		path = regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(path, "x")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader("{}")))
		if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s: unauthenticated %s %s = %d; want the route served (401)", pattern, method, path, rec.Code)
		}
	}
}
