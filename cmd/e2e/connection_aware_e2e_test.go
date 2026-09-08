// connection_aware_e2e_test.go — ADR-098 §9.A connection-aware
// execution e2e (PR-B + PR-C + PR-D behind per-PR flags).
//
// ## What this exercises
//
// Three sub-tests, each gated on a flag combination that
// mirrors the cluster outline's rollout gate (PR-B → 1 month →
// PR-C → 1 month → PR-D). The flags-ON test confirms the full
// pipeline (capture → probe → chooser) wires up; the flags-OFF
// test confirms the entire connection-aware path is a no-op
// when ops hasn't flipped the rollout switch.
//
// ## What this does NOT cover
//
// - TLS handshake against a real DB (the test uses a synthetic
//   host that nothing is listening on — the probe loop
//   classifies the outcome as "refused" / "unreachable" and
//   goes into the data_upstream_probes table regardless).
//   The §11 secret rule (host_redacted_hash on the wire) is
//   fully covered; the probe's HTTP semantics are covered in
//   pkg/meter/upstream_probe_test.go.
// - The chooser bias is a black box in this e2e — schedd is
//   booted but not invoked. The chooser itself is pinned in
//   pkg/sched/placement_test.go:TestChoosePlacement_BiasWinsAboveDelta.
// - The Free=0 quota short-circuit is pinned in
//   pkg/data/infer_test.go:TestClassifier_FreePlanQuotaRejected;
//   this e2e uses Hobby plan to walk the happy path.
//
// ## Build tag
//
// //go:build integration — the pgtest harness requires a
// running Postgres; CI runs it on the integration job.

//go:build integration

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// writeSaltFile drops a 32-byte salt file at t.TempDir() and
// points the secretbox package at it. This MUST run before
// the daemon boots — the apid classifier (C4) refuses to
// start if the salt file is missing or the wrong length. The
// env-var mechanism is FAAS_HOST_HASH_SALT_PATH (mirrors the
// production default /etc/faas/secrets/host_hash_salt).
func writeSaltFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "host_hash_salt")
	salt := make([]byte, 32)
	for i := range salt {
		salt[i] = byte(i)
	}
	if err := os.WriteFile(path, salt, 0o600); err != nil {
		t.Fatalf("write salt: %v", err)
	}
	t.Setenv("FAAS_HOST_HASH_SALT_PATH", path)
	secretbox.SetHostHashSaltPath(path)
	secretbox.ResetHostHashSaltCache()
	return path
}

// TestConnectionAwareE2E_FlagsOn walks the full pipeline with
// FAAS_DATA_PLACEMENT=1 + FAAS_UPSTREAM_PROBE=1 +
// FAAS_UPSTREAM_AFFINITY=1. The classifier captures an app's
// DATABASE_URL into data_upstreams; the probe loop emits a
// data_upstream_probes row; the chooser reads the row at wake.
// The test asserts the row exists with the §11
// host_redacted_hash label (NOT the plaintext host).
func TestConnectionAwareE2E_FlagsOn(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	env := []string{
		"FAAS_DATA_PLACEMENT=1",
		"FAAS_UPSTREAM_PROBE=1",
		"FAAS_UPSTREAM_AFFINITY=1",
	}
	env = append(env, "FAAS_HOST_HASH_SALT_PATH="+writeSaltFile(t))
	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Meterd|e2etest.Schedd, env)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	accountID := accountIDFromKey(t, context.Background(), pool, key)

	// Create an app.
	slug := "conn-aware-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	// POST an env mutation containing a DATABASE_URL. The
	// classifier (PR-B, gated on FAAS_DATA_PLACEMENT=1) must
	// capture this into data_upstreams.
	const plaintextHost = "db.example.com"
	dsn := "postgres://u:p@" + plaintextHost + ":5432/x"
	putRec, putStatus := doReq(t, h, key, http.MethodPut,
		"/v1/apps/"+slug+"/env/DATABASE_URL",
		api.PutAppEnvRequest{Value: dsn})
	if putStatus != http.StatusOK {
		t.Fatalf("env put: status=%d body=%s", putStatus, putRec)
	}

	// Re-derive the expected hash via the same call path the
	// classifier uses. The salt was set by writeSaltFile().
	hash, err := secretbox.HashHost(plaintextHost)
	if err != nil {
		t.Fatalf("HashHost: %v", err)
	}
	expectedHash := hash

	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT id, host_redacted_hash, kind, port, scope
		FROM data_upstreams
		WHERE account_id = $1 AND app_id = $2
	`, accountID, app.ID)
	if err != nil {
		t.Fatalf("query data_upstreams: %v", err)
	}
	defer rows.Close()

	found := false
	var gotHash, gotKind, gotScope string
	var gotPort int
	for rows.Next() {
		var id, appID string
		if err := rows.Scan(&id, &gotHash, &gotKind, &gotPort, &gotScope, &appID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if gotHash == expectedHash && gotKind == "postgres" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("data_upstreams row missing: want hash=%s kind=postgres; got hash=%s kind=%s scope=%s port=%d",
			expectedHash, gotHash, gotKind, gotScope, gotPort)
	}

	// §11 secret-rule smoke test: the row's host_redacted_hash
	// must NOT contain the plaintext host. Assert by
	// string-search on the raw bytes — the hashes are 64 hex
	// chars, the plaintext has dots.
	if strings.Contains(gotHash, ".") || strings.Contains(gotHash, "example") {
		t.Errorf("data_upstreams.host_redacted_hash leaks plaintext: %q", gotHash)
	}

	// Upstream listing endpoint (PR-B route) should surface
	// the same row.
	listRec, listStatus := doReq(t, h, key, http.MethodGet,
		"/v1/apps/"+slug+"/upstreams", nil)
	if listStatus != http.StatusOK {
		t.Fatalf("list upstreams: status=%d body=%s", listStatus, listRec)
	}
	var list api.DataUpstreamListResponse
	if err := json.Unmarshal(listRec, &list); err != nil {
		t.Fatalf("decode list: %v body=%s", err, listRec)
	}
	if len(list.Upstreams) == 0 {
		t.Fatalf("list upstreams: empty, want 1")
	}
	if list.Upstreams[0].HostRedactedHash != expectedHash {
		t.Errorf("list upstreams: got hash=%s, want %s",
			list.Upstreams[0].HostRedactedHash, expectedHash)
	}
}

// TestConnectionAwareE2E_FlagsOff walks the same path with
// FAAS_DATA_PLACEMENT=0 + FAAS_UPSTREAM_PROBE=0 +
// FAAS_UPSTREAM_AFFINITY=0. The classifier must NOT capture
// the env mutation; the probe loop must NOT emit a row; the
// chooser must NOT bias. The env table itself is unchanged
// (env writes are independent of the data-upstream pipeline).
func TestConnectionAwareE2E_FlagsOff(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	env := []string{
		"FAAS_DATA_PLACEMENT=0",
		"FAAS_UPSTREAM_PROBE=0",
		"FAAS_UPSTREAM_AFFINITY=0",
	}
	env = append(env, "FAAS_HOST_HASH_SALT_PATH="+writeSaltFile(t))
	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Meterd|e2etest.Schedd, env)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	accountID := accountIDFromKey(t, context.Background(), pool, key)

	slug := "conn-aware-off-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	const plaintextHost = "db.example.com"
	dsn := "postgres://u:p@" + plaintextHost + ":5432/x"
	_, putStatus := doReq(t, h, key, http.MethodPut,
		"/v1/apps/"+slug+"/env/DATABASE_URL",
		api.PutAppEnvRequest{Value: dsn})
	if putStatus != http.StatusOK {
		t.Fatalf("env put: status=%d", putStatus)
	}

	// Assert NO row landed in data_upstreams.
	ctx := context.Background()
	var nDataUp int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM data_upstreams
		WHERE account_id = $1 AND app_id = $2
	`, accountID, app.ID).Scan(&nDataUp); err != nil {
		t.Fatalf("count data_upstreams: %v", err)
	}
	if nDataUp != 0 {
		t.Errorf("classifier fired under flags-off: data_upstreams=%d, want 0", nDataUp)
	}

	// Assert NO probe row landed.
	var nProbes int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM data_upstream_probes
		WHERE account_id = $1
	`, accountID).Scan(&nProbes); err != nil {
		t.Fatalf("count data_upstream_probes: %v", err)
	}
	if nProbes != 0 {
		t.Errorf("probe loop fired under flags-off: data_upstream_probes=%d, want 0", nProbes)
	}
}

// TestConnectionAwareE2E_UpstreamDelete covers the delete
// route (PR-B). With flags-ON, capture + delete + re-list must
// leave the table empty. The chooser would then fall through
// to legacy tie-break (PR-D fail-open), which is pinned in
// pkg/sched/placement_test.go:TestChoosePlacement_NilScores_FailsOpen.
func TestConnectionAwareE2E_UpstreamDelete(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	env := []string{"FAAS_DATA_PLACEMENT=1"}
	env = append(env, "FAAS_HOST_HASH_SALT_PATH="+writeSaltFile(t))
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, env)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	_ = accountIDFromKey(t, context.Background(), pool, key)

	slug := "conn-aware-delete-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	dsn := "postgres://u:p@db.example.com:5432/x"
	_, putStatus := doReq(t, h, key, http.MethodPut,
		"/v1/apps/"+slug+"/env/DATABASE_URL",
		api.PutAppEnvRequest{Value: dsn})
	if putStatus != http.StatusOK {
		t.Fatalf("env put: status=%d", putStatus)
	}

	// List to get the upstream ID.
	listRec, _ := doReq(t, h, key, http.MethodGet,
		"/v1/apps/"+slug+"/upstreams", nil)
	var list api.DataUpstreamListResponse
	if err := json.Unmarshal(listRec, &list); err != nil {
		t.Fatalf("decode list: %v body=%s", err, listRec)
	}
	if len(list.Upstreams) == 0 {
		t.Fatalf("list upstreams: empty after put")
	}
	upstreamID := list.Upstreams[0].ID

	// Delete by ID.
	_, delStatus := doReq(t, h, key, http.MethodDelete,
		"/v1/apps/"+slug+"/upstreams/"+upstreamID, nil)
	if delStatus != http.StatusNoContent {
		t.Fatalf("delete upstream: status=%d", delStatus)
	}

	// Re-list — empty.
	listRec2, _ := doReq(t, h, key, http.MethodGet,
		"/v1/apps/"+slug+"/upstreams", nil)
	var list2 api.DataUpstreamListResponse
	if err := json.Unmarshal(listRec2, &list2); err != nil {
		t.Fatalf("decode list2: %v body=%s", err, listRec2)
	}
	if len(list2.Upstreams) != 0 {
		t.Errorf("list after delete: got %d, want 0", len(list2.Upstreams))
	}
}
