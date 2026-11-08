package apidserver_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// adr: 127 — amendment 2026-11-05: the detector compares the newest serving
// release with its predecessor by release order, over windows that follow
// their traffic rather than the clock.
func TestChooseRegressionComparison(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	dep := func(name string, created, first, last time.Time) sqlc.ListDeploymentsForCompareRow {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(name))
		return sqlc.ListDeploymentsForCompareRow{
			DeploymentID: pgtype.UUID{Bytes: id, Valid: true}, FirstSeen: first, LastSeen: last,
			DeploymentCreatedAt: created.Format(time.RFC3339Nano),
		}
	}
	old := dep("old", now.Add(-48*time.Hour), now.Add(-23*time.Hour), now.Add(-5*time.Hour))
	rolledOut := dep("new", now.Add(-5*time.Hour), now.Add(-5*time.Hour), now.Add(-time.Second))
	stable := dep("stable", now.Add(-48*time.Hour), now.Add(-23*time.Hour), now.Add(-2*time.Second))
	canary := dep("canary", now.Add(-10*time.Minute), now.Add(-9*time.Minute), now.Add(-3*time.Second))

	tests := []struct {
		name              string
		deps              []sqlc.ListDeploymentsForCompareRow
		ok                bool
		cur, prev         string
		curStart, prevEnd time.Time
		prevStart         time.Time
	}{
		{
			// Five hours after the rollout the baseline is still the old
			// deployment's last half hour, not an empty clock window.
			name: "rolled out hours ago", deps: []sqlc.ListDeploymentsForCompareRow{rolledOut, old}, ok: true,
			cur: "new", prev: "old", curStart: now.Add(-30 * time.Minute),
			prevStart: now.Add(-5*time.Hour - 30*time.Minute + time.Millisecond), prevEnd: now.Add(-5*time.Hour + time.Millisecond),
		},
		{
			// Whichever side answered last, the canary is the release under test.
			name: "canary answered last", deps: []sqlc.ListDeploymentsForCompareRow{canary, stable}, ok: true,
			cur: "canary", prev: "stable", curStart: now.Add(-9 * time.Minute), prevStart: now.Add(-9 * time.Minute), prevEnd: now,
		},
		{
			name: "stable answered last", deps: []sqlc.ListDeploymentsForCompareRow{stable, canary}, ok: true,
			cur: "canary", prev: "stable", curStart: now.Add(-9 * time.Minute), prevStart: now.Add(-9 * time.Minute), prevEnd: now,
		},
		{name: "only one deployment", deps: []sqlc.ListDeploymentsForCompareRow{rolledOut}},
		{name: "nothing serving", deps: []sqlc.ListDeploymentsForCompareRow{old}},
	}
	names := map[string]string{
		old.DeploymentID.String(): "old", rolledOut.DeploymentID.String(): "new",
		stable.DeploymentID.String(): "stable", canary.DeploymentID.String(): "canary",
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := apidserver.ChooseRegressionComparison(tt.deps, now)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if c, p := names[got.Cur], names[got.Prev]; c != tt.cur || p != tt.prev {
				t.Fatalf("pair = %s vs %s, want %s vs %s", c, p, tt.cur, tt.prev)
			}
			if !got.CurStart.Equal(tt.curStart) || !got.CurEnd.Equal(now) || !got.PrevStart.Equal(tt.prevStart) || !got.PrevEnd.Equal(tt.prevEnd) {
				t.Fatalf("windows cur [%s, %s) prev [%s, %s)", got.CurStart, got.CurEnd, got.PrevStart, got.PrevEnd)
			}
		})
	}
}
