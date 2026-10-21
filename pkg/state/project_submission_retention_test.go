package state_test

// adr: 20261021094500

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectSubmissionRetentionMemory(t *testing.T) {
	testProjectSubmissionRetention(t, state.NewMemStore())
}

// reserveRetentionReceipts records one receipt per safe-release status ("" for
// a plain apply) and returns their attempt IDs in the same order.
func reserveRetentionReceipts(t *testing.T, store state.Store, accountID string, statuses ...string) []string {
	t.Helper()
	ledger := store.(state.ProjectSubmissionStore)
	var ids []string
	for i, status := range statuses {
		id := strings.Repeat(string(rune('a'+i)), 32)
		frame := api.ProjectSubmissionReceipt{AttemptID: id, ProjectSlug: "retained", Status: "processing"}
		if status != "" {
			frame.Apply.SafeRelease = &api.ProjectSafeRelease{Status: status}
		}
		if reserved, err := ledger.ReserveProjectSubmission(t.Context(), accountID, id, strings.Repeat("f", 64), frame); err != nil || !reserved {
			t.Fatalf("reserve %s: %v %v", status, reserved, err)
		}
		ids = append(ids, id)
	}
	return ids
}

func testProjectSubmissionRetention(t *testing.T, store state.Store) {
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "receipt-retention@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	ids := reserveRetentionReceipts(t, store, account.ID, "", "succeeded", "verifying", "building", "rolling_back", "rolled_back")
	retention := store.(state.ProjectSubmissionRetentionStore)
	ledger := store.(state.ProjectSubmissionStore)
	// Nothing is older than a past cutoff.
	if n, err := retention.PruneProjectSubmissions(ctx, time.Now().Add(-time.Hour), 10); err != nil || n != 0 {
		t.Fatalf("recent receipts pruned: %d %v", n, err)
	}
	// Batches are bounded; a future cutoff makes every receipt old.
	cutoff := time.Now().Add(time.Hour)
	if n, err := retention.PruneProjectSubmissions(ctx, cutoff, 1); err != nil || n != 1 {
		t.Fatalf("first batch: %d %v", n, err)
	}
	if n, err := retention.PruneProjectSubmissions(ctx, cutoff, 10); err != nil || n != 2 {
		t.Fatalf("second batch: %d %v", n, err)
	}
	for i, id := range ids {
		_, err := ledger.ProjectSubmission(ctx, account.ID, id)
		advancing := i >= 2 && i <= 4
		if advancing && err != nil {
			t.Fatalf("advancing release %d lost its receipt: %v", i, err)
		}
		if !advancing && !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("settled receipt %d retained: %v", i, err)
		}
	}
}
