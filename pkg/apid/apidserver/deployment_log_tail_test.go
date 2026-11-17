package apidserver

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// Before, the live tail waited on a topic nothing published, so a following
// `gregale deploy` saw only the first page of build lines.
func TestNewDeploymentLogsReturnsRowsAfterCursorInOrder(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	const dep = "dep-tail"
	for i := 0; i < 1200; i++ {
		if _, err := store.AppendDeploymentLog(ctx, dep, "build", "line"); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	first, _, err := store.ListDeploymentLogs(ctx, dep, 0, 50)
	if err != nil || len(first) == 0 {
		t.Fatalf("first page: %v", err)
	}
	cursor := initialDeploymentLogSeq(ctx, store, dep, first, 0)

	if rows, newest, err := newDeploymentLogs(ctx, store, dep, cursor); err != nil || len(rows) != 0 || newest != cursor {
		t.Fatalf("no new rows: got %d rows, newest %d (cursor %d), err %v", len(rows), newest, cursor, err)
	}
	for i := 0; i < 1100; i++ { // more than two tail pages
		if _, err := store.AppendDeploymentLog(ctx, dep, "image", "progress"); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	rows, newest, err := newDeploymentLogs(ctx, store, dep, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1100 {
		t.Fatalf("rows = %d, want the 1100 written after the cursor", len(rows))
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].Seq <= rows[i-1].Seq {
			t.Fatalf("rows out of order at %d: %d after %d", i, rows[i].Seq, rows[i-1].Seq)
		}
	}
	if rows[0].Seq != cursor+1 || newest != rows[len(rows)-1].Seq {
		t.Fatalf("first %d newest %d, want %d and the last row", rows[0].Seq, newest, cursor+1)
	}
	// A before_seq page still tails from the newest row.
	if got := initialDeploymentLogSeq(ctx, store, dep, nil, 10); got != newest {
		t.Fatalf("before_seq cursor = %d, want newest %d", got, newest)
	}
	var _ deploymentLogLister = store
	_ = state.LogEntry{}
}
