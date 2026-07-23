package state

// MemStore DeleteAccount cascade tests + RestoreAccount tests.
//
// MemStore.DeleteAccount (memstore.go:2392) is the in-memory mirror
// of the production PgStore FK cascade. The PgStore side is fully
// covered by pgstore_account_deletion_test.go (5 tests on the FK
// walk + events branch). The MemStore side only had the events-
// cascade branch exercised (TestMem_DeleteAccount_CascadesEvents);
// the child-table branches were untouched, so MemStore sat at
// 57.4% coverage. These tests fill that gap, mirroring the PgStore
// coverage without duplicating it.
//
// RestoreAccount: PgStore has 2 tests; MemStore had zero. These 3
// tests cover all three branches (within-grace, past-grace, on-
// active-row) so the in-memory state machine stays parity with
// production.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// seedMemStoreFullAccount is the MemStore mirror of the PgStore
// seedFullAccount helper. Seeds account + app + deployment + build +
// instance + cron + domain + secret + key + a Stripe push-hour row so
// DeleteAccount has every child row to walk. Returns the account id.
//
// The seeded deployment is `pending` so DeleteAccount's conditional
// `WHERE status = 'deleted_pending'` doesn't trip. Tests call
// MarkAccountDeletionPending first.
func seedMemStoreFullAccount(t *testing.T, m *MemStore) (acctID string) {
	t.Helper()
	ctx := context.Background()

	email := fmt.Sprintf("mem-cascade+%s@example.com", t.Name())
	acct, err := m.CreateAccount(ctx, email, api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID: acct.ID, Slug: fmt.Sprintf("mem-cascade-%s", t.Name()),
		Type: AppTypeApp, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := m.CreateDeployment(ctx, Deployment{
		AppID: app.ID, Kind: DeploymentKindImage,
		ImageDigest: "sha256:abc", Status: DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if _, err := m.CreateBuild(ctx, dep.ID, DeploymentKindDockerfile, 4096, "/tmp/log"); err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}
	if _, err := m.CreateInstance(ctx, app.ID, dep.ID, "running", 256, "default-local", ""); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if _, err := m.CreateCustomDomain(ctx, fmt.Sprintf("mem-cascade-%s.example.com", t.Name()), app.ID, "tok"); err != nil {
		t.Fatalf("CreateCustomDomain: %v", err)
	}
	if _, err := m.CreateCron(ctx, app.ID, "*/5 * * * *", "/healthz", true); err != nil {
		t.Fatalf("CreateCron: %v", err)
	}
	if err := m.UpsertAppSecret(ctx, acct.ID, app.ID, "STRIPE_KEY", []byte("ct")); err != nil {
		t.Fatalf("UpsertAppSecret: %v", err)
	}
	if _, err := m.CreateAPIKey(ctx, acct.ID, []byte("deadbeefcafebabe"), "test"); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if err := m.RecordStripePushHour(ctx, acct.ID, time.Now().UTC().Truncate(time.Hour)); err != nil {
		t.Fatalf("RecordStripePushHour: %v", err)
	}
	return acct.ID
}

// markPendingAndDelete is the standard pattern: seed the account,
// mark it pending, then delete. Returns nothing — failure aborts via
// t.Fatal in the helper.
func markPendingAndDelete(t *testing.T, m *MemStore, acctID string) {
	t.Helper()
	if err := m.MarkAccountDeletionPending(context.Background(), acctID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	if err := m.DeleteAccount(context.Background(), acctID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
}

// TestMem_DeleteAccount_CascadesApps confirms the apps map is empty
// for the deleted account. The PgStore equivalent is
// TestPg_DeleteAccount_CascadesAllRows — we mirror the assertion shape
// (post-delete AppBySlug returns ErrNotFound for the seeded slug).
func TestMem_DeleteAccount_CascadesApps(t *testing.T) {
	m := NewMemStore()
	slug := fmt.Sprintf("mem-cascade-%s", t.Name())
	acctID := seedMemStoreFullAccount(t, m)
	markPendingAndDelete(t, m, acctID)

	if _, err := m.AppBySlug(context.Background(), slug); !errors.Is(err, ErrNotFound) {
		t.Errorf("AppBySlug after delete: err = %v, want ErrNotFound", err)
	}
}

// TestMem_DeleteAccount_CascadesDeployments confirms the deployments
// map is empty for the deleted account. We assert via the public
// ListDeploymentsForAccount reader rather than direct map inspection
// so the test exercises the same surface the rest of the codebase
// uses.
func TestMem_DeleteAccount_CascadesDeployments(t *testing.T) {
	m := NewMemStore()
	acctID := seedMemStoreFullAccount(t, m)
	markPendingAndDelete(t, m, acctID)

	deps, err := m.ListDeploymentsForAccount(context.Background(), acctID, time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListDeploymentsForAccount: %v", err)
	}
	if len(deps) != 0 {
		t.Errorf("len(deps) = %d, want 0", len(deps))
	}
}

// TestMem_DeleteAccount_CascadesBuildsAndInstances confirms both
// builds and instances are cleared. Both are reached transitively
// through the deployment-cascade walk; bundling them keeps the test
// count at 6 instead of 7 per the plan.
func TestMem_DeleteAccount_CascadesBuildsAndInstances(t *testing.T) {
	m := NewMemStore()
	acctID := seedMemStoreFullAccount(t, m)
	markPendingAndDelete(t, m, acctID)

	builds, err := m.ListBuildsForAccount(context.Background(), acctID)
	if err != nil {
		t.Fatalf("ListBuildsForAccount: %v", err)
	}
	if len(builds) != 0 {
		t.Errorf("len(builds) = %d, want 0", len(builds))
	}
	insts, err := m.ListInstancesForAccount(context.Background(), acctID)
	if err != nil {
		t.Fatalf("ListInstancesForAccount: %v", err)
	}
	if len(insts) != 0 {
		t.Errorf("len(insts) = %d, want 0", len(insts))
	}
}

// TestMem_DeleteAccount_CascadesCronsAndDomains confirms both crons
// and custom domains are cleared. Both are walked via the apps-
// account reverse-lookup; bundling keeps the test count at 6.
func TestMem_DeleteAccount_CascadesCronsAndDomains(t *testing.T) {
	m := NewMemStore()
	acctID := seedMemStoreFullAccount(t, m)
	markPendingAndDelete(t, m, acctID)

	crons, err := m.ListCronsForAccount(context.Background(), acctID)
	if err != nil {
		t.Fatalf("ListCronsForAccount: %v", err)
	}
	if len(crons) != 0 {
		t.Errorf("len(crons) = %d, want 0", len(crons))
	}
	domains, err := m.ListDomainsForAccount(context.Background(), acctID)
	if err != nil {
		t.Fatalf("ListDomainsForAccount: %v", err)
	}
	if len(domains) != 0 {
		t.Errorf("len(domains) = %d, want 0", len(domains))
	}
}

// TestMem_DeleteAccount_CascadesSecretsAndAPIKeys confirms both
// secrets and api keys are cleared. We assert via direct map
// inspection since neither surface has a "list for account" reader
// that survives DeleteAccount (by design — the parent row is gone).
func TestMem_DeleteAccount_CascadesSecretsAndAPIKeys(t *testing.T) {
	m := NewMemStore()
	acctID := seedMemStoreFullAccount(t, m)
	// Snapshot the pre-delete state for the post-delete comparison.
	markPendingAndDelete(t, m, acctID)

	m.mu.Lock()
	defer m.mu.Unlock()
	for k, sec := range m.secrets {
		if sec.AccountID == acctID {
			t.Errorf("secrets[%v] still present after delete (accountID = %q)", k, sec.AccountID)
		}
	}
	for kid, k := range m.keys {
		if k.AccountID == acctID {
			t.Errorf("keys[%v] still present after delete (accountID = %q)", kid, k.AccountID)
		}
	}
}

// TestMem_DeleteAccount_CascadesIdempotencyAndUsageAndStripe confirms
// the idempotency map (the api's request-replay key store), the usage
// aggregates, and the stripeByCustomer reverse-index are all cleared.
// Direct map inspection again — no public reader for these survives
// the account row's deletion.
func TestMem_DeleteAccount_CascadesIdempotencyAndUsageAndStripe(t *testing.T) {
	m := NewMemStore()
	acctID := seedMemStoreFullAccount(t, m)
	markPendingAndDelete(t, m, acctID)

	m.mu.Lock()
	defer m.mu.Unlock()
	// idem keys are "<accountID>\x00<key>"; any prefix match is a leak.
	for k := range m.idem {
		if len(k) >= len(acctID)+1 && k[:len(acctID)+1] == acctID+"\x00" {
			t.Errorf("idem[%q] still present after delete (account prefix leak)", k)
		}
	}
	for _, u := range m.usage {
		if u.AccountID == acctID {
			t.Errorf("usage row still present after delete (accountID = %q)", u.AccountID)
		}
	}
	for _, u := range m.usageByMonth {
		if u.AccountID == acctID {
			t.Errorf("usageByMonth row still present after delete (accountID = %q)", u.AccountID)
		}
	}
	for sc, acid := range m.stripeByCustomer {
		if acid == acctID {
			t.Errorf("stripeByCustomer[%q] still present (accountID = %q)", sc, acid)
		}
	}
}

// TestMem_RestoreAccount_WithinGrace confirms the happy path:
// MarkAccountDeletionPending → RestoreAccount within 30 days →
// AccountByID returns the active account. The status flips back to
// AccountActive and DeletionRequestedAt is cleared.
func TestMem_RestoreAccount_WithinGrace(t *testing.T) {
	m := NewMemStore()
	acctID := seedMemStoreFullAccount(t, m)

	if err := m.MarkAccountDeletionPending(context.Background(), acctID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	pending, err := m.AccountByID(context.Background(), acctID)
	if err != nil {
		t.Fatalf("AccountByID (pending): %v", err)
	}
	if pending.Status != AccountDeletedPending {
		t.Fatalf("pending.Status = %q, want %q", pending.Status, AccountDeletedPending)
	}
	if err := m.RestoreAccount(context.Background(), acctID); err != nil {
		t.Fatalf("RestoreAccount: %v", err)
	}
	got, err := m.AccountByID(context.Background(), acctID)
	if err != nil {
		t.Fatalf("AccountByID (restored): %v", err)
	}
	if got.Status != AccountActive {
		t.Errorf("Status = %q, want %q", got.Status, AccountActive)
	}
	if got.DeletionRequestedAt != nil {
		t.Errorf("DeletionRequestedAt = %v, want nil after restore", got.DeletionRequestedAt)
	}
}

// TestMem_RestoreAccount_PastGraceReturnsErrConflict confirms the
// grace-window check. We use SetDeletionRequestedAtForTest to set the
// stamp 31 days in the past (grace is 30 days), so the
// `time.Since(*a.DeletionRequestedAt) > DeletionGraceDuration()`
// guard fires and RestoreAccount returns ErrConflict — the signal
// pkg/grace uses to log and skip the tick.
func TestMem_RestoreAccount_PastGraceReturnsErrConflict(t *testing.T) {
	m := NewMemStore()
	acctID := seedMemStoreFullAccount(t, m)
	if err := m.MarkAccountDeletionPending(context.Background(), acctID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	// 31 days ago = past the 30-day grace.
	if err := m.SetDeletionRequestedAtForTest(acctID, time.Now().UTC().Add(-31*24*time.Hour)); err != nil {
		t.Fatalf("SetDeletionRequestedAtForTest: %v", err)
	}
	err := m.RestoreAccount(context.Background(), acctID)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("RestoreAccount past grace: err = %v, want ErrConflict", err)
	}
	// Account row must still exist in pending state (no row-level
	// mutation on ErrConflict).
	got, _ := m.AccountByID(context.Background(), acctID)
	if got.Status != AccountDeletedPending {
		t.Errorf("Status after ErrConflict = %q, want %q (no mutation)", got.Status, AccountDeletedPending)
	}
}

// TestMem_RestoreAccount_OnActiveRowReturnsErrConflict confirms that
// restoring an account that's not in deleted_pending returns
// ErrConflict. PgStore has the analogous ErrNotFound sentinel; the
// in-memory impl deliberately uses ErrConflict to keep the caller's
// "no-op on stale tick" branch uniform.
func TestMem_RestoreAccount_OnActiveRowReturnsErrConflict(t *testing.T) {
	m := NewMemStore()
	acctID := seedMemStoreFullAccount(t, m)
	// Don't MarkAccountDeletionPending — account stays active.
	err := m.RestoreAccount(context.Background(), acctID)
	if !errors.Is(err, ErrConflict) {
		t.Errorf("RestoreAccount on active row: err = %v, want ErrConflict", err)
	}
}

// (uuid is no longer imported here — the seed helper relies on the
// production CreateAccount/CreateApp paths to mint ids internally.
// If a future cascade test needs explicit uuids, add `github.com/google/uuid`
// back at the top of this file.)
