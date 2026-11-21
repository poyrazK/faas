package referrals

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/referrals/sqlc"
	"time"
)

func (s *PostgresStore) QualificationProfile(ctx context.Context, scope Scope, lead string) (QualificationProfile, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return QualificationProfile{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.q.ActivityLead(ctx, tx, sqlc.ActivityLeadParams{LeadID: pgID(lead), ActorID: pgID(scope.OwnerID), AllOwners: scope.All})
	if errors.Is(err, pgx.ErrNoRows) {
		return QualificationProfile{}, ErrNotFound
	}
	if err != nil {
		return QualificationProfile{}, err
	}
	out := QualificationProfile{Lead: Prospect{ID: lead, ProspectInput: ProspectInput{Name: row.Name, Email: row.Email, Company: row.Company, Stage: row.Stage, Note: row.Note, OwnerID: idString(row.OwnerID), Version: row.Version}, OwnerEmail: row.OwnerEmail}, Qualification: Qualification{QualificationInput: QualificationInput{LeadVersion: row.Version, BuyingTimeline: "unknown", Priority: "unknown", ProductFit: "unknown"}}}
	q, err := s.q.GetQualification(ctx, tx, pgID(lead))
	if err == nil {
		due := ""
		if q.DecisionOn.Valid {
			due = q.DecisionOn.Time.Format(time.DateOnly)
		}
		updated := q.UpdatedAt.Time
		out.Qualification = Qualification{QualificationInput: QualificationInput{LeadVersion: row.Version, Version: q.Version, UseCase: q.UseCase, Workload: q.Workload, DecisionMaker: q.DecisionMaker, BuyingTimeline: q.BuyingTimeline, DecisionOn: due, Priority: q.Priority, ProductFit: q.ProductFit, Blocker: q.Blocker}, UpdatedBy: nullableID(q.UpdatedBy), UpdatedAt: &updated}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	event, err := s.q.LatestSalesOutcome(ctx, tx, pgID(lead))
	if err == nil {
		out.Outcome = &Outcome{Stage: event.Stage, Reason: event.Reason, Note: event.Note, ActorID: nullableID(event.ActorID), OwnerID: nullableID(event.OwnerID), LeadVersion: event.LeadVersion, RecordedAt: event.RecordedAt.Time}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PostgresStore) SaveQualification(ctx context.Context, scope Scope, lead string, in QualificationInput) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := s.q.LockActivityLead(ctx, tx, sqlc.LockActivityLeadParams{LeadID: pgID(lead), ActorID: pgID(scope.OwnerID), AllOwners: scope.All})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if current.Version != in.LeadVersion {
		return ErrConflict
	}
	old, err := s.q.GetQualification(ctx, tx, pgID(lead))
	if errors.Is(err, pgx.ErrNoRows) {
		if in.Version != 0 {
			return ErrConflict
		}
		err = s.q.InsertQualification(ctx, tx, sqlc.InsertQualificationParams{LeadID: pgID(lead), UseCase: in.UseCase, Workload: in.Workload, DecisionMaker: in.DecisionMaker, BuyingTimeline: in.BuyingTimeline, DecisionOn: dateValue(in.DecisionOn), Priority: in.Priority, ProductFit: in.ProductFit, Blocker: in.Blocker, UpdatedBy: pgID(scope.OwnerID)})
	} else if err == nil {
		if old.Version != in.Version {
			return ErrConflict
		}
		var n int64
		n, err = s.q.UpdateQualification(ctx, tx, sqlc.UpdateQualificationParams{LeadID: pgID(lead), UseCase: in.UseCase, Workload: in.Workload, DecisionMaker: in.DecisionMaker, BuyingTimeline: in.BuyingTimeline, DecisionOn: dateValue(in.DecisionOn), Priority: in.Priority, ProductFit: in.ProductFit, Blocker: in.Blocker, UpdatedBy: pgID(scope.OwnerID), Version: in.Version})
		if err == nil && n != 1 {
			return ErrConflict
		}
	}
	if err != nil {
		return fmt.Errorf("save lead qualification: %w", err)
	}
	return tx.Commit(ctx)
}
func (s *PostgresStore) QualificationOverview(ctx context.Context, scope Scope, f QualificationFilter) ([]QualificationGap, QualificationCounts, []OutcomeCount, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, QualificationCounts{}, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if f.After == "" {
		f.After = uuid.Nil.String()
	}
	rows, err := s.q.QualificationGaps(ctx, tx, sqlc.QualificationGapsParams{ActorID: pgID(scope.OwnerID), AllOwners: scope.All, OwnerFilter: f.Owner, AfterID: pgID(f.After), RowLimit: f.Limit})
	if err != nil {
		return nil, QualificationCounts{}, nil, err
	}
	out := make([]QualificationGap, 0, len(rows))
	for _, r := range rows {
		missing := []string{}
		if r.UseCase == "" {
			missing = append(missing, "use_case")
		}
		if r.Workload == "" {
			missing = append(missing, "workload")
		}
		if r.DecisionMaker == "" {
			missing = append(missing, "decision_maker")
		}
		if r.BuyingTimeline == "unknown" {
			missing = append(missing, "buying_timeline")
		}
		out = append(out, QualificationGap{ID: idString(r.ID), Name: r.Name, Email: r.Email, Stage: r.Stage, OwnerID: idString(r.OwnerID), OwnerEmail: r.OwnerEmail, Captured: r.Captured, Missing: missing})
	}
	c, err := s.q.QualificationCounts(ctx, tx, sqlc.QualificationCountsParams{ActorID: pgID(scope.OwnerID), AllOwners: scope.All, OwnerFilter: f.Owner})
	if err != nil {
		return nil, QualificationCounts{}, nil, err
	}
	counts := QualificationCounts{Open: c.Open, Complete: c.Complete, MissingUseCase: c.MissingUseCase, MissingWorkload: c.MissingWorkload, MissingDecisionMaker: c.MissingDecisionMaker, MissingTimeline: c.MissingTimeline, UnclassifiedWon: c.UnclassifiedWon, UnclassifiedLost: c.UnclassifiedLost}
	events, err := s.q.QualificationOutcomeCounts(ctx, tx, sqlc.QualificationOutcomeCountsParams{ActorID: pgID(scope.OwnerID), AllOwners: scope.All, OwnerFilter: f.Owner})
	if err != nil {
		return nil, counts, nil, err
	}
	reasons := make([]OutcomeCount, 0, len(events))
	for _, r := range events {
		reasons = append(reasons, OutcomeCount{Stage: r.Stage, Reason: r.Reason, Count: r.Count})
	}
	return out, counts, reasons, tx.Commit(ctx)
}
