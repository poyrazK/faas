// account_scoped_e2e_test.go — M7 acceptance for issue #393 (the three
// account-scoped list endpoints).
//
// Subprocess-apid + real PgStore path (no KVM). Pins:
//
//   - GET /v1/instances returns instances across the caller's account,
//     each carrying app_id. Cross-account isolation via apps.account_id
//     JOIN. The full cursor walk exercises UUIDv7 id ordering (the
//     unit test defers to e2e for the strict "all rows reachable"
//     contract).
//   - GET /v1/secrets returns sealed envelopes; plaintext NEVER crosses
//     the wire (marker-seed + body-grep).
//   - GET /v1/apps/metrics?range= returns 200 with
//     `source: "degraded: prometheus not configured"` when apid has no
//     Prometheus client wired — the dashboard's empty-state contract.
//   - The strict-mode 400 on ?range= outside the closed vocabulary.
//
// Build tag: (none). CI-safe. Requires Postgres (skip via
// FAAS_SKIP_PG_TESTS).

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedAccount creates an account + admin-scoped API key directly in the
// store. The key plaintext is the bearer token for subsequent
// doReq calls. Mirrors the pattern in invocations_e2e_test.go but
// keeps account + key as separate steps (the original combined
// helper created an app too, which collided with later POST /v1/apps
// calls when tests needed slug control).
func seedAccount(t *testing.T, h *e2etest.Harness, ctx context.Context,
	label string, plan api.Plan) (string, string) {
	t.Helper()
	store := state.NewPgStore(h.Pool)
	res, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
		Email: "e2e+" + label + "@test.example",
		Plan:  plan,
	})
	if err != nil {
		t.Fatalf("CreateAccountWithPersonalOrg: %v", err)
	}
	acct := res.Account
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "e2e", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	return acct.ID, pt
}

// seedApp creates one app under acct. Used after seedAccount so each
// test controls slugs without colliding with helper-side defaults.
func seedApp(t *testing.T, ctx context.Context, h *e2etest.Harness,
	acctID, slug string) state.App {
	t.Helper()
	store := state.NewPgStore(h.Pool)
	app, err := store.CreateApp(ctx, state.App{
		AccountID:      acctID,
		Slug:           slug,
		Type:           state.AppTypeApp,
		RAMMB:          256,
		MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return app
}

// seedDeployment creates a live deployment for the app. Mirrors the
// pattern in invocations_e2e_test.go.
func seedDeployment(t *testing.T, h *e2etest.Harness, ctx context.Context, appID string) state.Deployment {
	t.Helper()
	store := state.NewPgStore(h.Pool)
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       appID,
		Status:      state.DeployLive,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:3933333333333333333333333333333333333333333333333333333333333333",
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return dep
}

// TestE2E_ListInstancesForAccount_AcrossApps seeds 3 apps × 2 instances
// each on one account, then asserts GET /v1/instances returns all 6
// rows with their app_ids populated.
func TestE2E_ListInstancesForAccount_AcrossApps(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.Start(t, pool, e2etest.APID)
	store := state.NewPgStore(h.Pool)
	ctx := context.Background()
	nodeID := defaultLocalComputeNodeID(t, ctx, store)

	res, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
		Email: "e2e+fanin@test.example",
		Plan:  api.PlanHobby,
	})
	if err != nil {
		t.Fatalf("CreateAccountWithPersonalOrg fanin: %v", err)
	}
	acct := res.Account
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "e2e", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	faninApps := make([]state.App, 0, 3)
	for _, slug := range []string{"fa", "fb", "fc"} {
		app, err := store.CreateApp(ctx, state.App{
			AccountID:      acct.ID,
			Slug:           slug,
			Type:           state.AppTypeApp,
			RAMMB:          256,
			MaxConcurrency: 1,
		})
		if err != nil {
			t.Fatalf("CreateApp: %v", err)
		}
		dep := seedDeployment(t, h, ctx, app.ID)
		for i := 0; i < 2; i++ {
			if _, err := store.CreateInstance(ctx, app.ID, dep.ID,
				string(state.StateRunning), 256, nodeID, ""); err != nil {
				t.Fatalf("CreateInstance: %v", err)
			}
		}
		faninApps = append(faninApps, app)
	}

	rec, _ := doReq(t, h, pt, http.MethodGet, "/v1/instances", nil)
	var out api.ListInstancesResponse
	if err := json.Unmarshal(rec, &out); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, rec)
	}
	if len(out.Instances) != 6 {
		t.Fatalf("expected 6 instances, got %d (body=%s)", len(out.Instances), rec)
	}
	seen := map[string]int{}
	for _, ins := range out.Instances {
		seen[ins.AppID]++
		if ins.ID == "" {
			t.Errorf("instance missing id: %+v", ins)
		}
		if ins.State != string(state.StateRunning) {
			t.Errorf("instance state=%q, want %q", ins.State, state.StateRunning)
		}
	}
	if seen[faninApps[0].ID] != 2 || seen[faninApps[1].ID] != 2 || seen[faninApps[2].ID] != 2 {
		t.Fatalf("expected 2/2/2 split across apps, got %v", seen)
	}
	if out.NextBefore != "" {
		t.Errorf("page fits under default limit but next_before=%q", out.NextBefore)
	}
}

// TestE2E_ListInstancesForAccount_CursorPagination seeds 5 instances
// and walks them with ?limit=2 + ?before=<id>. Asserts every row is
// reachable exactly once — the strict "all rows reachable" contract the
// cursor-by-id walk pins (the unit test only checks the page shape
// because MemStore's random newID() can't model UUIDv7 monotonicity).
func TestE2E_ListInstancesForAccount_CursorPagination(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.Start(t, pool, e2etest.APID)
	store := state.NewPgStore(h.Pool)
	ctx := context.Background()
	nodeID := defaultLocalComputeNodeID(t, ctx, store)

	acctID, key := seedAccount(t, h, ctx, "pagi", api.PlanHobby)
	app := seedApp(t, ctx, h, acctID, "pagi-app")
	dep := seedDeployment(t, h, ctx, app.ID)
	for i := 0; i < 5; i++ {
		if _, err := store.CreateInstance(ctx, app.ID, dep.ID,
			string(state.StateRunning), 256, nodeID, ""); err != nil {
			t.Fatalf("CreateInstance[%d]: %v", i, err)
		}
	}

	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 5; page++ {
		path := "/v1/instances?limit=2"
		if cursor != "" {
			path += "&before=" + cursor
		}
		rec, _ := doReq(t, h, key, http.MethodGet, path, nil)
		var out api.ListInstancesResponse
		if err := json.Unmarshal(rec, &out); err != nil {
			t.Fatalf("page %d unmarshal: %v (body=%s)", page, err, rec)
		}
		if len(out.Instances) == 0 {
			break
		}
		for _, ins := range out.Instances {
			if seen[ins.ID] {
				t.Errorf("page %d: instance %s returned twice", page, ins.ID)
			}
			seen[ins.ID] = true
		}
		cursor = out.NextBefore
		if cursor == "" {
			break
		}
	}
	if len(seen) != 5 {
		t.Fatalf("expected to walk all 5 instances, got %d", len(seen))
	}
}

// TestE2E_ListInstancesForAccount_CrossAccountIsolation seeds
// instances under two accounts and asserts that A's GET /v1/instances
// does NOT include B's instances, and vice versa. Pins the SQL JOIN
// on apps.account_id = $1 (the only IDOR guard; no per-handler check).
func TestE2E_ListInstancesForAccount_CrossAccountIsolation(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.Start(t, pool, e2etest.APID)
	store := state.NewPgStore(h.Pool)
	ctx := context.Background()
	nodeID := defaultLocalComputeNodeID(t, ctx, store)

	acctA, keyA := seedAccount(t, h, ctx, "iso-a", api.PlanHobby)
	acctB, keyB := seedAccount(t, h, ctx, "iso-b", api.PlanHobby)
	appA := seedApp(t, ctx, h, acctA, "iso-a-app")
	appB := seedApp(t, ctx, h, acctB, "iso-b-app")

	for _, app := range []state.App{appA, appB} {
		dep := seedDeployment(t, h, ctx, app.ID)
		for i := 0; i < 3; i++ {
			if _, err := store.CreateInstance(ctx, app.ID, dep.ID,
				string(state.StateRunning), 256, nodeID, ""); err != nil {
				t.Fatalf("CreateInstance: %v", err)
			}
		}
	}

	// A reads → sees A's app's 3 instances only.
	recA, _ := doReq(t, h, keyA, http.MethodGet, "/v1/instances", nil)
	var outA api.ListInstancesResponse
	if err := json.Unmarshal(recA, &outA); err != nil {
		t.Fatalf("A unmarshal: %v", err)
	}
	for _, ins := range outA.Instances {
		if ins.AppID == appB.ID {
			t.Errorf("A saw B's instance: app_id=%s", ins.AppID)
		}
		if ins.AppID != appA.ID {
			t.Errorf("A saw a non-A instance: app_id=%s", ins.AppID)
		}
	}

	recB, _ := doReq(t, h, keyB, http.MethodGet, "/v1/instances", nil)
	var outB api.ListInstancesResponse
	if err := json.Unmarshal(recB, &outB); err != nil {
		t.Fatalf("B unmarshal: %v", err)
	}
	for _, ins := range outB.Instances {
		if ins.AppID != appB.ID {
			t.Errorf("B saw a non-B instance: app_id=%s", ins.AppID)
		}
	}
}

// TestE2E_ListSecretsForAccount_PlaintextInvariant seeds a secret with
// a high-entropy marker, then asserts the marker NEVER appears in the
// GET /v1/secrets response body. Mirrors the unit test shape but on
// the real PgStore path with real age-sealed ciphertext.
func TestE2E_ListSecretsForAccount_PlaintextInvariant(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tmpDir := t.TempDir()
	recipientPath := tmpDir + "/host.age.pub"
	identityPath := recipientPath + ".priv"
	if err := writeTestRecipient(recipientPath); err != nil {
		t.Fatal(err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + recipientPath,
		"FAAS_HOST_AGE_IDENTITY_PATH=" + identityPath,
	})
	ctx := context.Background()

	acctID, key := seedAccount(t, h, ctx, "plaintext", api.PlanHobby)
	_ = acctID // app "plaintext-app" is created by the test via POST /v1/apps below

	const markerA = "PLAINTEXT-MARKER-ALPHA-99"
	const markerB = "PLAINTEXT-MARKER-BETA-77"

	if statusOnly(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "plaintext-a"}) != http.StatusCreated {
		t.Fatalf("create plaintext-a")
	}
	if statusOnly(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "plaintext-b"}) != http.StatusCreated {
		t.Fatalf("create plaintext-b")
	}
	if statusOnly(t, h, key, http.MethodPut,
		"/v1/apps/plaintext-a/secrets/MARKER_A",
		api.PutAppSecretRequest{Value: markerA}) != http.StatusOK {
		t.Fatalf("PUT MARKER_A")
	}
	if statusOnly(t, h, key, http.MethodPut,
		"/v1/apps/plaintext-b/secrets/MARKER_B",
		api.PutAppSecretRequest{Value: markerB}) != http.StatusOK {
		t.Fatalf("PUT MARKER_B")
	}

	// GET /v1/secrets — markers must NOT appear.
	rec, _ := doReq(t, h, key, http.MethodGet, "/v1/secrets", nil)
	if strings.Contains(string(rec), markerA) {
		t.Errorf("plaintext MARKER_A leaked in /v1/secrets: %s", rec)
	}
	if strings.Contains(string(rec), markerB) {
		t.Errorf("plaintext MARKER_B leaked in /v1/secrets: %s", rec)
	}
	var out api.ListSecretsForAccountResponse
	if err := json.Unmarshal(rec, &out); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, rec)
	}
	if len(out.Secrets) != 2 {
		t.Fatalf("expected 2 sealed envelopes, got %d", len(out.Secrets))
	}
	for _, s := range out.Secrets {
		if s.Ciphertext == "" {
			t.Errorf("ciphertext must be non-empty base64")
		}
		if s.AppID == "" || s.AppSlug == "" {
			t.Errorf("row missing owning app identifier: %+v", s)
		}
	}
}

// TestE2E_ListSecretsForAccount_CrossAccountIsolation seeds a secret
// under each of two accounts and asserts each account's GET /v1/secrets
// returns ONLY its own row. The pgstore's JOIN on apps.account_id = $1
// is the only IDOR guard.
func TestE2E_ListSecretsForAccount_CrossAccountIsolation(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tmpDir := t.TempDir()
	recipientPath := tmpDir + "/host.age.pub"
	identityPath := recipientPath + ".priv"
	if err := writeTestRecipient(recipientPath); err != nil {
		t.Fatal(err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + recipientPath,
		"FAAS_HOST_AGE_IDENTITY_PATH=" + identityPath,
	})
	ctx := context.Background()

	_, keyA := seedAccount(t, h, ctx, "iso-sa", api.PlanHobby)
	_, keyB := seedAccount(t, h, ctx, "iso-sb", api.PlanHobby)

	if statusOnly(t, h, keyA, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "sa-app"}) != http.StatusCreated {
		t.Fatalf("create sa-app")
	}
	if statusOnly(t, h, keyB, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "sb-app"}) != http.StatusCreated {
		t.Fatalf("create sb-app")
	}
	if statusOnly(t, h, keyA, http.MethodPut,
		"/v1/apps/sa-app/secrets/ALPHA",
		api.PutAppSecretRequest{Value: "a-value"}) != http.StatusOK {
		t.Fatalf("PUT ALPHA")
	}
	if statusOnly(t, h, keyB, http.MethodPut,
		"/v1/apps/sb-app/secrets/BETA",
		api.PutAppSecretRequest{Value: "b-value"}) != http.StatusOK {
		t.Fatalf("PUT BETA")
	}

	recA, _ := doReq(t, h, keyA, http.MethodGet, "/v1/secrets", nil)
	var outA api.ListSecretsForAccountResponse
	if err := json.Unmarshal(recA, &outA); err != nil {
		t.Fatalf("A unmarshal: %v", err)
	}
	if len(outA.Secrets) != 1 {
		t.Fatalf("A expected 1 secret, got %d", len(outA.Secrets))
	}
	if outA.Secrets[0].Key != "ALPHA" {
		t.Errorf("A got wrong key: %q", outA.Secrets[0].Key)
	}

	recB, _ := doReq(t, h, keyB, http.MethodGet, "/v1/secrets", nil)
	var outB api.ListSecretsForAccountResponse
	if err := json.Unmarshal(recB, &outB); err != nil {
		t.Fatalf("B unmarshal: %v", err)
	}
	if len(outB.Secrets) != 1 {
		t.Fatalf("B expected 1 secret, got %d", len(outB.Secrets))
	}
	if outB.Secrets[0].Key != "BETA" {
		t.Errorf("B got wrong key: %q", outB.Secrets[0].Key)
	}
}

// TestE2E_GetAppsMetrics_Degraded covers the apid-no-Prometheus path:
// the handler short-circuits to `source: "degraded: prometheus not
// configured"` with apps=nil — the dashboard's empty-state contract.
func TestE2E_GetAppsMetrics_Degraded(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.Start(t, pool, e2etest.APID) // no FAAS_APID_PROM_URL → no prom client
	ctx := context.Background()
	_, key := seedAccount(t, h, ctx, "deg", api.PlanHobby)

	rec, _ := doReq(t, h, key, http.MethodGet, "/v1/apps/metrics?range=5m", nil)
	if !strings.HasPrefix(string(rec), "{") {
		t.Fatalf("response not JSON: %s", rec)
	}
	var out api.AppsMetricsResponse
	if err := json.Unmarshal(rec, &out); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, rec)
	}
	if !strings.HasPrefix(out.Source, "degraded:") {
		t.Errorf("source: got %q want degraded:<reason>", out.Source)
	}
	if out.Apps != nil {
		t.Errorf("apps must be nil on degraded, got %+v", out.Apps)
	}
	if out.Range != "5m" {
		t.Errorf("range: got %q want 5m", out.Range)
	}
	if out.AsOf == "" {
		t.Errorf("as_of must be populated")
	}
}

// TestE2E_GetAppsMetrics_InvalidRange pins the strict-mode 400 on
// ?range= outside the closed vocabulary (5m|15m|1h|6h|24h|7d|15d).
func TestE2E_GetAppsMetrics_InvalidRange(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()
	_, key := seedAccount(t, h, ctx, "rng", api.PlanHobby)

	raw, code := doReq(t, h, key, http.MethodGet, "/v1/apps/metrics?range=99y", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 (body=%s)", code, raw)
	}
	var p api.Problem
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, raw)
	}
	if p.Code != api.CodeValidation {
		t.Errorf("code: got %q want %q", p.Code, api.CodeValidation)
	}
}
