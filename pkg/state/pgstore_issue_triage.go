package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) CompareIssueReleases(ctx context.Context, account, app, baseline, candidate, environment string, now time.Time) (api.IssueReleaseComparison, error) {
	out := api.IssueReleaseComparison{WindowStart: now.Add(-api.IssueImpactSummaryWindow), WindowEnd: now, Environment: environment}
	base, e1 := uuid.Parse(baseline)
	can, e2 := uuid.Parse(candidate)
	if e1 != nil || e2 != nil || base == can || base == uuid.Nil || can == uuid.Nil || environment == "" {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	for i, id := range []string{base.String(), can.String()} {
		r, err := q.IssueReleaseHealth(ctx, tx, sqlc.IssueReleaseHealthParams{AccountID: issueUUID(account), AppID: issueUUID(app), DeploymentID: issueUUID(id), Environment: environment, WindowStart: issueTime(out.WindowStart), WindowEnd: issueTime(out.WindowEnd)})
		if err != nil {
			return out, mapErr(err)
		}
		h := api.IssueReleaseHealth{DeploymentID: id, CommitSHA: r.CommitSha.String, CreatedAt: r.CreatedAt.Time, ObservedRequests: r.ObservedRequests, HTTP5xxRequests: r.Http5xxRequests, ObservedIssueEvents: r.ObservedIssueEvents, ObservedIssueGroups: r.ObservedIssueGroups, NewIssueGroups: r.NewIssueGroups, RegressedIssueGroups: r.RegressedIssueGroups, IdentifiedCustomers: r.IdentifiedCustomers, UnattributedEvents: r.UnattributedEvents, RequestCoverage: "unknown"}
		if h.ObservedRequests > 0 {
			rate := float64(h.HTTP5xxRequests) / float64(h.ObservedRequests)
			h.HTTP5xxRate = &rate
			h.RequestCoverage = "observed"
		}
		if i == 0 {
			out.Baseline = h
		} else {
			out.Candidate = h
		}
	}
	if out.Baseline.HTTP5xxRate != nil && out.Candidate.HTTP5xxRate != nil {
		delta := *out.Candidate.HTTP5xxRate - *out.Baseline.HTTP5xxRate
		out.HTTP5xxRateDelta = &delta
	}
	return out, tx.Commit(ctx)
}

// BulkActOnIssues shares the same app lock and transaction as single-item triage.
// Any missing issue or incompatible resolution rolls back the complete batch.
func (s *PgStore) BulkActOnIssues(ctx context.Context, app, actor string, in api.BulkIssueActionRequest, now time.Time) (api.BulkIssueActionResponse, error) {
	out := api.BulkIssueActionResponse{Items: []api.Issue{}}
	in, err := api.NormalizeBulkIssueAction(in, now)
	if err != nil {
		return out, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.IssueLockApp(ctx, tx, issueUUID(app)); err != nil {
		return out, mapErr(err)
	}
	seen := map[string]bool{}
	for _, id := range in.IssueIDs {
		canonical, err := q.IssueCanonicalID(ctx, tx, sqlc.IssueCanonicalIDParams{AppID: issueUUID(app), ID: issueUUID(id)})
		if err != nil {
			return api.BulkIssueActionResponse{}, mapErr(err)
		}
		key := issueID(canonical)
		if seen[key] {
			continue
		}
		seen[key] = true
		item, err := actOnIssueTx(ctx, q, tx, app, id, actor, in.Action, now)
		if err != nil {
			return api.BulkIssueActionResponse{}, err
		}
		out.Items = append(out.Items, item)
	}
	return out, tx.Commit(ctx)
}
