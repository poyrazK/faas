package apidserver_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/debugger"
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
	replaced := dep("replaced", now.Add(-20*time.Minute), now.Add(-16*time.Minute), now.Add(-12*time.Minute))
	replacement := dep("replacement", now.Add(-11*time.Minute), now.Add(-10*time.Minute), now.Add(-9*time.Minute))
	// One platform request (a cold-boot health check) reached the replaced
	// deployment two minutes after the replacement's first request.
	strayed := dep("strayed", now.Add(-20*time.Minute), now.Add(-16*time.Minute), now.Add(-8*time.Minute))

	tests := []struct {
		name              string
		deps              []sqlc.ListDeploymentsForCompareRow
		ok                bool
		cur, prev         string
		curStart, prevEnd time.Time
		prevStart         time.Time
		// ownStart/ownEnd: the predecessor's own window; zero means the
		// comparison is not side by side and it equals prev.
		ownStart, ownEnd time.Time
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
			ownStart: now.Add(-2*time.Second - 30*time.Minute + time.Millisecond), ownEnd: now.Add(-2*time.Second + time.Millisecond),
		},
		{
			name: "stable answered last", deps: []sqlc.ListDeploymentsForCompareRow{stable, canary}, ok: true,
			cur: "canary", prev: "stable", curStart: now.Add(-9 * time.Minute), prevStart: now.Add(-9 * time.Minute), prevEnd: now,
			ownStart: now.Add(-2*time.Second - 30*time.Minute + time.Millisecond), ownEnd: now.Add(-2*time.Second + time.Millisecond),
		},
		{
			// Replaced minutes ago, so both answered within the serving
			// window, but one after the other: the predecessor is compared
			// over its own last traffic, not the new deployment's window
			// where it has no rows (found on prod 2026-11-11: a 15x slowdown
			// went unreported for 28 minutes).
			name: "replaced minutes ago", deps: []sqlc.ListDeploymentsForCompareRow{replacement, replaced}, ok: true,
			cur: "replacement", prev: "replaced", curStart: now.Add(-10 * time.Minute),
			prevStart: now.Add(-12*time.Minute - 30*time.Minute + time.Millisecond), prevEnd: now.Add(-12*time.Minute + time.Millisecond),
		},
		{
			// The stray request makes the two overlap, so the shared window
			// is compared; the predecessor's own window is kept for routes
			// it did not serve there (found on prod 2026-11-14).
			name: "stray request after replacement", deps: []sqlc.ListDeploymentsForCompareRow{replacement, strayed}, ok: true,
			cur: "replacement", prev: "strayed", curStart: now.Add(-10 * time.Minute), prevStart: now.Add(-10 * time.Minute), prevEnd: now,
			ownStart: now.Add(-8*time.Minute - 30*time.Minute + time.Millisecond), ownEnd: now.Add(-8*time.Minute + time.Millisecond),
		},
		{name: "only one deployment", deps: []sqlc.ListDeploymentsForCompareRow{rolledOut}},
		{name: "nothing serving", deps: []sqlc.ListDeploymentsForCompareRow{old}},
	}
	names := map[string]string{
		old.DeploymentID.String(): "old", rolledOut.DeploymentID.String(): "new",
		stable.DeploymentID.String(): "stable", canary.DeploymentID.String(): "canary",
		replaced.DeploymentID.String(): "replaced", replacement.DeploymentID.String(): "replacement",
		strayed.DeploymentID.String(): "strayed",
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
			ownStart, ownEnd := tt.ownStart, tt.ownEnd
			if ownStart.IsZero() && tt.prevEnd.Equal(now) {
				t.Fatalf("test case %q must give the predecessor's own window", tt.name)
			}
			if ownStart.IsZero() {
				ownStart, ownEnd = tt.prevStart, tt.prevEnd
			}
			if !got.OwnStart.Equal(ownStart) || !got.OwnEnd.Equal(ownEnd) {
				t.Fatalf("own window [%s, %s), want [%s, %s)", got.OwnStart, got.OwnEnd, ownStart, ownEnd)
			}
		})
	}
}

// Side by side, the shared window's baseline wins for every route it has;
// routes the predecessor served only before the overlap take its own window.
func TestMergeRegressionBaselines(t *testing.T) {
	shared := []sqlc.RequestTelemetryBaselineP95ByRouteRow{{Route: "GET /healthz", P95Ms: 900, N: 1}}
	sharedErrs := map[string]debugger.RouteErrors{"GET /healthz": {Requests: 1}}
	own := []sqlc.RequestTelemetryBaselineP95ByRouteRow{
		{Route: "GET /", P95Ms: 17, N: 120},
		{Route: "GET /healthz", P95Ms: 5, N: 3},
	}
	ownErrs := map[string]debugger.RouteErrors{"GET /": {Requests: 120}, "GET /healthz": {Requests: 3, Errors: 1}}
	p95, errs := apidserver.MergeRegressionBaselines(shared, sharedErrs, own, ownErrs)
	got := apidserver.RegressionBaselineRoutes(p95, 200)
	if len(got) != 2 || got["GET /"] != 17 || got["GET /healthz"] != 900 {
		t.Fatalf("p95 = %v, want GET / from the own window and /healthz from the shared one", got)
	}
	if errs["GET /"].Requests != 120 || errs["GET /healthz"] != (debugger.RouteErrors{Requests: 1}) {
		t.Fatalf("errors = %v", errs)
	}
}
