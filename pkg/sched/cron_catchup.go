package sched

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cronDueOccurrences returns a schedule's due occurrences from first up to
// now, keeping only the newest keep of them (newest last), and how many
// older due occurrences it passed over. Walking the schedule is in memory and
// cheap; each kept occurrence costs a database write on schedd's main loop,
// so after a long outage or suspension only the newest are recorded. The
// newest due time is always kept, so coalesce_latest fires it however long
// the gap was (ADR-20261026201726 amendment 2026-10-30).
func cronDueOccurrences(sched *Schedule, first, now time.Time, keep int) ([]time.Time, int) {
	if keep < 1 {
		keep = 1
	}
	due := make([]time.Time, 0, keep)
	skipped := 0
	for next, scanned := first, 0; !next.After(now) && scanned < api.CronCatchUpScanMax; next, scanned = sched.NextFireAt(next), scanned+1 {
		if len(due) == keep {
			due = append(due[:0], due[1:]...)
			skipped++
		}
		due = append(due, next)
	}
	return due, skipped
}
