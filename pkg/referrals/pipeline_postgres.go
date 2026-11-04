package referrals

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/referrals/sqlc"
)

func idString(id pgtype.UUID) string { return uuid.UUID(id.Bytes).String() }
func dateValue(value string) pgtype.Date {
	at, err := time.Parse(time.DateOnly, value)
	return pgtype.Date{Time: at, Valid: err == nil}
}
func pipelineResult(id pgtype.UUID, err error, update bool) (string, error) {
	var conflict *pgconn.PgError
	if errors.As(err, &conflict) && conflict.Code == "23505" {
		if conflict.ConstraintName == "sales_leads_owner_id_email_key" {
			return "", ErrDuplicateLead
		}
		return "", ErrConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if update {
			return "", ErrConflict
		}
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("save sales record: %w", err)
	}
	return idString(id), nil
}

func (s *PostgresStore) SalesTeam(ctx context.Context, emails []string) ([]Rep, error) {
	rows, err := s.q.SalesTeam(ctx, s.pool, emails)
	if err != nil {
		return nil, fmt.Errorf("read sales team: %w", err)
	}
	out := make([]Rep, 0, len(rows))
	for _, r := range rows {
		out = append(out, Rep{ID: idString(r.ID), Email: r.Email})
	}
	return out, nil
}

func (s *PostgresStore) Prospects(ctx context.Context, scope Scope, f PipelineFilter) ([]Prospect, []StageCount, error) {
	if f.After == "" {
		f.After = uuid.Nil.String()
	}
	rows, err := s.q.ListProspects(ctx, s.pool, sqlc.ListProspectsParams{ActorID: pgID(scope.OwnerID), AllOwners: scope.All, OwnerFilter: f.Owner, Search: f.Query, Stage: f.Stage, AfterID: pgID(f.After), RowLimit: f.Limit})
	if err != nil {
		return nil, nil, fmt.Errorf("list sales leads: %w", err)
	}
	out := make([]Prospect, 0, len(rows))
	for _, r := range rows {
		out = append(out, Prospect{ID: idString(r.ID), ProspectInput: ProspectInput{Name: r.Name, Email: r.Email, Company: r.Company, Stage: r.Stage, Note: r.Note, OwnerID: idString(r.OwnerID), Version: r.Version}, OwnerEmail: r.OwnerEmail, Registered: r.Registered, Paid: r.Paid, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time})
	}
	counts, err := s.q.ProspectCounts(ctx, s.pool, sqlc.ProspectCountsParams{ActorID: pgID(scope.OwnerID), AllOwners: scope.All, OwnerFilter: f.Owner, Search: f.Query})
	if err != nil {
		return nil, nil, fmt.Errorf("count sales leads: %w", err)
	}
	summary := make([]StageCount, 0, len(counts))
	for _, r := range counts {
		summary = append(summary, StageCount{Stage: r.Stage, Count: r.Count})
	}
	return out, summary, nil
}

func (s *PostgresStore) SaveProspect(ctx context.Context, scope Scope, actor, id string, in ProspectInput) (string, error) {
	if actor != scope.OwnerID {
		return "", ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	previous := ""
	version := int64(1)
	if id != "" {
		current, e := s.q.LockProspectOutcome(ctx, tx, sqlc.LockProspectOutcomeParams{LeadID: pgID(id), ActorID: pgID(actor), AllOwners: scope.All})
		if errors.Is(e, pgx.ErrNoRows) {
			return "", ErrConflict
		}
		if e != nil {
			return "", e
		}
		if current.Version != in.Version {
			return "", ErrConflict
		}
		previous = current.Stage
		version = current.Version + 1
	}
	if err = validateClosing(previous, in); err != nil {
		return "", err
	}
	saved, err := s.saveProspectRow(ctx, tx, scope, actor, id, in)
	if err != nil {
		return "", err
	}
	if in.CloseReason != "" {
		err = s.q.InsertSalesOutcome(ctx, tx, sqlc.InsertSalesOutcomeParams{ID: pgID(uuid.Must(uuid.NewV7()).String()), LeadID: pgID(saved), LeadVersion: version, Stage: in.Stage, Reason: in.CloseReason, Note: in.CloseNote, ActorID: pgID(actor), OwnerID: pgID(in.OwnerID)})
		if err != nil {
			return "", fmt.Errorf("record closing reason: %w", err)
		}
	}
	return saved, tx.Commit(ctx)
}
func validateClosing(previous string, in ProspectInput) error {
	if in.CloseReason != "" && (!ValidOutcomeReason(in.Stage, in.CloseReason) || (in.CloseReason == "other" && in.CloseNote == "")) {
		return ErrInvalidOutcome
	}
	if in.CloseReason == "" && in.CloseNote != "" {
		return ErrInvalidOutcome
	}
	if ClosedStage(in.Stage) && previous != in.Stage && in.CloseReason == "" {
		return ErrOutcomeRequired
	}
	return nil
}
func (s *PostgresStore) saveProspectRow(ctx context.Context, tx pgx.Tx, scope Scope, actor, id string, in ProspectInput) (string, error) {
	if id == "" {
		result, err := s.q.CreateProspect(ctx, tx, sqlc.CreateProspectParams{AllOwners: scope.All, ID: pgID(uuid.Must(uuid.NewV7()).String()), OwnerID: pgID(in.OwnerID), Name: in.Name, Email: in.Email, Company: in.Company, Stage: in.Stage, Note: in.Note, ActorID: pgID(actor)})
		return pipelineResult(result, err, false)
	}
	result, err := s.q.UpdateProspect(ctx, tx, sqlc.UpdateProspectParams{ID: pgID(id), OwnerID: pgID(in.OwnerID), Name: in.Name, Email: in.Email, Company: in.Company, Stage: in.Stage, Note: in.Note, ActorID: pgID(actor), AllOwners: scope.All, Version: in.Version})
	return pipelineResult(result, err, true)
}

func (s *PostgresStore) DeleteProspect(ctx context.Context, scope Scope, id string, version int64) (bool, error) {
	n, err := s.q.DeleteProspect(ctx, s.pool, sqlc.DeleteProspectParams{ID: pgID(id), Version: version, ActorID: pgID(scope.OwnerID), AllOwners: scope.All})
	if err != nil {
		return false, fmt.Errorf("delete sales lead: %w", err)
	}
	return n > 0, nil
}

func (s *PostgresStore) Tasks(ctx context.Context, scope Scope, f TaskFilter) ([]Task, TaskCounts, error) {
	if f.AfterID == "" {
		f.AfterID = uuid.Nil.String()
		f.AfterDate = "0001-01-01"
	}
	rows, err := s.q.ListSalesTasks(ctx, s.pool, sqlc.ListSalesTasksParams{ActorID: pgID(scope.OwnerID), AllOwners: scope.All, OwnerFilter: f.Owner, Status: f.Status, DueFilter: f.Due, OnDate: dateValue(f.On), AfterDate: dateValue(f.AfterDate), AfterID: pgID(f.AfterID), RowLimit: f.Limit})
	if err != nil {
		return nil, TaskCounts{}, fmt.Errorf("list sales tasks: %w", err)
	}
	out := make([]Task, 0, len(rows))
	for _, r := range rows {
		out = append(out, Task{ID: idString(r.ID), TaskInput: TaskInput{LeadID: idString(r.LeadID), Title: r.Title, DueOn: r.DueOn.Time.Format(time.DateOnly), Done: r.CompletedAt.Valid, Version: r.Version}, LeadName: r.LeadName, LeadEmail: r.LeadEmail, OwnerID: idString(r.OwnerID), OwnerEmail: r.OwnerEmail})
	}
	c, err := s.q.SalesTaskCounts(ctx, s.pool, sqlc.SalesTaskCountsParams{ActorID: pgID(scope.OwnerID), AllOwners: scope.All, OwnerFilter: f.Owner, OnDate: dateValue(f.On)})
	if err != nil {
		return nil, TaskCounts{}, fmt.Errorf("count sales tasks: %w", err)
	}
	return out, TaskCounts{Open: c.Open, Today: c.Today, Overdue: c.Overdue}, nil
}

func (s *PostgresStore) SaveTask(ctx context.Context, scope Scope, actor, id string, in TaskInput) (string, error) {
	if actor != scope.OwnerID {
		return "", ErrNotFound
	}
	if id == "" {
		result, err := s.q.CreateSalesTask(ctx, s.pool, sqlc.CreateSalesTaskParams{ID: pgID(uuid.NewString()), LeadID: pgID(in.LeadID), Title: in.Title, DueOn: dateValue(in.DueOn), ActorID: pgID(actor), AllOwners: scope.All})
		return pipelineResult(result, err, false)
	}
	result, err := s.q.UpdateSalesTask(ctx, s.pool, sqlc.UpdateSalesTaskParams{ID: pgID(id), Title: in.Title, DueOn: dateValue(in.DueOn), Done: in.Done, Version: in.Version, ActorID: pgID(actor), AllOwners: scope.All})
	return pipelineResult(result, err, true)
}
