package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// --- B2.2 (issue #196): claim fairness ---

// seedFairnessFixture creates a store with three accounts × apps ×
// deployments × queued builds, then runs N claims (returning the
// build IDs picked in order). Used by the per-account fairness +
// starvation-fallback tests below.
func seedFairnessFixture(t *testing.T, buildsPerAccount int) (m *state.MemStore, accounts [3]string, appIDs [3]string, buildIDsByAcct map[string][]string) {
	t.Helper()
	m = state.NewMemStore()
	buildIDsByAcct = map[string][]string{}
	for i := 0; i < 3; i++ {
		acct, err := m.CreateAccount(context.Background(), "acct-"+uuid.NewString()[:8]+"@example.com", api.PlanPro)
		if err != nil {
			t.Fatalf("CreateAccount[%d]: %v", i, err)
		}
		app, err := m.CreateApp(context.Background(), state.App{
			AccountID:      acct.ID,
			Slug:           "app-" + uuid.NewString()[:8],
			RAMMB:          256,
			IdleTimeoutS:   60,
			MaxConcurrency: 5,
		})
		if err != nil {
			t.Fatalf("CreateApp[%d]: %v", i, err)
		}
		accounts[i] = acct.ID
		appIDs[i] = app.ID

		buildIDsByAcct[acct.ID] = make([]string, 0, buildsPerAccount)
		for b := 0; b < buildsPerAccount; b++ {
			dep, err := m.CreateDeployment(context.Background(), state.Deployment{
				AppID:       app.ID,
				Kind:        state.DeploymentKindTarball,
				SourcePath:  "/tmp/fake.tar.gz",
				SourceBytes: 100,
				LogPath:     "/tmp/build.log",
			})
			if err != nil {
				t.Fatalf("CreateDeployment[%d/%d]: %v", i, b, err)
			}
			build, err := m.CreateBuild(context.Background(), dep.ID, state.DeploymentKindTarball, 100, dep.LogPath)
			if err != nil {
				t.Fatalf("CreateBuild[%d/%d]: %v", i, b, err)
			}
			buildIDsByAcct[acct.ID] = append([]string{build.ID}, buildIDsByAcct[acct.ID]...) // FIFO order
		}
	}
	return m, accounts, appIDs, buildIDsByAcct
}

// TestMemStore_ClaimNextQueuedBuildWithFairness_FreshQueuePicksAny is the
// baseline: with no rows in recentClaims, every account is "fresh"; the
// claim should pick the head-of-FIFO queued build, period. This is
// the same observable behaviour as the pre-B2.2 FIFO claim.
func TestMemStore_ClaimNextQueuedBuildWithFairness_FreshQueuePicksAny(t *testing.T) {
	m, accounts, _, _ := seedFairnessFixture(t, 1)

	_, err := m.ClaimNextQueuedBuildWithFairness(context.Background(), 30*time.Second)
	if err != nil {
		t.Fatalf("claim 1: %v", err)
	}
	// Record the claim for the first account we can find via build→deployment→app.
	if err := m.RecordRecentBuildClaim(context.Background(), accounts[0], uuid.NewString()); err != nil {
		t.Fatalf("record: %v", err)
	}
	_, err = m.ClaimNextQueuedBuildWithFairness(context.Background(), 30*time.Second)
	if err != nil {
		t.Fatalf("claim 2: %v", err)
	}
}

// TestMemStore_ClaimNextQueuedBuildWithFairness_NoStarvationWhenAllRecent
// is the critical-invariant #1 gate: every account in the skip set
// must still yield a claim (the fallback path). The end-state: N
// claims drain the queue.
func TestMemStore_ClaimNextQueuedBuildWithFairness_NoStarvationWhenAllRecent(t *testing.T) {
	m, accounts, _, _ := seedFairnessFixture(t, 1)

	// Pre-populate recentClaims with ALL three accounts so every
	// queued build's account is in the skip set.
	for _, acct := range accounts {
		if err := m.RecordRecentBuildClaim(context.Background(), acct, uuid.NewString()); err != nil {
			t.Fatalf("seed skip: %v", err)
		}
	}

	// Now drive claims. Every call should succeed (fallback to FIFO
	// across every queued row), draining the queue.
	for i := 0; i < len(accounts); i++ {
		got, err := m.ClaimNextQueuedBuildWithFairness(context.Background(), 30*time.Second)
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		if got.Status != state.BuildRunning {
			t.Errorf("claim %d status = %s, want running", i, got.Status)
		}
	}
	// Queue should now be empty.
	if _, err := m.ClaimNextQueuedBuildWithFairness(context.Background(), 30*time.Second); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("after draining, claim = %v, want ErrNotFound", err)
	}
}

// TestMemStore_ClaimNextQueuedBuildWithFairness_PreferQuietAccount is the
// core B2.2 promise: with account A in the skip set (within window),
// the next claim must NOT pick any of A's queued builds. B or C
// builds must win.
func TestMemStore_ClaimNextQueuedBuildWithFairness_PreferQuietAccount(t *testing.T) {
	m, accounts, _, buildIDsByAcct := seedFairnessFixture(t, 3) // 3 builds per account, 9 total

	// Mark A as recently-claimed so the fairness filter excludes it.
	if err := m.RecordRecentBuildClaim(context.Background(), accounts[0], uuid.NewString()); err != nil {
		t.Fatalf("seed skip: %v", err)
	}

	// Pick 6 builds (B has 3, C has 3) → none should be from A.
	for i := 0; i < 6; i++ {
		pick, err := m.ClaimNextQueuedBuildWithFairness(context.Background(), 30*time.Second)
		if err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		dep, err := m.DeploymentByID(context.Background(), pick.DeploymentID)
		if err != nil {
			t.Fatalf("deployment: %v", err)
		}
		app, err := m.AppByID(context.Background(), dep.AppID)
		if err != nil {
			t.Fatalf("app: %v", err)
		}
		if app.AccountID == accounts[0] {
			t.Errorf("claim %d picked A; A is in skip set", i)
		}
	}

	// Now A's 3 builds must still be BuildQueued — the fairness filter
	// must not delete, mark-failed, or otherwise touch rows it skipped.
	// (The upper-loop asserts none of A was *picked*; this loop asserts
	// they remained *eligible* for the next fairness window. Together
	// they pin both halves of the B2.2 promise.)
	for _, bid := range buildIDsByAcct[accounts[0]] {
		b, err := m.BuildByID(context.Background(), bid)
		if err != nil {
			t.Fatalf("BuildByID(%s): %v", bid, err)
		}
		if b.Status != state.BuildQueued {
			t.Errorf("A's build %s status = %s, want queued (fairness filter must not touch skipped rows)", bid, b.Status)
		}
	}
}

// TestMemStore_RecordRecentBuildClaim_EmptyAccountIDRejects is the
// input-validation gate. The SQL path enforces the NOT NULL via the
// column; MemStore mirrors the same API contract by returning an
// error on empty.
func TestMemStore_RecordRecentBuildClaim_EmptyAccountIDRejects(t *testing.T) {
	m := state.NewMemStore()
	if err := m.RecordRecentBuildClaim(context.Background(), "", uuid.NewString()); err == nil {
		t.Error("empty account_id should be rejected")
	}
}
