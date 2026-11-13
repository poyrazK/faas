// adr: 20261111114059
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

// A budget preview prices an agent's change as the real request would and
// writes nothing: the app keeps its settings, no prewarm is made, no
// approval is opened and no activity, undo record or spend is recorded.
// It says whether a person would be asked and why. The header is refused on
// routes that do not price before writing, and a person's credential is
// answered as never charged.
func TestAgentBudgetPreviewWritesNothing(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "budget-preview@example.com", api.PlanPro)
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
	agent, key := mint("preview-agent", api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite)
	if err := store.SetAPIKeyApprovalCategories(ctx, key.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	setBudget := func(cents int64) {
		t.Helper()
		if err := store.SetAgentKeyBudget(ctx, key.ID, state.AgentKeyBudget{Set: true, MonthlyCents: cents}, acct.ID); err != nil {
			t.Fatal(err)
		}
	}
	setBudget(1000)
	owner, _ := mint("preview-admin", api.ScopeAdmin)
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "preview-svc", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	call := func(token, method, path, body string, preview bool) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		if preview {
			r.Header.Set(api.AgentPreviewHeader, api.AgentPreviewBudget)
		}
		srv.ServeHTTP(rec, r)
		return rec
	}
	previewOf := func(t *testing.T, rec *httptest.ResponseRecorder) api.AgentBudgetPreview {
		t.Helper()
		var out api.AgentBudgetPreview
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !out.Preview {
			t.Fatalf("preview = %d %s", rec.Code, rec.Body)
		}
		return out
	}
	unchanged := func(t *testing.T) {
		t.Helper()
		got, err := store.AppBySlug(ctx, "preview-svc")
		if err != nil || got.MinInstances != 0 {
			t.Fatalf("app after a preview = min %d, %v; want unchanged", got.MinInstances, err)
		}
		if intents, err := store.ListPrewarmIntentsForApp(ctx, app.ID, 10); err != nil || len(intents) != 0 {
			t.Fatalf("prewarms after a preview = %d, %v", len(intents), err)
		}
		if approvals, err := store.ListAgentApprovalsForKey(ctx, key.ID, 10); err != nil || len(approvals) != 0 {
			t.Fatalf("approvals after a preview = %+v, %v", approvals, err)
		}
		since := time.Now().Add(-time.Hour)
		if rows, err := store.ListAgentActivity(ctx, key.ID, since, state.AgentActivityCursor{}, 10); err != nil || len(rows) != 0 {
			t.Fatalf("activity after a preview = %+v, %v", rows, err)
		}
		if recs, err := store.AgentUndoRecords(ctx, key.ID, since, 10); err != nil || len(recs) != 0 {
			t.Fatalf("undo records after a preview = %+v, %v", recs, err)
		}
		if spent, err := store.AgentBudgetSpent(ctx, key.ID, time.Now()); err != nil || spent != 0 {
			t.Fatalf("spend after a preview = %d, %v", spent, err)
		}
	}
	twoWarm := api.CentsCeil(api.AgentStandingMillicents(2, 512))

	t.Run("over budget, a person would be asked", func(t *testing.T) {
		out := previewOf(t, call(agent, http.MethodPatch, "/v1/apps/preview-svc", `{"min_instances":2}`, true))
		if !out.Charged || out.AddedCents != twoWarm || out.BudgetCents == nil || *out.BudgetCents != 1000 ||
			out.RemainingCents == nil || *out.RemainingCents != 1000 || !out.NeedsApproval || out.ApprovalCategory != api.AgentApprovalBudget {
			t.Fatalf("preview = %+v; want %d cents past a 1000-cent budget", out, twoWarm)
		}
		unchanged(t)
	})
	t.Run("scaling and prewarm preview the same way", func(t *testing.T) {
		var snap api.AppScalingSnapshot
		if rec := call(agent, http.MethodGet, "/v1/apps/preview-svc/scaling", "", false); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &snap) != nil {
			t.Fatalf("scaling snapshot = %d %s", rec.Code, rec.Body)
		}
		body, _ := json.Marshal(api.ApplyAppScalingRequest{ExpectedAppID: snap.AppID, RequestID: newUUID(t), ExpectedRevision: &snap.Revision, MinInstances: ptrInt(2)})
		if out := previewOf(t, call(agent, http.MethodPost, "/v1/apps/preview-svc/scaling", string(body), true)); out.AddedCents != twoWarm || !out.NeedsApproval {
			t.Fatalf("scaling preview = %+v", out)
		}
		wake := time.Now().Add(time.Hour).UTC()
		prewarm, _ := json.Marshal(api.PrewarmRequest{Count: 2, WakeAt: wake, ExpiresAt: wake.Add(time.Hour)})
		if out := previewOf(t, call(agent, http.MethodPost, "/v1/apps/preview-svc/prewarm", string(prewarm), true)); !out.Charged || out.AddedCents <= 0 {
			t.Fatalf("prewarm preview = %+v", out)
		}
		unchanged(t)
	})
	t.Run("within budget, nobody is asked", func(t *testing.T) {
		setBudget(100_000)
		out := previewOf(t, call(agent, http.MethodPatch, "/v1/apps/preview-svc", `{"min_instances":2}`, true))
		if out.NeedsApproval || out.AddedCents != twoWarm || *out.RemainingCents != 100_000 {
			t.Fatalf("preview = %+v", out)
		}
		unchanged(t)
	})
	t.Run("another approval the real request waits for is named", func(t *testing.T) {
		careful, carefulKey := mint("preview-careful-agent", api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite)
		if err := store.SetAPIKeyApprovalCategories(ctx, carefulKey.ID, []string{api.AgentApprovalProduction}); err != nil {
			t.Fatal(err)
		}
		out := previewOf(t, call(careful, http.MethodPatch, "/v1/apps/preview-svc", `{"min_instances":2}`, true))
		if !out.NeedsApproval || out.ApprovalCategory != api.AgentApprovalProduction {
			t.Fatalf("preview = %+v; want production", out)
		}
		if approvals, err := store.ListAgentApprovalsForKey(ctx, carefulKey.ID, 10); err != nil || len(approvals) != 0 {
			t.Fatalf("a preview opened approvals: %+v, %v", approvals, err)
		}
		unchanged(t)
	})
	t.Run("a change that adds nothing is previewed as free", func(t *testing.T) {
		out := previewOf(t, call(agent, http.MethodPatch, "/v1/apps/preview-svc", `{"max_concurrency":4}`, true))
		if !out.Charged || out.AddedCents != 0 || out.NeedsApproval {
			t.Fatalf("preview = %+v", out)
		}
		if got, _ := store.AppBySlug(ctx, "preview-svc"); got.MaxConcurrency != 5 {
			t.Fatalf("max_concurrency after a preview = %d", got.MaxConcurrency)
		}
		unchanged(t)
	})
	t.Run("a person's credential is never charged", func(t *testing.T) {
		if out := previewOf(t, call(owner, http.MethodPatch, "/v1/apps/preview-svc", `{"min_instances":2}`, true)); out.Charged || out.AddedCents != 0 {
			t.Fatalf("preview = %+v", out)
		}
		unchanged(t)
	})
	t.Run("routes that cannot be previewed refuse the header", func(t *testing.T) {
		// A cron is not priced, so it cannot be previewed.
		if rec := call(agent, http.MethodPost, "/v1/crons", `{"app":"preview-svc","schedule":"* * * * *","path":"/tick"}`, true); rec.Code != http.StatusBadRequest {
			t.Fatalf("cron preview = %d %s; want 400", rec.Code, rec.Body)
		}
		if crons, err := store.ListCronsForApp(ctx, app.ID); err != nil || len(crons) != 0 {
			t.Fatalf("crons after a refused preview = %d, %v", len(crons), err)
		}
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPatch, "/v1/apps/preview-svc", strings.NewReader(`{"min_instances":2}`))
		r.Header.Set("Authorization", "Bearer "+agent)
		r.Header.Set(api.AgentPreviewHeader, "everything")
		srv.ServeHTTP(rec, r)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("unknown preview = %d %s", rec.Code, rec.Body)
		}
		unchanged(t)
	})
}
