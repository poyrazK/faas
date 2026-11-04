package referrals

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/referrals/sqlc"
)

func messageTemplate(r sqlc.SalesMessageTemplate) MessageTemplate {
	return MessageTemplate{ID: idString(r.ID), MessageTemplateInput: MessageTemplateInput{Title: r.Title, Category: r.Category, Stage: r.Stage, Status: r.Status, Subject: r.Subject, Body: r.Body, Version: r.Version}, UpdatedAt: r.UpdatedAt.Time}
}
func (s *PostgresStore) MessageTemplate(ctx context.Context, scope Scope, id string) (MessageTemplate, error) {
	r, err := s.q.GetMessageTemplate(ctx, s.pool, sqlc.GetMessageTemplateParams{ID: pgID(id), Manager: scope.All})
	if errors.Is(err, pgx.ErrNoRows) {
		return MessageTemplate{}, ErrNotFound
	}
	return messageTemplate(r), err
}
func (s *PostgresStore) MessageTemplates(ctx context.Context, scope Scope, f TemplateFilter) ([]MessageTemplate, error) {
	if f.After == "" {
		f.After = uuid.Nil.String()
	}
	rows, err := s.q.ListMessageTemplates(ctx, s.pool, sqlc.ListMessageTemplatesParams{Manager: scope.All, AfterID: pgID(f.After), RowLimit: f.Limit})
	if err != nil {
		return nil, fmt.Errorf("list message templates: %w", err)
	}
	out := make([]MessageTemplate, 0, len(rows))
	for _, r := range rows {
		out = append(out, messageTemplate(r))
	}
	return out, nil
}
func (s *PostgresStore) SaveMessageTemplate(ctx context.Context, scope Scope, id string, in MessageTemplateInput) (string, error) {
	if !scope.All {
		return "", ErrNotFound
	}
	if err := ValidateMessageTemplate(in); err != nil {
		return "", err
	}
	if id == "" {
		if in.Version != 0 {
			return "", ErrConflict
		}
		saved, err := s.q.InsertMessageTemplate(ctx, s.pool, sqlc.InsertMessageTemplateParams{ID: pgID(uuid.Must(uuid.NewV7()).String()), Title: in.Title, Category: in.Category, Stage: in.Stage, Status: in.Status, Subject: in.Subject, Body: in.Body, ActorID: pgID(scope.OwnerID), Manager: scope.All})
		return pipelineResult(saved, err, false)
	}
	saved, err := s.q.UpdateMessageTemplate(ctx, s.pool, sqlc.UpdateMessageTemplateParams{ID: pgID(id), Title: in.Title, Category: in.Category, Stage: in.Stage, Status: in.Status, Subject: in.Subject, Body: in.Body, ActorID: pgID(scope.OwnerID), Version: in.Version, Manager: scope.All})
	return pipelineResult(saved, err, true)
}
func (s *PostgresStore) MessageTemplateSource(ctx context.Context, scope Scope, lead, id string, version int64) (TemplateSource, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return TemplateSource{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	l, err := s.q.TemplateLead(ctx, tx, sqlc.TemplateLeadParams{LeadID: pgID(lead), ActorID: pgID(scope.OwnerID), AllOwners: scope.All})
	if errors.Is(err, pgx.ErrNoRows) {
		return TemplateSource{}, ErrNotFound
	}
	if err != nil {
		return TemplateSource{}, err
	}
	t, err := s.q.GetMessageTemplate(ctx, tx, sqlc.GetMessageTemplateParams{ID: pgID(id), Manager: scope.All})
	if errors.Is(err, pgx.ErrNoRows) {
		return TemplateSource{}, ErrNotFound
	}
	if err != nil {
		return TemplateSource{}, err
	}
	if t.Version != version {
		return TemplateSource{}, ErrConflict
	}
	out := TemplateSource{Template: messageTemplate(t), Lead: TemplateLead{ID: idString(l.ID), Name: l.Name, Email: l.Email, Company: l.Company, Stage: l.Stage, OwnerEmail: l.OwnerEmail, Version: l.Version, ReferralCode: l.ReferralCode}}
	return out, tx.Commit(ctx)
}
