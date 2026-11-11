package apidserver

import (
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// debugRegressionMaxDeployments bounds the deployments one detector pass
// reads to pick its comparison pair.
const debugRegressionMaxDeployments = 10

// debugRegressionComparison is the pair of deployments and windows one
// detector pass compares (ADR-127 amendment 2026-11-05).
type debugRegressionComparison struct {
	cur, prev          sqlc.ListDeploymentsForCompareRow
	curStart, curEnd   time.Time
	prevStart, prevEnd time.Time
}

// chooseRegressionComparison picks what the regression detector compares.
//
// The current deployment is the newest release among the deployments that
// served in the last window: by release, not by who answered the last
// request, so a canary and its stable deployment never swap places between
// passes. Its predecessor is another deployment still serving beside it (a
// canary's stable side, a traffic split), compared over the same window, or
// else the deployment that served most recently before it, compared over its
// own last window of traffic. Both windows follow the deployments' traffic
// rather than the clock, so a regression is still found hours after the
// rollout, while the previous deployment's rows are within the detector's
// lookback.
func chooseRegressionComparison(deps []sqlc.ListDeploymentsForCompareRow, now time.Time) (debugRegressionComparison, bool) {
	type candidate struct {
		row                 sqlc.ListDeploymentsForCompareRow
		first, last, create time.Time
	}
	servingSince := now.Add(-debugRegressionBaselineWindow)
	var serving, others []candidate
	for _, row := range deps {
		first, okFirst := regressionTime(row.FirstSeen)
		last, okLast := regressionTime(row.LastSeen)
		if !row.DeploymentID.Valid || !okFirst || !okLast {
			continue
		}
		created := first
		if parsed, err := time.Parse(time.RFC3339Nano, row.DeploymentCreatedAt); err == nil {
			created = parsed
		}
		c := candidate{row: row, first: first, last: last, create: created}
		if !last.Before(servingSince) {
			serving = append(serving, c)
		} else {
			others = append(others, c)
		}
	}
	if len(serving) == 0 {
		return debugRegressionComparison{}, false
	}
	byRelease := func(list []candidate) {
		sort.SliceStable(list, func(i, j int) bool {
			if !list[i].create.Equal(list[j].create) {
				return list[i].create.After(list[j].create)
			}
			return list[i].row.DeploymentID.String() > list[j].row.DeploymentID.String()
		})
	}
	byRelease(serving)
	cur := serving[0]
	out := debugRegressionComparison{cur: cur.row, curEnd: now}
	out.curStart = servingSince
	if cur.first.After(out.curStart) {
		out.curStart = cur.first
	}
	if len(serving) > 1 {
		// Served side by side: compare the same stretch of time.
		out.prev = serving[1].row
		out.prevStart, out.prevEnd = out.curStart, out.curEnd
		return out, true
	}
	if len(others) == 0 {
		return debugRegressionComparison{}, false
	}
	sort.SliceStable(others, func(i, j int) bool { return others[i].last.After(others[j].last) })
	prev := others[0]
	out.prev = prev.row
	// The predecessor's last window of traffic; the end is exclusive in the
	// queries, so step past its last row.
	out.prevEnd = prev.last.Add(time.Millisecond)
	out.prevStart = out.prevEnd.Add(-debugRegressionBaselineWindow)
	return out, true
}

// regressionTime reads a MIN/MAX(received_at) column, which sqlc types as
// interface{}.
func regressionTime(raw interface{}) (time.Time, bool) {
	switch value := raw.(type) {
	case time.Time:
		return value, true
	case pgtype.Timestamptz:
		return value.Time, value.Valid
	}
	return time.Time{}, false
}
