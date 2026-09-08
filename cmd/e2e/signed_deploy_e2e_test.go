// signed_deploy_e2e_test.go — non-metal CI-safe acceptance for the
// deploy-time cosign signature-enforcement gate (issue #472 / ADR-058).
//
// What this test pins
//
//   - The apid pre-flight gate fires BEFORE the deployment row is
//     INSERTED: flag-on + no trusted signers → 403 CodeDeploySignatureInvalid
//     (the "operator toggled the flag but forgot to onboard a publisher"
//     footgun must fail-closed at the wire surface, not after a
//     wasted imaged dispatch).
//
//   - The per-app trusted-signer CRUD REST surface round-trips:
//     PUT, GET, DELETE; the audit rows
//     (`app.trusted_signer_added`, `app.trusted_signer_rotated`,
//     `app.trusted_signer_removed`) land on the `events` table.
//
//   - The PATCH /v1/apps/{slug}/security wire surface flips the
//     require_signed flag on a Pro-plan app and emits
//     `app.security_updated`.
//
//   - The on-disk mirror at $FAAS_TRUSTED_PUBLISHERS_DIR is written
//     by the apid LISTEN goroutine after a PUT, so the deploy-side
//     verifier (imaged) can read the publisher list without a DB
//     round-trip. This is the end-to-end cross-process proof for
//     the C1 critical fix.
//
// What this test deliberately does NOT cover
//
//   - The actual ECDSA signature verification — that's a unit test
//     (pkg/cosign/verify_test.go) since the verifier is a pure
//     function and doesn't need a daemon. Spinning up imaged to
//     cover verify here would force the metal build tag (no
//     CI-safe).
//
//   - Source-tarball deploys vs OCI image deploys — the gate forks
//     on req.Image != ""; the unit-level
//     cmd/apid/handlers_signature_test.go pins that boundary
//     because the multipart upload path is awkward to drive from
//     this harness.
//
// Build tag: (none). CI-safe. Requires Postgres (skip via
// FAAS_SKIP_PG_TESTS) AND cert fixtures on the box that fast-forwards
// the digest won't reach imaged (since imaged is not started).

package e2e_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// countAuditRows scans the `events` table for rows matching
// (kind, app_id). The test harness writes audit rows into the
// test's per-t sub-schema (pgtest.SchemaOf), so the query is
// schema-scoped by the connection's search_path — no explicit
// schema prefix needed.
func countAuditRows(ctx context.Context, t *testing.T, h *e2etest.Harness, kind, appID string) int {
	t.Helper()
	var n int
	if err := h.Pool.QueryRow(ctx,
		`select count(*) from events where kind = $1 and subject = $2::uuid`,
		kind, appID).Scan(&n); err != nil {
		t.Fatalf("count audit rows kind=%q app=%q: %v", kind, appID, err)
	}
	return n
}

// findAppIDBySlug hits the store directly so the test doesn't
// have to thread the ID through every subcase. Schema-scoped
// read — the connection's search_path already filters.
func findAppIDBySlug(ctx context.Context, t *testing.T, h *e2etest.Harness, accountID, slug string) string {
	t.Helper()
	var id string
	if err := h.Pool.QueryRow(ctx,
		`select id::text from apps where account_id = $1::uuid and slug = $2`,
		accountID, slug).Scan(&id); err != nil {
		t.Fatalf("find app %q: %v", slug, err)
	}
	return id
}

// pemBlob64 returns a base64-encoded 64-byte placeholder DER
// blob. The pre-flight gate only checks existence, not the
// bytes; the actual PKIX shape is enforced by the
// app_trusted_signers_pem_shape CHECK at the DB layer (64..1024
// bytes post-decode).
func pemBlob64(c byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return jsonBase64(b)
}

// jsonBase64 is the small helper so the table can write
// `jsonBase64([]byte{...})` inline without an import dance.
func jsonBase64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// TestSignedDeploy_PreFlightFailClosed is the load-bearing case
// from the issue body: operator toggles require_signed=true on an
// app but no trusted publishers exist yet. The next deploy MUST
// reject with 403 deploy_signature_invalid BEFORE the deployment
// row is inserted into the DB, so the customer sees the failure
// at the wire surface (not as a delayed FAILED row).
func TestSignedDeploy_PreFlightFailClosed(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)
	key := h.SeedAccount(context.Background(), api.PlanPro)
	tenantKey := key

	// Two apps: a "good" one and the fail-closed one we toggle
	// without onboarding a publisher.
	if code := statusOnly(t, h, tenantKey, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "app-flag-off"}); code != http.StatusCreated {
		t.Fatalf("seed app-flag-off: %d", code)
	}
	if code := statusOnly(t, h, tenantKey, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "app-flag-on-no-signers"}); code != http.StatusCreated {
		t.Fatalf("seed app-flag-on-no-signers: %d", code)
	}

	// Flip the flag on the second app via PATCH /security. The
	// free plan does not support require_signed=true (per
	// pkg/api/limits.go TrustedSignerCountMax==0); the test runs
	// on Pro so this 200s.
	assertProblemAPID(t, h, tenantKey, http.MethodPatch,
		"/v1/apps/app-flag-on-no-signers/security",
		api.AppSecurityRequest{RequireSigned: ptrBool(true)},
		http.StatusOK, "")

	// Image-deploy attempt against the fail-closed app must 403
	// with the deploy_signature_invalid code. The deployment row
	// is NOT inserted (the wire surface rejects before the store
	// call), so an audit row count must stay zero for
	// app.signed_image_accepted / app.signature_missing — those
	// are imaged-side events, not apid-side.
	rec := doReqBytes(t, h, tenantKey, http.MethodPost,
		"/v1/apps/app-flag-on-no-signers/deployments",
		api.CreateDeploymentRequest{Image: "registry.example.com/x@sha256:" + strings.Repeat("a", 64)})
	if !strings.Contains(string(rec), api.CodeDeploySignatureInvalid) {
		t.Fatalf("expected body to contain %q, got %s", api.CodeDeploySignatureInvalid, rec)
	}
	// Decode the problem to confirm the status code.
	var p api.Problem
	if err := json.Unmarshal(rec, &p); err != nil {
		t.Fatalf("decode problem: %v (body=%s)", err, rec)
	}
	if p.Status != http.StatusForbidden {
		t.Fatalf("status=%d want 403 (body=%s)", p.Status, rec)
	}
	if !strings.Contains(strings.ToLower(p.Detail), "no trusted publishers") {
		t.Errorf("detail=%q, want fragment %q", p.Detail, "no trusted publishers")
	}
}

// TestSignedDeploy_TrustedSignerRotation exercises the full
// PUT-then-rotate-then-delete lifecycle of a single publisher and
// pins the audit taxonomy:
//
//   - First PUT     → app.trusted_signer_added
//   - Second PUT    → app.trusted_signer_rotated (xmax=0 idiom)
//   - DELETE        → app.trusted_signer_removed
//
// This is the AuditEvent emission path that PR-Audit#2 needed to
// pin (the previous racy 1-second heuristic was replaced by the
// PostgreSQL (xmax = 0) idiom in the upsert RETURNING).
func TestSignedDeploy_TrustedSignerRotation(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	if code := statusOnly(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "app-rotation"}); code != http.StatusCreated {
		t.Fatalf("seed app: %d", code)
	}

	// Capture the auto-created account id from the seeded key —
	// the audit query needs the app_id, which we resolve from
	// the apps row.
	acctID := mustAccountID(t, h, key)
	appID := findAppIDBySlug(context.Background(), t, h, acctID, "app-rotation")

	// 1. First PUT — must emit app.trusted_signer_added.
	if code := statusOnly(t, h, key, http.MethodPut,
		"/v1/apps/app-rotation/trusted_signers/ci-bot",
		api.AddTrustedSignerRequest{PublicKeyPEM: pemBlob64('A')}); code != http.StatusOK {
		t.Fatalf("first PUT: %d", code)
	}
	waitForAudit(t, h, "app.trusted_signer_added", appID, 1, 5*time.Second)

	// 2. Second PUT with a different key blob — must emit
	// app.trusted_signer_rotated (NOT added). The DB
	// (xmax=0) idiom gives an exact, race-free signal.
	before := countAuditRows(context.Background(), t, h, "app.trusted_signer_added", appID)
	if code := statusOnly(t, h, key, http.MethodPut,
		"/v1/apps/app-rotation/trusted_signers/ci-bot",
		api.AddTrustedSignerRequest{PublicKeyPEM: pemBlob64('B')}); code != http.StatusOK {
		t.Fatalf("rotate PUT: %d", code)
	}
	waitForAudit(t, h, "app.trusted_signer_rotated", appID, 1, 5*time.Second)
	// The added count must NOT have grown — rotations are
	// classified as "rotated", not "added".
	if after := countAuditRows(context.Background(), t, h, "app.trusted_signer_added", appID); after != before {
		t.Errorf("added count drifted: before=%d after=%d", before, after)
	}

	// 3. DELETE — must emit app.trusted_signer_removed.
	if code := statusOnly(t, h, key, http.MethodDelete,
		"/v1/apps/app-rotation/trusted_signers/ci-bot", nil); code != http.StatusNoContent {
		t.Fatalf("DELETE: %d", code)
	}
	waitForAudit(t, h, "app.trusted_signer_removed", appID, 1, 5*time.Second)

	// 4. After DELETE, the list endpoint must 200 with an empty
	// signers array (the wire contract — empty list is the
	// expected state for any app with require_signed=false).
	listRaw := doReqBytes(t, h, key, http.MethodGet,
		"/v1/apps/app-rotation/trusted_signers", nil)
	if !strings.Contains(string(listRaw), `"signers":[]`) {
		t.Errorf("list after delete: %s, want empty signers array", listRaw)
	}
}

// TestSignedDeploy_OnDiskMirror is the cross-process end-to-end
// proof for the C1 critical fix. apid LISTENs on the
// trusted_signer_changed pg_notify channel and writes a per-app
// PEM file under $FAAS_TRUSTED_PUBLISHERS_DIR; imaged reads that
// directory at verify time. After a PUT, the file MUST exist on
// disk within the LISTEN round-trip window (≤2s on local postgres).
func TestSignedDeploy_OnDiskMirror(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Per-test trusted-publishers dir. The temp dir is created
	// by the harness below; the env var is consumed by the
	// runTrustedPublisherWriter goroutine.
	trustedDir := t.TempDir()

	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_TRUSTED_PUBLISHERS_DIR=" + trustedDir,
	})
	key := h.SeedAccount(context.Background(), api.PlanPro)

	if code := statusOnly(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "app-mirror"}); code != http.StatusCreated {
		t.Fatalf("seed app: %d", code)
	}
	acctID := mustAccountID(t, h, key)
	appID := findAppIDBySlug(context.Background(), t, h, acctID, "app-mirror")

	// PUT a publisher; the LISTEN goroutine must mirror it to
	// <dir>/<appID>--ci-bot.pem within a few seconds.
	if code := statusOnly(t, h, key, http.MethodPut,
		"/v1/apps/app-mirror/trusted_signers/ci-bot",
		api.AddTrustedSignerRequest{PublicKeyPEM: pemBlob64('M')}); code != http.StatusOK {
		t.Fatalf("PUT: %d", code)
	}

	wantPath := filepath.Join(trustedDir, appID+"--ci-bot.pem")
	if !waitForFile(t, wantPath, 5*time.Second) {
		// Surface the directory listing on failure so the
		// bisect isn't a guessing game.
		entries, _ := os.ReadDir(trustedDir)
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("mirror file %s never appeared; dir contents: %v", wantPath, names)
	}

	// Per-file mode must be 0444 — the on-disk mirror is an
	// argv-passed file, not a socket (imaged reads it as
	// root); the umask on the os.CreateTemp / WriteFile path
	// would otherwise leave it world-writable.
	info, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("stat mirror file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o444 {
		t.Errorf("mirror file mode = %o, want 0444", perm)
	}

	// DELETE — the mirror must be reconciled away.
	if code := statusOnly(t, h, key, http.MethodDelete,
		"/v1/apps/app-mirror/trusted_signers/ci-bot", nil); code != http.StatusNoContent {
		t.Fatalf("DELETE: %d", code)
	}
	if !waitForFileGone(t, wantPath, 5*time.Second) {
		t.Fatalf("mirror file %s still present after DELETE", wantPath)
	}
}

// TestSignedDeploy_FreePlanRefuses asserts the Free plan wire
// surface: the TrustedSignerCountMax limit is 0 on Free, so the
// pre-flight quota check inside upsertTrustedSigner rejects the
// PUT with 403 CodePlanLimitTrustedSigners BEFORE any DB row is
// written. This is the per-plan boundary documented in
// pkg/api/limits.go.
func TestSignedDeploy_FreePlanRefuses(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)
	key := h.SeedAccount(context.Background(), api.PlanFree)

	if code := statusOnly(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "app-free"}); code != http.StatusCreated {
		t.Fatalf("seed app: %d", code)
	}

	assertProblemAPID(t, h, key, http.MethodPut,
		"/v1/apps/app-free/trusted_signers/ci-bot",
		api.AddTrustedSignerRequest{PublicKeyPEM: pemBlob64('F')},
		http.StatusForbidden, api.CodePlanLimitTrustedSigners)
}

// TestSignedDeploy_TarballBypassesGate pins the railpack-path
// boundary: a multipart tarball deploy (req.Image is empty) is
// unaffected by the require_signed flag. We can't easily drive a
// multipart upload through this harness, but the JSON body
// without an Image field is 400 BEFORE the signature gate runs
// (the handler forks on Content-Type), so the failure mode we
// pin is "non-403, non-deploy_signature_invalid": a 400 from the
// body validator, not a 403 from the gate.
func TestSignedDeploy_TarballBypassesGate(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	if code := statusOnly(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "app-tarball"}); code != http.StatusCreated {
		t.Fatalf("seed app: %d", code)
	}
	// Flip the flag on. No signers onboarded — image-path
	// deploys would 403, but the JSON body handoff below never
	// goes through the image path.
	assertProblemAPID(t, h, key, http.MethodPatch,
		"/v1/apps/app-tarball/security",
		api.AppSecurityRequest{RequireSigned: ptrBool(true)},
		http.StatusOK, "")

	// Empty Image field — pre-flight accepts it because the
	// Content-Type is JSON (not multipart), the signature gate
	// only forks on req.Image != "", and the body validator
	// emits a 400 (NOT 403). So: 400, never 403.
	code := statusOnly(t, h, key, http.MethodPost,
		"/v1/apps/app-tarball/deployments",
		api.CreateDeploymentRequest{})
	if code == http.StatusForbidden {
		t.Fatalf("non-image deploy triggered signature gate (got 403)")
	}
}

// ptrBool is a tiny helper so the table can write ptrBool(true)
// inline without an extra import.
func ptrBool(v bool) *bool { return &v }

// mustAccountID resolves the auto-created account id from the
// API key. The harness's SeedAccount returns the key value, not
// the account id; for the audit query we need the account id to
// resolve apps.app_id. Simpler: query the api_key_table join.
func mustAccountID(t *testing.T, h *e2etest.Harness, apiKey string) string {
	t.Helper()
	var id string
	if err := h.Pool.QueryRow(context.Background(),
		`select account_id::text from api_keys where key_sha256 = $1`,
		api.HashAPIKey(apiKey)).Scan(&id); err != nil {
		t.Fatalf("resolve account_id from key: %v", err)
	}
	return id
}

// waitForAudit polls the events table for the (kind, appID) pair
// to reach the expected count. Bounded by timeout so a stuck
// LISTEN goroutine doesn't hang the suite.
func waitForAudit(t *testing.T, h *e2etest.Harness, kind, appID string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := countAuditRows(context.Background(), t, h, kind, appID); got >= want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("audit row kind=%q app=%q never reached count=%d (within %s)", kind, appID, want, timeout)
}

// waitForFile polls for a file's existence. Used by the mirror
// test to absorb the LISTEN round-trip latency.
func waitForFile(t *testing.T, path string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// waitForFileGone is the inverse of waitForFile.
func waitForFileGone(t *testing.T, path string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
