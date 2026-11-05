package apidserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/previewconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261103180000: a PR preview runs unreviewed code and is torn down with
// its pull request, so it cannot own a bucket; its production app still can.
func TestPRPreviewRefusesObjectStorageBuckets(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t))
	e := apidserver.Setup(t, api.PlanHobby)
	if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, &apidserver.FakeObjectProvider{}, &apidserver.FakeObjectProvider{}, "external"))
	apidserver.CreateApp(t, e, "bucket-shop")
	apidserver.SeedPreviewAppForTest(t, e, "pr-3-bucket-shop", "bucket-shop", 3)

	r := e.Do(t, "POST", "/v1/apps/pr-3-bucket-shop/buckets", map[string]any{"name": "assets"}, nil)
	var problem api.Problem
	if r.Code != http.StatusConflict || json.Unmarshal(r.Body.Bytes(), &problem) != nil || problem.Code != api.CodePRPreviewResourceUnsupported {
		t.Fatalf("preview bucket = %d %s, want 409 %s", r.Code, r.Body.String(), api.CodePRPreviewResourceUnsupported)
	}
	if r := e.Do(t, "POST", "/v1/apps/bucket-shop/buckets", map[string]any{"name": "assets"}, nil); r.Code != http.StatusCreated {
		t.Fatalf("production bucket = %d %s", r.Code, r.Body.String())
	}
}

// ADR-20261103180000: binding a database to a PR preview would hand its
// credentials to unreviewed code.
func TestPRPreviewRefusesManagedPostgresBindings(t *testing.T) {
	env := apidserver.NewSourceRefTestServer(t, api.PlanPro, "bound-shop", 9005)
	_, _, databaseID := apidserver.ConfigureSourceRefManagedPostgres(t, env)
	srv := *env.Srv()
	ctx := context.Background()
	acct, err := (*env.Store()).AccountByID(ctx, *env.AcctID())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := (*env.Store()).CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "pr-5-bound-shop", Type: "stateless",
		RAMMB: 256, Status: state.AppActive, PreviewOfSlug: "bound-shop", PreviewPrNumber: 5, PreviewPrState: state.PreviewPrStateOpen})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(api.CreateManagedPostgresBindingRequest{AppID: preview.ID, Scope: "default", EnvironmentKey: "DATABASE_URL", Access: "read_write"})
	req := httptest.NewRequest(http.MethodPost, "/v1/postgres/databases/"+databaseID+"/bindings", bytes.NewReader(body))
	req.SetPathValue("id", databaseID)
	w := httptest.NewRecorder()
	srv.CreateManagedPostgresBinding(w, req, acct)
	var problem api.Problem
	if w.Code != http.StatusConflict || json.Unmarshal(w.Body.Bytes(), &problem) != nil || problem.Code != api.CodePRPreviewResourceUnsupported {
		t.Fatalf("preview binding = %d %s, want 409 %s", w.Code, w.Body, api.CodePRPreviewResourceUnsupported)
	}
}

// ADR-20261103180000: removing a value from the production app's pr-preview
// scope removes the copy its open previews hold, and keeps a value set on a
// preview itself.
func TestDeletingAPRPreviewValueRemovesPreviewCopies(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	apidserver.CreateApp(t, e, "values-shop")
	first := apidserver.SeedPreviewAppForTest(t, e, "pr-6-values-shop", "values-shop", 6)
	second := apidserver.SeedPreviewAppForTest(t, e, "pr-7-values-shop", "values-shop", 7)
	if r := e.Do(t, "PUT", "/v1/apps/values-shop/env/STRIPE_KEY?scope=pr-preview", api.PutAppEnvRequest{Value: "sk_test_1"}, nil); r.Code >= 300 {
		t.Fatalf("set pr-preview value = %d %s", r.Code, r.Body.String())
	}
	parent, err := (*e.Store()).AppBySlug(ctx, "values-shop")
	if err != nil {
		t.Fatal(err)
	}
	for _, preview := range []state.App{first, second} {
		if _, err := previewconfig.Sync(ctx, *e.Store(), parent, preview); err != nil {
			t.Fatalf("sync %s: %v", preview.Slug, err)
		}
	}
	if err := (*e.Store()).UpsertAppEnvInScope(ctx, second.AccountID, second.ID, api.DefaultEnvScope, "STRIPE_KEY", "sk_test_mine"); err != nil {
		t.Fatal(err)
	}

	if r := e.Do(t, "DELETE", "/v1/apps/values-shop/env/STRIPE_KEY?scope=pr-preview", nil, nil); r.Code != http.StatusNoContent {
		t.Fatalf("delete pr-preview value = %d %s", r.Code, r.Body.String())
	}
	value := func(app state.App) (string, bool) {
		rows, err := (*e.Store()).ListAppEnvInScope(ctx, app.AccountID, app.ID, api.DefaultEnvScope)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.Key == "STRIPE_KEY" {
				return row.Value, true
			}
		}
		return "", false
	}
	if got, ok := value(first); ok {
		t.Fatalf("synced preview kept the removed value %q", got)
	}
	if got, ok := value(second); !ok || got != "sk_test_mine" {
		t.Fatalf("preview's own value = (%q, %v), want it kept", got, ok)
	}
}
