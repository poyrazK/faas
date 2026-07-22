package state_test

// G6 round-trips for the six new Store methods (spec §17 G6, ADR-021).
//
// Mirrors the style of pgstore_test.go (one seedLiveDeploy helper, one
// focused assertion per subtest). Uses pgtest.Open so the whole file
// skips when Postgres is unreachable — no `make test` regression in
// environments without a running cluster.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgPoolFromStore recovers the test pool for the few cases that need
// raw SQL (fast-forwarding deletion_requested_at past the 30-day
// guard). PgStore doesn't expose its pool — by design, since the rest
// of the codebase must not reach around the Store interface. Tests
// re-open it via pgtest via a lookup that the package-internal
// `pgStore(t)` helper has already called; the simplest path is to
// have pgStore return the pool alongside the store. See
// pgStoreAccountDeletionWithPool below.
type pgDeps struct {
	store *state.PgStore
	pool  *pgxpool.Pool
	ctx   context.Context
}

// pgStoreAccountDeletionWithPool is the pgStore variant used by the
// G6 round-trips. Same body as pgStore() but also returns the pool so
// a single test can fast-forward deletion_requested_at past the
// 30-day guard. Kept separate so the other pgstore_test.go callers
// keep their narrow (store, ctx) signature.
func pgStoreAccountDeletionWithPool(t *testing.T) pgDeps {
	t.Helper()
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pgDeps{store: state.NewPgStore(pool), pool: pool, ctx: ctx}
}

// seedFullAccount creates account + app + deployment + build + instance
// + cron + domain + secret + key so DeleteAccount has every child row
// to walk. Returns the account id.
func seedFullAccount(t *testing.T, s *state.PgStore, ctx context.Context) (string, error) {
	t.Helper()
	acctID, _, err := seedFullAccountWithDep(t, s, ctx)
	return acctID, err
}

// seedFullAccountWithDep is the seedFullAccount variant that also
// returns the seeded deployment id. Tests that need to create a new
// instance (CreateInstance requires a valid deployment_id UUID) call
// this instead.
func seedFullAccountWithDep(t *testing.T, s *state.PgStore, ctx context.Context) (string, string, error) {
	t.Helper()
	email := fmt.Sprintf("g6+%s@example.com", t.Name())
	acct, err := s.CreateAccount(ctx, email, api.PlanHobby)
	if err != nil {
		return "", "", err
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: fmt.Sprintf("g6-app-%s", t.Name()), Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		return "", "", err
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:abc", Status: state.DeployLive,
	})
	if err != nil {
		return "", "", err
	}
	if _, err := s.CreateBuild(ctx, dep.ID, state.DeploymentKindDockerfile, 4096, "/tmp/log"); err != nil {
		return "", "", err
	}
	if _, err := s.CreateInstance(ctx, app.ID, dep.ID, "running", 256, defaultLocalID); err != nil {
		return "", "", err
	}
	if _, err := s.CreateCustomDomain(ctx, fmt.Sprintf("g6-%s.example.com", t.Name()), app.ID, "tok"); err != nil {
		return "", "", err
	}
	if _, err := s.CreateCron(ctx, app.ID, "*/5 * * * *", "/healthz", true); err != nil {
		return "", "", err
	}
	if err := s.UpsertAppSecret(ctx, acct.ID, app.ID, "STRIPE_KEY", []byte("ct")); err != nil {
		return "", "", err
	}
	if _, err := s.CreateAPIKey(ctx, acct.ID, []byte("deadbeefcafebabe"), "test"); err != nil {
		return "", "", err
	}
	return acct.ID, dep.ID, nil
}

func TestPg_MarkAccountDeletionPending_Idempotent(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "idem@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := s.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
		t.Fatalf("first mark: %v", err)
	}
	first, err := s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatalf("read after first: %v", err)
	}
	if first.Status != state.AccountDeletedPending || first.DeletionRequestedAt == nil {
		t.Fatalf("first mark did not flip status/timestamp: %+v", first)
	}

	// Second call must NOT overwrite the original timestamp — that's
	// the idempotency contract the grace timer relies on.
	if err := s.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
		t.Fatalf("second mark: %v", err)
	}
	second, err := s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatalf("read after second: %v", err)
	}
	if !second.DeletionRequestedAt.Equal(*first.DeletionRequestedAt) {
		t.Errorf("idempotent re-mark changed timestamp: %v -> %v",
			first.DeletionRequestedAt, second.DeletionRequestedAt)
	}
}

func TestPg_RestoreAccount_WithinGrace(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "restore@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := s.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	if err := s.RestoreAccount(ctx, acct.ID); err != nil {
		t.Fatalf("RestoreAccount: %v", err)
	}
	got, err := s.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if got.Status != state.AccountActive {
		t.Errorf("status = %q, want active", got.Status)
	}
	if got.DeletionRequestedAt != nil {
		t.Errorf("DeletionRequestedAt should be cleared, got %v", got.DeletionRequestedAt)
	}
}

// TestPg_RestoreAccount_PastGraceReturnsConflict pushes the deletion
// timestamp back past the 30-day window so RestoreAccount's SQL guard
// (`deletion_requested_at > now() - interval '30 days'`) returns zero
// rows, and the handler surfaces ErrConflict.
func TestPg_RestoreAccount_PastGraceReturnsConflict(t *testing.T) {
	d := pgStoreAccountDeletionWithPool(t)
	s, ctx, pool := d.store, d.ctx, d.pool
	acct, err := s.CreateAccount(ctx, "stale@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := s.MarkAccountDeletionPending(ctx, acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	// Fast-forward 31 days. The guard is "now() - interval '30 days'"
	// inside the SQL, not in the Go clock — so the cleanest way to
	// simulate "the window has lapsed" is to rewind the timestamp.
	if _, err := pool.Exec(ctx,
		`update accounts set deletion_requested_at = now() - interval '31 days' where id = $1`,
		acct.ID); err != nil {
		t.Fatalf("fast-forward deletion_requested_at: %v", err)
	}
	err = s.RestoreAccount(ctx, acct.ID)
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("RestoreAccount past grace = %v, want ErrConflict", err)
	}
}

// TestPg_DeleteAccount_CascadesAllRows seeds one of every dependent row,
// runs DeleteAccount, and asserts AccountByID returns ErrNotFound AND
// the dependent lists are empty (zero children survive).
func TestPg_DeleteAccount_CascadesAllRows(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, err := seedFullAccount(t, s, ctx)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	// DeleteAccount is a conditional sentinel — it only fires for
	// rows already in deleted_pending. Match the apid handler shape.
	if err := s.MarkAccountDeletionPending(ctx, acctID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	if err := s.DeleteAccount(ctx, acctID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	if _, err := s.AccountByID(ctx, acctID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("AccountByID after delete = %v, want ErrNotFound", err)
	}
	// Every list-for-account method should return empty after the delete.
	if rows, _ := s.ListApps(ctx, acctID); len(rows) != 0 {
		t.Errorf("ListApps = %d, want 0", len(rows))
	}
	if rows, _ := s.ListDeploymentsForAccount(ctx, acctID, time.Time{}, 100); len(rows) != 0 {
		t.Errorf("ListDeploymentsForAccount = %d, want 0", len(rows))
	}
	if rows, _ := s.ListBuildsForAccount(ctx, acctID); len(rows) != 0 {
		t.Errorf("ListBuildsForAccount = %d, want 0", len(rows))
	}
	if rows, _ := s.ListInstancesForAccount(ctx, acctID); len(rows) != 0 {
		t.Errorf("ListInstancesForAccount = %d, want 0", len(rows))
	}
	if rows, _ := s.ListCronsForAccount(ctx, acctID); len(rows) != 0 {
		t.Errorf("ListCronsForAccount = %d, want 0", len(rows))
	}
	if rows, _ := s.ListDomainsForAccount(ctx, acctID); len(rows) != 0 {
		t.Errorf("ListDomainsForAccount = %d, want 0", len(rows))
	}
	if rows, _ := s.ListAPIKeys(ctx, acctID); len(rows) != 0 {
		t.Errorf("ListAPIKeys = %d, want 0", len(rows))
	}
}

// TestPg_DeleteAccount_TwiceIsErrNotFound proves the second delete
// surfaces ErrNotFound (not a silent success). The grace timer relies
// on this to ignore redelivered ticks.
func TestPg_DeleteAccount_TwiceIsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, err := seedFullAccount(t, s, ctx)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.MarkAccountDeletionPending(ctx, acctID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	if err := s.DeleteAccount(ctx, acctID); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	err = s.DeleteAccount(ctx, acctID)
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
}

// TestPg_UsageByAccount_AggregatesByMonth seeds two rows for the same
// (account, app, month) and asserts the SELECT … WHERE account_id = $1
// sums them. MemStore has its own shape (per-minute accumulator), so
// this round-trip only lives in PgStore.
func TestPg_UsageByAccount_AggregatesByMonth(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, depID, err := seedFullAccountWithDep(t, s, ctx)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Find the app + instance we just seeded.
	apps, err := s.ListApps(ctx, acctID)
	if err != nil || len(apps) == 0 {
		t.Fatalf("ListApps: %v", err)
	}
	app := apps[0]
	ins, err := s.CreateInstance(ctx, app.ID, depID, "running", 256, defaultLocalID)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	minute := time.Now().UTC().Truncate(time.Minute).Add(-1 * time.Minute)
	if err := s.AppendUsage(ctx, acctID, app.ID, ins.ID, minute, 1024, 5); err != nil {
		t.Fatalf("AppendUsage: %v", err)
	}
	if err := s.AppendUsage(ctx, acctID, app.ID, ins.ID, minute.Add(time.Minute), 2048, 7); err != nil {
		t.Fatalf("AppendUsage: %v", err)
	}
	rows, err := s.UsageByAccount(ctx, acctID, time.Time{})
	if err != nil {
		t.Fatalf("UsageByAccount: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("UsageByAccount = %d rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].MBSeconds != 1024+2048 || rows[0].Requests != 5+7 {
		t.Errorf("UsageByAccount aggregate = %+v, want mb=%d req=%d",
			rows[0], 1024+2048, 5+7)
	}
}

// TestPg_DeleteAccount_CascadesEvents is the G6 right-to-erasure
// regression (spec §17 G6, ADR-021). Audit events whose subject points
// at the account, or whose payload's account_id matches, must NOT
// outlive the customer — they are part of the personal data the
// customer is requesting to be erased.
//
// Seeds two event rows against the account: one keyed by subject, one
// keyed by data->>account_id. DeleteAccount must remove both.
func TestPg_DeleteAccount_CascadesEvents(t *testing.T) {
	d := pgStoreAccountDeletionWithPool(t)
	s, ctx, pool := d.store, d.ctx, d.pool
	acctID, err := seedFullAccount(t, s, ctx)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.MarkAccountDeletionPending(ctx, acctID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	subject := acctID // events.subject is uuid; id is already uuid
	payload := []byte(`{"account_id":"` + acctID + `","note":"GDPR export"}`)
	if err := s.AppendEvent(ctx, "test", "export", &subject, payload); err != nil {
		t.Fatalf("AppendEvent subject=%s: %v", subject, err)
	}
	if err := s.AppendEvent(ctx, "test", "export", nil, payload); err != nil {
		t.Fatalf("AppendEvent data: %v", err)
	}
	// Pre-condition: both event rows exist. We can't use ListEvents("")
	// here — the PgStore implementation filters by subject IS NOT NULL
	// (WHERE subject = NULL matches nothing), which would mask the data-
	// keyed event we just inserted. Use a raw count against the table
	// instead — this is a deletion-cascade test, so reading the table
	// directly is honest.
	var n int
	if err := pool.QueryRow(ctx,
		`select count(*) from events where data->>'account_id' = $1::text`, acctID).Scan(&n); err != nil {
		t.Fatalf("count pre: %v", err)
	}
	if n != 2 {
		t.Fatalf("precondition: want 2 events keyed to account, got %d", n)
	}

	if err := s.DeleteAccount(ctx, acctID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}

	// Post-condition: no event with subject=acctID or data->>account_id=acctID.
	if err := pool.QueryRow(ctx,
		`select count(*) from events where subject = $1::uuid or data->>'account_id' = $1::text`,
		acctID).Scan(&n); err != nil {
		t.Fatalf("count post: %v", err)
	}
	if n != 0 {
		t.Errorf("want 0 events after DeleteAccount, got %d", n)
	}
}

// TestPg_DeleteAccount_RestoredRowSurvivesTick is the regression for the
// restore→tick race (review of #46). A customer that hits
// POST /v1/account/restore in between pkg/grace.RunOnce's
// ListAllAccounts and DeleteAccount must NOT see their row hard-
// deleted. The conditional `WHERE id=$1 AND status='deleted_pending'`
// on the parent DELETE is what closes the race.
func TestPg_DeleteAccount_RestoredRowSurvivesTick(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, err := seedFullAccount(t, s, ctx)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.MarkAccountDeletionPending(ctx, acctID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}
	// Customer races the timer: restore BEFORE the sweep runs.
	if err := s.RestoreAccount(ctx, acctID); err != nil {
		t.Fatalf("RestoreAccount: %v", err)
	}
	// DeleteAccount now must report ErrNotFound (the conditional
	// didn't match the row) and the account must still exist.
	err = s.DeleteAccount(ctx, acctID)
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("DeleteAccount after restore = %v, want ErrNotFound", err)
	}
	if _, err := s.AccountByID(ctx, acctID); err != nil {
		t.Errorf("AccountByID after restore+race-delete = %v, want nil "+
			"(the race must NOT delete a restored account)", err)
	}
}

// TestPg_DeleteAccount_OnActiveRowReturnsErrNotFound is the regression
// for the sentinel on the conditional DELETE (review of #46). Before
// the patch, DeleteAccount always returned nil on a redelivered tick
// because the probe ran AFTER the unconditional accounts DELETE. The
// new conditional DELETE returns ErrNotFound when status !=
// 'deleted_pending' — same answer the grace timer relies on for
// idempotency.
func TestPg_DeleteAccount_OnActiveRowReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "active@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	err = s.DeleteAccount(ctx, acct.ID)
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("DeleteAccount on active row = %v, want ErrNotFound", err)
	}
	if _, err := s.AccountByID(ctx, acct.ID); err != nil {
		t.Errorf("AccountByID after no-op delete = %v, want nil", err)
	}
}

// TestPg_ListCronsForAccount_ReturnsSeededRows is the regression test
// for the cron-scan mismatch found in PR #83 review. The shipped
// pgstore.ListCronsForAccount SELECTed 5 columns but Scan read 6
// (incl. a non-existent c.CreatedAt), so every PG export call returned
// a scan error that the apid export helper silently swallowed —
// crons came back empty in every GDPR bundle. This test seeds a cron
// via seedFullAccountWithDep and asserts the list round-trips without
// error and contains the seeded schedule string. Skips in CI when
// Postgres is unreachable (pgtest.Open).
func TestPg_ListCronsForAccount_ReturnsSeededRows(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, err := seedFullAccountWithDep(t, s, ctx)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	rows, err := s.ListCronsForAccount(ctx, acctID)
	if err != nil {
		t.Fatalf("ListCronsForAccount: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d crons, want 1 (scan mismatch would have returned an empty slice)", len(rows))
	}
	if rows[0].Schedule != "*/5 * * * *" {
		t.Errorf("cron schedule = %q, want %q", rows[0].Schedule, "*/5 * * * *")
	}
	if rows[0].Path != "/healthz" || !rows[0].Enabled {
		t.Errorf("cron row shape = %+v", rows[0])
	}
}
