// handlers_env_diff_test.go — unit tests for
// buildEnvDiffResponse (ADR-117 PR-C).
//
// The handler itself (s.envDiff) is exercised in the e2e
// suite (cmd/e2e/env_diff_e2e_test.go) where the full
// server + Store + HMAC key are wired. The pure-Go matrix
// builder is testable in isolation here — no server, no
// Store, no auth surface.

package main

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBuildEnvDiffResponse_Empty(t *testing.T) {
	got := buildEnvDiffResponse("app-empty", nil, nil)
	if got.AppSlug != "app-empty" {
		t.Errorf("AppSlug = %q, want %q", got.AppSlug, "app-empty")
	}
	if len(got.Scopes) != 0 {
		t.Errorf("Scopes = %+v, want empty", got.Scopes)
	}
	if len(got.Rows) != 0 {
		t.Errorf("Rows = %+v, want empty", got.Rows)
	}
	if got.GeneratedAt.IsZero() {
		t.Error("GeneratedAt is zero; want non-zero timestamp")
	}
	if time.Since(got.GeneratedAt) > 5*time.Second {
		t.Errorf("GeneratedAt = %v, want within 5s of now", got.GeneratedAt)
	}
}

func TestBuildEnvDiffResponse_SecretCellsCarryValueHashNeverValue(t *testing.T) {
	// This is the load-bearing security property of the
	// endpoint: a secret row's cell must never emit a 'value'
	// field. The DTO's omitempty on Value provides this, but
	// we pin the wire shape here so a future contributor
	// adding a Value to the secret branch is caught.
	secrets := []state.AppSecret{
		{AccountID: "a", AppID: "b", Scope: "prod", Key: "STRIPE_KEY", ValueHash: "abcdef0123456789"},
		{AccountID: "a", AppID: "b", Scope: "staging", Key: "STRIPE_KEY", ValueHash: "1111111111111111"},
	}
	got := buildEnvDiffResponse("sec-app", secrets, nil)
	if len(got.Rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(got.Rows))
	}
	if got.Rows[0].Kind != api.EnvDiffKindSecret {
		t.Errorf("Kind = %q, want %q", got.Rows[0].Kind, api.EnvDiffKindSecret)
	}
	if got.Rows[0].Key != "STRIPE_KEY" {
		t.Errorf("Key = %q, want STRIPE_KEY", got.Rows[0].Key)
	}
	// The cell must carry value_hash; value must be empty.
	prod := got.Rows[0].Cells["prod"]
	if !prod.Present {
		t.Error("prod cell Present = false, want true")
	}
	if prod.ValueHash != "abcdef0123456789" {
		t.Errorf("prod.ValueHash = %q, want abcdef0123456789", prod.ValueHash)
	}
	if prod.Value != "" {
		t.Errorf("prod.Value = %q, want \"\" (secret cells must NEVER carry plaintext)", prod.Value)
	}
	staging := got.Rows[0].Cells["staging"]
	if !staging.Present || staging.ValueHash != "1111111111111111" || staging.Value != "" {
		t.Errorf("staging cell = %+v, want {Present:true,ValueHash:1111111111111111,Value:\"\"}", staging)
	}
}

func TestBuildEnvDiffResponse_EnvCellsCarryValueNeverValueHash(t *testing.T) {
	// Mirror of the secret test for the env branch.
	envs := []state.AppEnv{
		{AccountID: "a", AppID: "b", Scope: "prod", Key: "LOG_LEVEL", Value: "info"},
		{AccountID: "a", AppID: "b", Scope: "staging", Key: "LOG_LEVEL", Value: "debug"},
	}
	got := buildEnvDiffResponse("env-app", nil, envs)
	if len(got.Rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(got.Rows))
	}
	if got.Rows[0].Kind != api.EnvDiffKindEnv {
		t.Errorf("Kind = %q, want %q", got.Rows[0].Kind, api.EnvDiffKindEnv)
	}
	prod := got.Rows[0].Cells["prod"]
	if !prod.Present || prod.Value != "info" || prod.ValueHash != "" {
		t.Errorf("prod cell = %+v, want {Present:true,Value:info,ValueHash:\"\"}", prod)
	}
}

func TestBuildEnvDiffResponse_PrePRCRowEmitsEmptyValueHash(t *testing.T) {
	// Pre-PR-C rows have value_hash = '' (NULL in PG, COALESCE
	// surfaces ''). The cell must carry Present=true but
	// ValueHash="" (omitempty drops the key from the wire).
	secrets := []state.AppSecret{
		{AccountID: "a", AppID: "b", Scope: "prod", Key: "OLD_KEY"}, // no ValueHash
	}
	got := buildEnvDiffResponse("pre-prc", secrets, nil)
	cell := got.Rows[0].Cells["prod"]
	if !cell.Present {
		t.Error("Present = false, want true (the row exists, just lacks value_hash)")
	}
	if cell.ValueHash != "" {
		t.Errorf("ValueHash = %q, want \"\" (pre-PR-C row)", cell.ValueHash)
	}
}

func TestBuildEnvDiffResponse_MissingScopeRendersPresentFalse(t *testing.T) {
	// STRIPE_KEY exists in prod + staging with DIFFERENT
	// value_hashes (so the matrix renderer distinguishes
	// them) but is MISSING in dev. The dev cell must
	// exist in the Cells map with Present=false; the
	// matrix is unioned over the actual scope set.
	secrets := []state.AppSecret{
		{AccountID: "a", AppID: "b", Scope: "prod", Key: "STRIPE_KEY", ValueHash: "deadbeefdeadbeef"},
		{AccountID: "a", AppID: "b", Scope: "staging", Key: "STRIPE_KEY", ValueHash: "1234567890abcdef"},
		{AccountID: "a", AppID: "b", Scope: "dev", Key: "OTHER_KEY", ValueHash: "feedfacefeedface"},
	}
	got := buildEnvDiffResponse("miss-scope", secrets, nil)
	// Scopes are unioned: dev, prod, staging (sorted ASC).
	if len(got.Scopes) != 3 {
		t.Fatalf("Scopes = %+v, want 3 (dev, prod, staging)", got.Scopes)
	}
	// Find the STRIPE_KEY row (sorted second, after OTHER_KEY).
	var stripeRow *api.EnvDiffRow
	for i := range got.Rows {
		if got.Rows[i].Key == "STRIPE_KEY" {
			stripeRow = &got.Rows[i]
			break
		}
	}
	if stripeRow == nil {
		t.Fatal("STRIPE_KEY row missing from Rows")
	}
	// STRIPE_KEY has cells for ALL three scopes (including
	// dev where it's missing).
	if len(stripeRow.Cells) != 3 {
		t.Fatalf("STRIPE_KEY has %d cells, want 3 (dev/prod/staging)", len(stripeRow.Cells))
	}
	dev := stripeRow.Cells["dev"]
	if dev.Present {
		t.Error("dev.Present = true, want false (STRIPE_KEY not in dev)")
	}
	if dev.ValueHash != "" {
		t.Errorf("dev.ValueHash = %q, want \"\" (missing row has no hash)", dev.ValueHash)
	}
	prod := stripeRow.Cells["prod"]
	if !prod.Present || prod.ValueHash != "deadbeefdeadbeef" {
		t.Errorf("prod cell = %+v, want {Present:true,ValueHash:deadbeefdeadbeef}", prod)
	}
}

func TestBuildEnvDiffResponse_SortedKeysAndScopes(t *testing.T) {
	// Wire shape stability: keys sorted ASC, scopes sorted ASC.
	secrets := []state.AppSecret{
		{Key: "Z", Scope: "z-scope", ValueHash: "fefefefefefefefe"},
		{Key: "A", Scope: "a-scope", ValueHash: "abababababababab"},
		{Key: "M", Scope: "m-scope", ValueHash: "babababababababa"},
	}
	envs := []state.AppEnv{
		{Key: "B", Scope: "z-scope", Value: "1"},
		{Key: "Y", Scope: "a-scope", Value: "2"},
	}
	got := buildEnvDiffResponse("sort", secrets, envs)
	// Scopes sorted ASC: a-scope < m-scope < z-scope
	if len(got.Scopes) != 3 || got.Scopes[0] != "a-scope" || got.Scopes[1] != "m-scope" || got.Scopes[2] != "z-scope" {
		t.Errorf("Scopes = %+v, want [a-scope m-scope z-scope]", got.Scopes)
	}
	// Rows sorted ASC by key: A, B, M, Y, Z
	if len(got.Rows) != 5 {
		t.Fatalf("want 5 rows, got %d", len(got.Rows))
	}
	for i, want := range []string{"A", "B", "M", "Y", "Z"} {
		if got.Rows[i].Key != want {
			t.Errorf("Rows[%d].Key = %q, want %q", i, got.Rows[i].Key, want)
		}
	}
}

func TestBuildEnvDiffResponse_UnionByKeyNotByKind(t *testing.T) {
	// DATABASE_URL exists in BOTH app_secrets (kind=secret)
	// and app_envs (kind=env). The matrix must render TWO
	// rows (one per kind) — the customer wants to see "is it
	// a secret OR an env var, or both?" and the unioned-by-
	// key-but-not-by-kind shape is the answer.
	secrets := []state.AppSecret{
		{Key: "DATABASE_URL", Scope: "prod", ValueHash: "deadbeefdeadbeef"},
	}
	envs := []state.AppEnv{
		{Key: "DATABASE_URL", Scope: "prod", Value: "postgres://prod/db"},
	}
	got := buildEnvDiffResponse("union", secrets, envs)
	if len(got.Rows) != 2 {
		t.Fatalf("want 2 rows (secret + env for same key), got %d", len(got.Rows))
	}
	// Both rows share the key; the env row sorts first
	// (env < secret under the tiebreak).
	if got.Rows[0].Kind != api.EnvDiffKindEnv {
		t.Errorf("Rows[0].Kind = %q, want env", got.Rows[0].Kind)
	}
	if got.Rows[1].Kind != api.EnvDiffKindSecret {
		t.Errorf("Rows[1].Kind = %q, want secret", got.Rows[1].Kind)
	}
}
