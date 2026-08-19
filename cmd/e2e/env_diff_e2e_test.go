// env_diff_e2e_test.go — ADR-117 PR-C env-diff matrix wire-surface
// acceptance test.
//
// Nine assertions, in order:
//
//   1. Seed a Pro-plan app + recipient + identity pair (the PUT path
//      stamps kid alongside ciphertext, so the identity is required;
//      pre-PR-C the test would 503 on the kid-stamping guard).
//   2. Seed `DATABASE_URL` secret in prod and staging (DIFFERENT
//      values: prod = "postgres://prod-host/db", staging =
//      "postgres://stg-host/db"). Both should produce DIFFERENT
//      value_hashes.
//   3. Seed `LOG_LEVEL` env var in prod (= "debug") and staging
//      (= "info"). Plaintext env vars — value visible on the wire.
//   4. Seed `STRIPE_KEY` secret ONLY in prod (= "sk_test_xxx"). The
//      staging cell must be Present=false.
//   5. GET /v1/apps/{slug}/env-diff → assert 3 rows ordered ASC by
//      key (DATABASE_URL, LOG_LEVEL, STRIPE_KEY).
//   6. Assert DATABASE_URL row: kind=secret, prod.value_hash !=
//      staging.value_hash, both Present=true.
//   7. Assert LOG_LEVEL row: kind=env, prod.value="debug",
//      staging.value="info".
//   8. Assert STRIPE_KEY row: kind=secret, prod cell present,
//      staging cell Present=false.
//   9. Property test: walk the entire JSON response and assert NO
//      secret plaintext appears anywhere (defense-in-depth; the
//      renderer is supposed to never emit `value` on secret cells,
//      but the test catches a future regression).
//
// Why this is KVM-free: apid owns the env-diff matrix read path;
// the wire surface is HTTP. schedd/vmmd are not in the loop, so
// the wake-paths are covered separately by the metal suite.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// TestEnvDiffSurfacePg is the ADR-117 PR-C wire-surface acceptance.
// It exercises the nine assertions listed in the file header in
// order — keeping the order stable is important because each
// assertion leaves state (rows in app_secrets / app_envs) the next
// assertion depends on.
func TestEnvDiffSurfacePg(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Recipient + identity (the pair apid needs to seal + stamp
	// kid). PR-C adds value_hash stamping on the same path.
	tmpDir := t.TempDir()
	recipientPath := filepath.Join(tmpDir, "host.age.pub")
	identityPath := recipientPath + ".priv"
	if err := writeTestRecipient(recipientPath); err != nil {
		t.Fatalf("write recipient: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + recipientPath,
		"FAAS_HOST_AGE_IDENTITY_PATH=" + identityPath,
	})

	// Assertion 1: seed Pro account + app (Pro has a generous
	// SecretCountMax so we don't hit quota in this test).
	const plan = api.PlanPro
	key := h.SeedAccount(context.Background(), plan, "env-diff")
	const slug = "env-diff-app"
	if code := statusOnly(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug}); code != http.StatusCreated {
		t.Fatalf("create app: %d", code)
	}

	// putSecret writes a secret at a given scope; mirrors
	// the helper in secrets_scope_e2e_test.go.
	putSecret := func(scope, keyName, value string) {
		t.Helper()
		path := "/v1/apps/" + slug + "/secrets/" + keyName
		if scope != "" {
			path += "?scope=" + url.QueryEscape(scope)
		}
		body := api.PutAppSecretRequest{Value: value}
		if code := statusOnly(t, h, key, http.MethodPut, path, body); code != http.StatusOK {
			t.Fatalf("PUT %s scope=%q: %d", keyName, scope, code)
		}
	}
	putEnv := func(scope, keyName, value string) {
		t.Helper()
		path := "/v1/apps/" + slug + "/env/" + keyName
		if scope != "" {
			path += "?scope=" + url.QueryEscape(scope)
		}
		body := api.PutAppEnvRequest{Value: value}
		if code := statusOnly(t, h, key, http.MethodPut, path, body); code != http.StatusOK {
			t.Fatalf("PUT env %s scope=%q: %d", keyName, scope, code)
		}
	}

	// Assertion 2: DATABASE_URL secret at prod + staging, different
	// values. The 16-hex value_hash must differ.
	putSecret("prod", "DATABASE_URL", "postgres://prod-host/db")
	putSecret("staging", "DATABASE_URL", "postgres://stg-host/db")
	// Assertion 3: LOG_LEVEL env var at prod + staging, different
	// values. Env cells expose the literal value.
	putEnv("prod", "LOG_LEVEL", "debug")
	putEnv("staging", "LOG_LEVEL", "info")
	// Assertion 4: STRIPE_KEY secret ONLY at prod.
	putSecret("prod", "STRIPE_KEY", "sk_test_xxx")

	// Fetch the env-diff matrix.
	raw, status := doReq(t, h, key, http.MethodGet,
		"/v1/apps/"+slug+"/env-diff", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /env-diff: %d (body=%s)", status, raw)
	}
	var diff api.EnvDiffResponse
	if err := json.Unmarshal(raw, &diff); err != nil {
		t.Fatalf("decode EnvDiffResponse: %v (body=%s)", err, raw)
	}

	// Assertion 5: 3 rows, ordered ASC by key.
	if len(diff.Rows) != 3 {
		t.Fatalf("rows = %d, want 3 (DATABASE_URL, LOG_LEVEL, STRIPE_KEY). body=%s",
			len(diff.Rows), raw)
	}
	wantKeys := []string{"DATABASE_URL", "LOG_LEVEL", "STRIPE_KEY"}
	for i, want := range wantKeys {
		if diff.Rows[i].Key != want {
			t.Errorf("Rows[%d].Key = %q, want %q (body=%s)", i, diff.Rows[i].Key, want, raw)
		}
	}

	// Assertion 6: DATABASE_URL row — secret, prod + staging cells
	// Present=true with DIFFERENT value_hashes.
	database := findRow(t, diff, "DATABASE_URL")
	if database.Kind != api.EnvDiffKindSecret {
		t.Errorf("DATABASE_URL.Kind = %q, want secret (body=%s)", database.Kind, raw)
	}
	databaseProd, ok := database.Cells["prod"]
	if !ok {
		t.Fatalf("DATABASE_URL missing prod cell. body=%s", raw)
	}
	if !databaseProd.Present {
		t.Errorf("DATABASE_URL prod.Present = false, want true (body=%s)", raw)
	}
	if databaseProd.ValueHash == "" {
		t.Errorf("DATABASE_URL prod.ValueHash = \"\", want non-empty (body=%s)", raw)
	}
	databaseStaging := database.Cells["staging"]
	if !databaseStaging.Present {
		t.Errorf("DATABASE_URL staging.Present = false, want true (body=%s)", raw)
	}
	if databaseStaging.ValueHash == "" {
		t.Errorf("DATABASE_URL staging.ValueHash = \"\", want non-empty (body=%s)", raw)
	}
	if databaseProd.ValueHash == databaseStaging.ValueHash {
		t.Errorf("DATABASE_URL prod.value_hash == staging.value_hash (%q); DIFFERENT plaintexts must produce DIFFERENT hashes (body=%s)",
			databaseProd.ValueHash, raw)
	}
	// Security: secret cell NEVER carries value.
	if databaseProd.Value != "" {
		t.Errorf("DATABASE_URL prod.Value = %q, want \"\" (secret cells must NEVER carry plaintext; body=%s)",
			databaseProd.Value, raw)
	}
	if databaseStaging.Value != "" {
		t.Errorf("DATABASE_URL staging.Value = %q, want \"\" (body=%s)", databaseStaging.Value, raw)
	}

	// Assertion 7: LOG_LEVEL row — env, prod + staging cells
	// carry the LITERAL values.
	logLevel := findRow(t, diff, "LOG_LEVEL")
	if logLevel.Kind != api.EnvDiffKindEnv {
		t.Errorf("LOG_LEVEL.Kind = %q, want env (body=%s)", logLevel.Kind, raw)
	}
	logProd := logLevel.Cells["prod"]
	if !logProd.Present || logProd.Value != "debug" {
		t.Errorf("LOG_LEVEL prod cell = %+v, want {Present:true,Value:debug} (body=%s)", logProd, raw)
	}
	if logProd.ValueHash != "" {
		t.Errorf("LOG_LEVEL prod.ValueHash = %q, want \"\" (env cells must NEVER carry value_hash; body=%s)",
			logProd.ValueHash, raw)
	}
	logStaging := logLevel.Cells["staging"]
	if !logStaging.Present || logStaging.Value != "info" {
		t.Errorf("LOG_LEVEL staging cell = %+v, want {Present:true,Value:info} (body=%s)", logStaging, raw)
	}

	// Assertion 8: STRIPE_KEY row — secret, prod cell Present=true,
	// staging cell Present=false.
	stripe := findRow(t, diff, "STRIPE_KEY")
	if stripe.Kind != api.EnvDiffKindSecret {
		t.Errorf("STRIPE_KEY.Kind = %q, want secret (body=%s)", stripe.Kind, raw)
	}
	stripeProd := stripe.Cells["prod"]
	if !stripeProd.Present || stripeProd.ValueHash == "" {
		t.Errorf("STRIPE_KEY prod cell = %+v, want {Present:true,ValueHash:non-empty} (body=%s)",
			stripeProd, raw)
	}
	stripeStaging := stripe.Cells["staging"]
	if stripeStaging.Present {
		t.Errorf("STRIPE_KEY staging.Present = true, want false (STRIPE_KEY only in prod; body=%s)", raw)
	}
	if stripeStaging.ValueHash != "" {
		t.Errorf("STRIPE_KEY staging.ValueHash = %q, want \"\" (missing row has no hash; body=%s)",
			stripeStaging.ValueHash, raw)
	}

	// Assertion 9: property test — walk the raw JSON and assert
	// no secret plaintext leaks. The renderer is supposed to
	// never emit `value` on secret cells, but this catches a
	// future regression (e.g. a contributor adding a Value
	// field to the secret branch of buildEnvDiffResponse).
	for _, secret := range []string{
		"postgres://prod-host/db",
		"postgres://stg-host/db",
		"sk_test_xxx",
	} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("raw env-diff response leaks secret plaintext %q (body=%s)",
				secret, raw)
		}
	}
}

// findRow locates the row with the given key. Errors if not
// found so the caller can `t.Fatalf`-style assertions on
// the returned row without nil-checking.
func findRow(t *testing.T, diff api.EnvDiffResponse, key string) api.EnvDiffRow {
	t.Helper()
	for _, r := range diff.Rows {
		if r.Key == key {
			return r
		}
	}
	t.Fatalf("row %q not found in env-diff response (rows=%v)", key, diff.Rows)
	return api.EnvDiffRow{}
}
