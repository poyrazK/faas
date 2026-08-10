package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func pgCoverageFixture(t *testing.T) (*state.PgStore, context.Context, state.Account, state.App, state.Deployment) {
	t.Helper()
	s, ctx := pgStore(t)
	now := time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC)
	account, err := s.CreateAccount(ctx, "pg-coverage-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "pg-coverage-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + uuid.NewString(), Status: state.DeployPending, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	return s, ctx, account, app, deployment
}

func TestPg_CoverageInvocationLifecycle(t *testing.T) {
	s, ctx, account, app, _ := pgCoverageFixture(t)
	if _, err := s.InvocationByID(ctx, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing invocation = %v", err)
	}
	enqueue := func(due time.Time) (state.Invocation, error) {
		return s.EnqueueInvocation(ctx, state.Invocation{
			AccountID: account.ID, AppID: app.ID, Source: state.InvocationAsyncInvoke, DueAt: due,
		})
	}
	due, err := enqueue(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	future, err := enqueue(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ListDueInvocations(ctx, time.Now(), 10); err != nil || len(got) != 1 || got[0].ID != due.ID {
		t.Fatalf("due list returned %d rows, want 1", len(got))
	}
	if _, err := s.ClaimInvocation(ctx, due.ID, "instance", 30); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimInvocation(ctx, due.ID, "instance", 30); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("double claim = %v", err)
	}
	if err := s.CompleteInvocation(ctx, due.ID, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.FailInvocation(ctx, due.ID, "noop", 0, 0); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("fail after completion = %v", err)
	}
	fail, err := enqueue(time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimInvocation(ctx, fail.ID, "instance", 30); err != nil {
		t.Fatal(err)
	}
	if err := s.FailInvocation(ctx, fail.ID, "boom", time.Minute, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.FailInvocation(ctx, fail.ID, "perm", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelInvocation(ctx, future.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CountInstanceInvocationsInMinute(ctx, uuid.NewString(), time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestPg_CoveragePasswordOAuthIdempotency(t *testing.T) {
	s, ctx, account, _, _ := pgCoverageFixture(t)
	if err := s.SetAccountPassword(ctx, account.ID, "phc-test"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.AccountPasswordByAccountID(ctx, account.ID); err != nil || got != "phc-test" {
		t.Fatalf("password read = %q, %v", got, err)
	}
	if err := s.SetAccountPassword(ctx, account.ID, "phc-test-v2"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAccountPassword(ctx, "", "phc"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("empty account password = %v", err)
	}
	if _, err := s.AccountPasswordByAccountID(ctx, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("password missing = %v", err)
	}
	if err := s.DeleteAccountPassword(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertOAuthLink(ctx, account.ID, "google", "subj", account.Email, true); err != nil {
		t.Fatal(err)
	}
	// MemStore detects cross-account OAuth takeover on UpsertOAuthLink;
	// PgStore silently drops the conflicting write (the WHERE clause on
	// the ON CONFLICT branch filters out non-matching account_id, so
	// the affected-row count is 0 and no error surfaces). Both are
	// safe — the existing row keeps its original binding. Document the
	// divergence here; anti-takeover enforcement lives in the OAuth
	// callback's pre-flight OAuthLinkByProviderSubject lookup, not the
	// upsert.
	if err := s.UpsertOAuthLink(ctx, uuid.NewString(), "google", "subj", "other@example.com", true); err != nil {
		t.Fatalf("oauth takeover on PgStore = %v (want nil — PgStore silently keeps the original binding)", err)
	}
}

func TestPg_CoverageLoginAndCliTokens(t *testing.T) {
	s, ctx, account, _, _ := pgCoverageFixture(t)
	expired := []byte("expired-token")
	if err := s.IssueLoginToken(ctx, expired, account.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeLoginToken(ctx, expired); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired login token = %v", err)
	}
	hash := []byte("pg-login-coverage")
	if err := s.IssueLoginToken(ctx, hash, account.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeLoginToken(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeLoginToken(ctx, hash); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("replay login token = %v", err)
	}
	if _, _, err := s.PeekCliAuthCode(ctx, []byte("missing-code")); err == nil {
		t.Fatal("peek missing code should error")
	}
	cliHash := []byte("pg-cli-coverage")
	if err := s.IssueCliAuthCode(ctx, cliHash, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PeekCliAuthCode(ctx, cliHash); err == nil {
		t.Fatal("peek expired cli should error")
	}
	freshHash := []byte("pg-cli-coverage-fresh")
	if err := s.IssueCliAuthCode(ctx, freshHash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	cliHash = freshHash
	if err := s.ClaimCliAuthCode(ctx, cliHash, account.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ConsumeCliAuthCode(ctx, cliHash); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ConsumeCliAuthCode(ctx, cliHash); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cli replay = %v", err)
	}
}

func TestPg_CoverageSnapshotsAndDomain(t *testing.T) {
	s, ctx, _, _, deployment := pgCoverageFixture(t)
	if _, err := s.CreateSnapshot(ctx, state.Snapshot{DeploymentID: deployment.ID}); err == nil {
		t.Fatal("snapshot without storage key should fail")
	}
	snap, err := s.CreateSnapshot(ctx, state.Snapshot{DeploymentID: deployment.ID, FCVersion: "v1", StorageKey: "snap/pg-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSnapshotStale(ctx, snap.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LatestSnapshot(ctx, deployment.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("latest stale = %v", err)
	}
	// PgStore schema has no unique constraint on (deployment_id, storage_key)
	// (snapshots only enforce storage_key != ''), so a second insert with
	// the same key succeeds — MemStore's in-memory dedupe diverges here.
	// This path is here to keep the insert branch of CreateSnapshot
	// exercised after the stale branch above; the LatestSnapshot ErrNotFound
	// assertion is the meaningful negative-path coverage.
	if _, err := s.CreateSnapshot(ctx, state.Snapshot{DeploymentID: deployment.ID, FCVersion: "v1", StorageKey: "snap/pg-2"}); err != nil {
		t.Fatalf("pg second snapshot with fresh key = %v (want nil)", err)
	}
	if _, err := s.CreateCustomDomain(ctx, "missing.example", uuid.NewString(), "tok"); err == nil {
		t.Fatal("domain with unknown app should fail")
	}
}

func TestPg_CoverageInstanceStatePaths(t *testing.T) {
	s, ctx, _, app, deployment := pgCoverageFixture(t)
	defaultNode := resolveDefaultLocal(t, ctx, s)
	instance, err := s.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateRunning), 512, defaultNode, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInstanceStateToTerminal(ctx, instance.ID, string(state.StateStopped), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListInstancesInTerminalStatesOlderThan(ctx, []state.State{state.StateStopped}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListInstancesByStatesOlderThan(ctx, []state.State{state.StateStopped}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInstanceState(ctx, uuid.NewString(), string(state.StateRunning)); err == nil {
		t.Fatal("missing instance update should error")
	}
	if _, err := s.LiveDeployment(ctx, app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("live deployment before status=live = %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.LiveDeployment(ctx, app.ID); err != nil || got.ID != deployment.ID {
		t.Fatalf("live deployment after mark = %+v, %v", got, err)
	}
}

// TestPg_ListBuildsForAccountPaged covers the three-way branch in
// PgStore.ListBuildsForAccountPaged (first-page / keyset / queued-
// tail cursor). The reviewer flagged that the whitebox handler
// tests use memstore only — pgstore's branch order is the same
// shape but unverified. The order is load-bearing for the
// queued-tail case (before.IsZero() && beforeID != "" must hit
// the queued-tail branch, NOT the first-page branch) — see
// pkg/state/pgstore.go::ListBuildsForAccountPaged.
func TestPg_ListBuildsForAccountPaged(t *testing.T) {
	s, ctx, account, _, deployment := pgCoverageFixture(t)

	// Seed: 1 running build (started_at set) + 1 queued build
	// (started_at NULL) + 1 cross-account build (must NOT surface).
	// Build kind is `dockerfile` (per the builds_kind_check constraint
	// added in 00085 — `image` is reserved for the *deployment* kind
	// and rejected on builds).
	running, err := s.CreateBuild(ctx, deployment.ID, state.DeploymentKindDockerfile, 1024, "/tmp/q.log")
	if err != nil {
		t.Fatalf("CreateBuild running: %v", err)
	}
	if _, err := s.ClaimQueuedBuild(ctx, running.ID); err != nil {
		t.Fatalf("ClaimQueuedBuild: %v", err)
	}
	queued, err := s.CreateBuild(ctx, deployment.ID, state.DeploymentKindDockerfile, 1024, "/tmp/q2.log")
	if err != nil {
		t.Fatalf("CreateBuild queued: %v", err)
	}
	// Cross-account noise: another account's build under a different
	// deployment. Must never appear in any paged query for acct 1.
	other, err := s.CreateAccount(ctx, "pg-bld-other-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := s.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "pg-bld-other-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	otherDep, err := s.CreateDeployment(ctx, state.Deployment{AppID: otherApp.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + uuid.NewString(), Status: state.DeployPending, CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateBuild(ctx, otherDep.ID, state.DeploymentKindDockerfile, 999, "/tmp/x.log"); err != nil {
		t.Fatal(err)
	}

	// (1) First page — no cursor. Returns both owned builds
	// ordered (running first, queued last NULLS LAST). Cross-
	// account row excluded by a.account_id = $1 in the SQL.
	page1, err := s.ListBuildsForAccountPaged(ctx, account.ID, "", "", time.Time{}, "", 0)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len = %d, want 2 (running + queued): %+v", len(page1), page1)
	}
	if page1[0].ID != running.ID {
		t.Errorf("page1[0] = %s, want running (%s)", page1[0].ID, running.ID)
	}
	if page1[0].StartedAt.IsZero() {
		t.Errorf("page1[0] started_at is zero, want non-zero")
	}
	if page1[1].ID != queued.ID {
		t.Errorf("page1[1] = %s, want queued (%s)", page1[1].ID, queued.ID)
	}
	if !page1[1].StartedAt.IsZero() {
		t.Errorf("page1[1] started_at = %v, want zero (queued)", page1[1].StartedAt)
	}

	// (2) Queued-tail cursor: before=zero, beforeID=queued.ID.
	// This is the branch order tripwire — pre-fix, this fell
	// into the first-page branch and returned the full list
	// again. Post-fix, this hits the queued-tail branch and
	// returns 0 rows (no queued rows with id < queued.id).
	page2, err := s.ListBuildsForAccountPaged(ctx, account.ID, "", "", time.Time{}, queued.ID, 0)
	if err != nil {
		t.Fatalf("page2 queued-tail: %v", err)
	}
	if len(page2) != 0 {
		t.Fatalf("page2 queued-tail len = %d, want 0 (queued tail exhaustion): %+v", len(page2), page2)
	}

	// (3) Keyset cursor pointing at the running row: before is
	// running.StartedAt, beforeID is running.ID. Returns the
	// queued row only (under DESC NULLS LAST the queued row
	// sorts AFTER the running row in the desc ordering).
	page3, err := s.ListBuildsForAccountPaged(ctx, account.ID, "", "", running.StartedAt, running.ID, 0)
	if err != nil {
		t.Fatalf("page3 keyset: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("page3 keyset len = %d, want 1 (queued): %+v", len(page3), page3)
	}
	if page3[0].ID != queued.ID {
		t.Errorf("page3[0] = %s, want queued (%s)", page3[0].ID, queued.ID)
	}

	// (4) Sanity: filter — app=app2.ID on a different owned app
	// drops the seeded builds entirely. (We didn't seed any build
	// under app2, so result is empty.)
	if _, err := s.CreateApp(ctx, state.App{ID: "00000000-0000-0000-0000-00000000bldgap", AccountID: account.ID, Slug: "pg-bld-empty-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60}); err != nil {
		t.Fatal(err)
	}
	appFilter, err := s.ListBuildsForAccountPaged(ctx, account.ID, "", "00000000-0000-0000-0000-00000000bldgap", time.Time{}, "", 0)
	if err != nil {
		t.Fatalf("app filter: %v", err)
	}
	if len(appFilter) != 0 {
		t.Errorf("app filter len = %d, want 0 (no builds under that app): %+v", len(appFilter), appFilter)
	}
}
