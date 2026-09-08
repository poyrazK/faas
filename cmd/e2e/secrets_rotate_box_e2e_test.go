// secrets_rotate_box_e2e_test.go — ADR-089 PR-C full-lifecycle e2e.
//
// Scenario (mirrors the production operator workflow from
// docs/ops/host-age-rotation.md):
//
//  1. Boot apid with identity A. Seed N customer accounts and
//     PUT M secrets each. Every row lands on disk with
//     kid = fingerprint(A).
//
//  2. Stop apid (t.Cleanup fires when the test returns; the
//     first harness's daemons are torn down on the second
//     StartWithEnv call's lifecycle).
//
//  3. Boot apid with identity B (a freshly generated age X25519
//     key) AND FAAS_REKEY_ENABLED=true AND
//     FAAS_REKEY_PROGRESS_FILE=<tmpdir>/rekey.json. The
//     runner sees rows whose kid = A != B and re-seals them
//     under B.
//
//  4. Poll the on-disk progress file until rekeyed reaches the
//     seeded count and failed == 0. The file is the test seam;
//     the admin endpoint requires the operator allowlist which
//     the harness doesn't pin by default (the wire shape of
//     the disabled-path is exercised by
//     secrets_rotate_e2e_test.go::TestRekeyProgressDisabledPg).
//
// Crash-safe restart is pinned at the unit level in
// cmd/apid/rekey_runner_test.go::TestRunner_LoadsExistingProgress.
// The box test exercises the full walk + on-disk persistence
// path end-to-end.
//
// Build tag: (none). CI-safe. Requires Postgres
// (skip via FAAS_SKIP_PG_TESTS).

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/rekey"
)

// TestRekeyRunnerPg walks the full PR-C lifecycle.
//
// The test is intentionally small (3 accounts × 2 secrets = 6
// rows) so it runs in seconds — the runner paces itself at
// 100 rows/sec by default, and 6 rows finishes inside one
// batch (BatchSize=50) and a single progress tick.
func TestRekeyRunnerPg(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Phase 1: identity A — seed the customer table.
	recipientA, identityA := startHostedRecipient(t)
	h1 := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + recipientA,
		"FAAS_HOST_AGE_IDENTITY_PATH=" + identityA,
	})
	// Capture A's key bytes so phase 2 can stage identity A as
	// host.age.previous next to identity B's host.age — apid's
	// LoadHostKeys(dir) picks up both and feeds the slice to
	// OpenMulti, which is the only way the rekey runner can
	// unseal envelopes sealed under the previous key. Without
	// this, every unseal attempt fails with "no key matched"
	// and the runner reports Failed=N rows.
	identityABytes, err := os.ReadFile(identityA)
	if err != nil {
		t.Fatalf("read identity A: %v", err)
	}
	const (
		numAccounts    = 3
		secretsPerAcct = 2
		totalSecrets   = numAccounts * secretsPerAcct
	)
	for i := 0; i < numAccounts; i++ {
		key := h1.SeedAccount(context.Background(), api.PlanHobby, "rekey-"+string(rune('a'+i)))
		slug := "rekey-app-" + string(rune('a'+i))
		if code := statusOnly(t, h1, key, http.MethodPost, "/v1/apps",
			api.CreateAppRequest{Slug: slug}); code != http.StatusCreated {
			t.Fatalf("phase1: create %s: %d", slug, code)
		}
		for j := 0; j < secretsPerAcct; j++ {
			secretKey := keyNameForQuota(j)
			if code := statusOnly(t, h1, key, http.MethodPut,
				"/v1/apps/"+slug+"/secrets/"+secretKey,
				api.PutAppSecretRequest{Value: "v1-" + secretKey}); code != http.StatusOK {
				t.Fatalf("phase1: PUT %s/%s: %d", slug, secretKey, code)
			}
		}
	}

	// Phase 2: identity B — the runner re-seals under the new
	// identity.
	//
	// ADR-094 / L2 fix (PR-823): we DO stop h1 explicitly here,
	// before phase 2 boots. Phase 1's apid holds a MaxConns=8 pool
	// + 4+ background goroutines (rekey walker, sseFanIn, audit
	// subscriber, grace sweep) each holding a connection on the
	// shared Postgres service container. With phase 1 still alive
	// when phase 2 boots, the two daemons compete for the
	// service-container max_connections=100 budget; the first
	// pgxpool.Acquire call inside phase 2's bgBefore goroutines
	// can race with phase 1's rekey walker's connection
	// churn and surface as "closed pool" / "context canceled"
	// inside phase 2's listener bind path. Stopping phase 1
	// before phase 2 boots removes that contention.
	//
	// Harness.Stop (pkg/e2etest/harness.go) signals the daemon
	// subprocess (SIGTERM → 5s grace → SIGKILL) but does NOT
	// close the test pool — the pgtest pool survives both
	// phases and is closed only by the LIFO t.Cleanup at test
	// exit. Safe to call h1.Stop() here.
	h1.Stop()
	recipientB, identityB := startHostedRecipient(t)
	// Stage A as host.age.previous in identity B's dir so
	// apid's LoadHostKeys picks it up — see phase-1 comment.
	previousPath := filepath.Join(filepath.Dir(identityB), "host.age.previous")
	if err := os.WriteFile(previousPath, identityABytes, 0o400); err != nil {
		t.Fatalf("stage identity A as previous: %v", err)
	}
	progressFile := filepath.Join(t.TempDir(), "rekey-progress.json")
	_ = e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + recipientB,
		"FAAS_HOST_AGE_IDENTITY_PATH=" + identityB,
		"FAAS_REKEY_ENABLED=true",
		"FAAS_REKEY_PROGRESS_FILE=" + progressFile,
	})

	// Poll the on-disk progress file. The runner ticks per
	// batch; with BatchSize=50 and 6 rows total we expect one
	// tick then a "complete" tick (total == 6, no fresh rows).
	wantTotal := totalSecrets
	deadline := time.Now().Add(30 * time.Second)
	var prog rekey.RekeyProgress
	for {
		if time.Now().After(deadline) {
			t.Fatalf("rekey runner did not drain in 30s: progress=%+v", prog)
		}
		data, err := os.ReadFile(progressFile)
		if err == nil {
			if err := json.Unmarshal(data, &prog); err == nil {
				if prog.Total >= wantTotal && prog.Failed == 0 {
					break
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Final assertions.
	if prog.Rekeyed+prog.Skipped < wantTotal {
		t.Errorf("rekeyed(%d)+skipped(%d) < total(%d) — runner undercounted",
			prog.Rekeyed, prog.Skipped, wantTotal)
	}
	if prog.Failed != 0 {
		t.Errorf("rekey failures: %d (expected 0; rows were sealed under identity A, runner unsealed under A and re-sealed under B)",
			prog.Failed)
	}
}

// TestRekeyRunnerPg_Phase2Isolated is the L2 sibling test that
// pins the h1.stop() between phases. It runs the same phase-1 →
// phase-2 lifecycle as TestRekeyRunnerPg but in a stripped-down
// shape that only exercises the listener-bind path (no actual
// secrets, no rekey progress assertions) — the original flake
// was on phase 2's listener bind timing out at
// `127.0.0.1:<port> did not accept within 10s`, which is the
// pool-starvation race the L2 fix targets. This sibling is
// deliberately fast (no progress-file polling) so it can run as
// a CI smoke test multiple times without slowing the shard.
//
// The test's only assertion is that phase 2's StartWithEnv
// succeeds deterministically — i.e. the listener bind returns
// inside the harness's waitTCP deadline. If h1.stop() ever stops
// being called between phases, this test will flake the same
// way TestRekeyRunnerPg did pre-fix.
func TestRekeyRunnerPg_Phase2Isolated(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Phase 1: minimal boot — no secrets seeded, just confirm
	// the daemon reaches the listener.
	recipientA, identityA := startHostedRecipient(t)
	h1 := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + recipientA,
		"FAAS_HOST_AGE_IDENTITY_PATH=" + identityA,
	})

	// Stop phase 1 before phase 2 boots (ADR-094 / L2 fix).
	h1.Stop()

	// Phase 2: boot a fresh apid with a different identity.
	// StartWithEnv's waitTCP returns when the listener accepts
	// TCP connections; if h1.stop() was missing, this is where
	// the 10s deadline would fire (pre-fix flake signature).
	recipientB, identityB := startHostedRecipient(t)
	h2 := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + recipientB,
		"FAAS_HOST_AGE_IDENTITY_PATH=" + identityB,
	})
	// h2 is wired through the harness's t.Cleanup — no manual
	// stop here. The test's only assertion is "phase 2 reached
	// the listener-bind path"; if StartWithEnv returned without
	// fail, the listener is up.
	_ = h2
}
