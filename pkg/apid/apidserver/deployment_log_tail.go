package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// deploymentLogTailPage is one ListDeploymentLogs read while tailing.
	deploymentLogTailPage = 500
	// deploymentLogTailMaxPages bounds the catch-up per poll; a builder
	// writing faster than this many rows per tick loses the oldest of them
	// from the live view (they stay in the table for `gregale logs`).
	deploymentLogTailMaxPages = 8
)

type deploymentLogLister interface {
	ListDeploymentLogs(ctx context.Context, deploymentID string, beforeSeq int64, limit int) ([]state.LogEntry, bool, error)
}

// newDeploymentLogs returns the deployment's log rows with seq > after in
// chronological order, and the highest seq seen.
//
// streamDeploymentLogs used to wait for TopicDeploymentLog, which nothing
// publishes (the build_log NOTIFY is not fanned in and carries a build id,
// not a deployment), so `gregale deploy` showed stage frames only and every
// build or imaging line written after the first page appeared only once the
// deployment had ended (Kroki feedback 2026-11-17). The handler now polls
// the table on its status tick.
func newDeploymentLogs(ctx context.Context, store deploymentLogLister, id string, after int64) ([]state.LogEntry, int64, error) {
	var collected []state.LogEntry
	before := int64(0)
	for range deploymentLogTailMaxPages {
		page, _, err := store.ListDeploymentLogs(ctx, id, before, deploymentLogTailPage)
		if err != nil {
			return nil, after, err
		}
		reachedOld := len(page) < deploymentLogTailPage
		for _, e := range page { // DESC by seq
			if e.Seq <= after {
				reachedOld = true
				break
			}
			collected = append(collected, e)
		}
		if reachedOld || len(page) == 0 {
			break
		}
		before = page[len(page)-1].Seq
	}
	newest := after
	out := make([]state.LogEntry, 0, len(collected))
	for i := len(collected) - 1; i >= 0; i-- {
		out = append(out, collected[i])
		if collected[i].Seq > newest {
			newest = collected[i].Seq
		}
	}
	return out, newest, nil
}

// tailDeploymentLogs writes the rows written since after and returns the new
// high-water seq. A read error keeps the old seq so the next tick retries.
func tailDeploymentLogs(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, store deploymentLogLister, id string, after int64) int64 {
	rows, newest, err := newDeploymentLogs(ctx, store, id, after)
	if err != nil {
		return after
	}
	for _, e := range rows {
		writeLogEvent(w, flusher, e)
	}
	return newest
}

// initialDeploymentLogSeq is the seq the live tail continues after: the
// newest row when the stream opened. A before_seq page is an older slice the
// caller pages back through; the tail still starts at the newest row so it
// never repeats rows the caller already has.
func initialDeploymentLogSeq(ctx context.Context, store deploymentLogLister, id string, page []state.LogEntry, beforeSeq int64) int64 {
	if beforeSeq > 0 {
		page, _, _ = store.ListDeploymentLogs(ctx, id, 0, 1)
	}
	newest := int64(0)
	for _, e := range page {
		if e.Seq > newest {
			newest = e.Seq
		}
	}
	return newest
}
