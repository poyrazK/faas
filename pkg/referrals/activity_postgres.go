package referrals

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/referrals/sqlc"
)

func (s *PostgresStore) LeadActivity(ctx context.Context, scope Scope, lead string, f ActivityFilter) (Prospect, []Activity, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Prospect{}, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.q.ActivityLead(ctx, tx, sqlc.ActivityLeadParams{LeadID: pgID(lead), ActorID: pgID(scope.OwnerID), AllOwners: scope.All})
	if errors.Is(err, pgx.ErrNoRows) {
		return Prospect{}, nil, ErrNotFound
	}
	if err != nil {
		return Prospect{}, nil, err
	}
	if f.AfterID == "" {
		f.AfterID = uuid.Nil.String()
		f.AfterAt = time.Time{}
	}
	rows, err := s.q.ListSalesActivities(ctx, tx, sqlc.ListSalesActivitiesParams{LeadID: pgID(lead), ActorID: pgID(scope.OwnerID), AllOwners: scope.All, AfterID: pgID(f.AfterID), AfterAt: pgtype.Timestamptz{Time: f.AfterAt, Valid: true}, RowLimit: f.Limit})
	if err != nil {
		return Prospect{}, nil, fmt.Errorf("read lead activity: %w", err)
	}
	out := make([]Activity, 0, len(rows))
	for _, r := range rows {
		due := ""
		if r.FollowUpOn.Valid {
			due = r.FollowUpOn.Time.Format(time.DateOnly)
		}
		out = append(out, Activity{ActivityInput: ActivityInput{ID: idString(r.ID), Kind: r.Kind, Summary: r.Summary, Outcome: r.Outcome, NextStep: r.NextStep, OccurredAt: r.OccurredAt.Time, FollowUpTitle: r.FollowUpTitle, FollowUpOn: due}, ActorID: nullableID(r.ActorID), ActorEmail: r.ActorEmail, RecordedAt: r.RecordedAt.Time, TaskID: nullableID(r.TaskID)})
	}
	prospect := Prospect{ID: idString(row.ID), ProspectInput: ProspectInput{Name: row.Name, Email: row.Email, Company: row.Company, Stage: row.Stage, Note: row.Note, OwnerID: idString(row.OwnerID), Version: row.Version}, OwnerEmail: row.OwnerEmail, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
	return prospect, out, tx.Commit(ctx)
}
func activityInput(r sqlc.SalesActivity) ActivityInput {
	due := ""
	if r.FollowUpOn.Valid {
		due = r.FollowUpOn.Time.Format(time.DateOnly)
	}
	return ActivityInput{ID: idString(r.ID), Kind: r.Kind, Summary: r.Summary, Outcome: r.Outcome, NextStep: r.NextStep, OccurredAt: r.OccurredAt.Time, FollowUpTitle: r.FollowUpTitle, FollowUpOn: due}
}
func sameActivity(r sqlc.SalesActivity, in ActivityInput, lead, actor string) bool {
	old := activityInput(r)
	return nullableID(r.LeadID) == lead && nullableID(r.ActorID) == actor && old.Kind == in.Kind && old.Summary == in.Summary && old.Outcome == in.Outcome && old.NextStep == in.NextStep && old.OccurredAt.Equal(in.OccurredAt) && old.FollowUpTitle == in.FollowUpTitle && old.FollowUpOn == in.FollowUpOn
}
func (s *PostgresStore) RecordActivity(ctx context.Context, scope Scope, lead string, in ActivityInput) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.q.LockActivityLead(ctx, tx, sqlc.LockActivityLeadParams{LeadID: pgID(lead), ActorID: pgID(scope.OwnerID), AllOwners: scope.All})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	existing, err := s.q.ActivityByID(ctx, tx, pgID(in.ID))
	if err == nil {
		if !sameActivity(existing, in, lead, scope.OwnerID) {
			return "", ErrConflict
		}
		return in.ID, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if row.Version != in.LeadVersion {
		return "", ErrConflict
	}
	var task pgtype.UUID
	if in.FollowUpTitle != "" {
		task, err = s.q.CreateSalesTask(ctx, tx, sqlc.CreateSalesTaskParams{ID: pgID(uuid.NewString()), LeadID: pgID(lead), Title: in.FollowUpTitle, DueOn: dateValue(in.FollowUpOn), ActorID: pgID(scope.OwnerID), AllOwners: scope.All})
		if err != nil {
			return "", fmt.Errorf("create activity follow-up: %w", err)
		}
	}
	err = s.q.InsertSalesActivity(ctx, tx, sqlc.InsertSalesActivityParams{ID: pgID(in.ID), LeadID: pgID(lead), ActorID: pgID(scope.OwnerID), Kind: in.Kind, Summary: in.Summary, Outcome: in.Outcome, NextStep: in.NextStep, OccurredAt: pgtype.Timestamptz{Time: in.OccurredAt, Valid: true}, FollowUpTitle: in.FollowUpTitle, FollowUpOn: dateValue(in.FollowUpOn), TaskID: task})
	if err != nil {
		return "", fmt.Errorf("record sales activity: %w", err)
	}
	return in.ID, tx.Commit(ctx)
}
func (s *PostgresStore) LeadAlerts(ctx context.Context, scope Scope, f AlertFilter) ([]LeadAlert, AlertCounts, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, AlertCounts{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if f.After == "" {
		f.After = uuid.Nil.String()
	}
	rows, err := s.q.ListLeadAlerts(ctx, tx, sqlc.ListLeadAlertsParams{ActorID: pgID(scope.OwnerID), AllOwners: scope.All, OwnerFilter: f.Owner, KindFilter: f.Kind, OnDate: dateValue(f.On), AsOf: pgtype.Timestamptz{Time: f.AsOf, Valid: true}, StaleDays: f.StaleDays, AfterID: pgID(f.After), RowLimit: f.Limit})
	if err != nil {
		return nil, AlertCounts{}, fmt.Errorf("read lead alerts: %w", err)
	}
	out := make([]LeadAlert, 0, len(rows))
	for _, r := range rows {
		due := ""
		if r.EarliestDueOn.Valid {
			due = r.EarliestDueOn.Time.Format(time.DateOnly)
		}
		out = append(out, LeadAlert{ID: idString(r.ID), Name: r.Name, Email: r.Email, Company: r.Company, Stage: r.Stage, OwnerID: idString(r.OwnerID), OwnerEmail: r.OwnerEmail, LastContactAt: r.LastContactAt.Time, HasContact: r.HasContact, Stale: r.Stale, OverdueTasks: r.OverdueTasks, EarliestDueOn: due})
	}
	c, err := s.q.LeadAlertCounts(ctx, tx, sqlc.LeadAlertCountsParams{ActorID: pgID(scope.OwnerID), AllOwners: scope.All, OwnerFilter: f.Owner, OnDate: dateValue(f.On), AsOf: pgtype.Timestamptz{Time: f.AsOf, Valid: true}, StaleDays: f.StaleDays})
	if err != nil {
		return nil, AlertCounts{}, fmt.Errorf("count lead alerts: %w", err)
	}
	return out, AlertCounts{Leads: c.Leads, Stale: c.Stale, OverdueLeads: c.OverdueLeads, OverdueTasks: c.OverdueTasks}, tx.Commit(ctx)
}
