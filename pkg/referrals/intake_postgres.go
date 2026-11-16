package referrals

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/referrals/sqlc"
)

func nullableID(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return idString(id)
}
func inquiryInput(r sqlc.SalesInquiry) InquiryInput {
	return InquiryInput{Name: r.Name, Email: r.Email, Company: r.Company, Intent: r.Intent, Message: r.Message, Code: r.RequestedCode}
}

func (s *PostgresStore) SubmitInquiry(ctx context.Context, nonce string, expires time.Time, in InquiryInput, options IntakeOptions) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	last, err := s.q.LockSalesIntake(ctx, tx)
	if err != nil {
		return err
	}
	if err = s.q.PurgeIntakeReceipts(ctx, tx, api.SalesIntakeReceiptPurge); err != nil {
		return err
	}
	n, err := s.q.ClaimIntakeReceipt(ctx, tx, sqlc.ClaimIntakeReceiptParams{Nonce: pgID(nonce), ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}})
	if err != nil {
		return err
	}
	if n == 0 {
		return tx.Commit(ctx)
	}
	existing, err := s.q.ActiveSalesInquiry(ctx, tx, in.Email)
	if err == nil {
		err = s.repeatInquiry(ctx, tx, existing, in.Message, options)
	} else if errors.Is(err, pgx.ErrNoRows) {
		err = s.newInquiry(ctx, tx, last, in, options)
	}
	if err != nil {
		return fmt.Errorf("capture sales inquiry: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) newInquiry(ctx context.Context, tx pgx.Tx, last pgtype.UUID, in InquiryInput, o IntakeOptions) error {
	owner, lead, reason, err := s.routeInquiry(ctx, tx, last, in, o)
	if err != nil {
		return err
	}
	var task pgtype.UUID
	status := "pending"
	if reason == "" {
		lead, task, err = s.promoteInquiry(ctx, tx, in, owner, lead, o.DueOn)
		if err != nil {
			return err
		}
		status = "assigned"
	}
	return s.q.InsertSalesInquiry(ctx, tx, sqlc.InsertSalesInquiryParams{ID: pgID(uuid.Must(uuid.NewV7()).String()), Name: in.Name, Email: in.Email, Company: in.Company, Intent: in.Intent, Message: in.Message, RequestedCode: in.Code, Status: status, Reason: reason, LeadID: lead, TaskID: task})
}

func (s *PostgresStore) routeInquiry(ctx context.Context, tx pgx.Tx, last pgtype.UUID, in InquiryInput, o IntakeOptions) (owner, lead pgtype.UUID, reason string, err error) {
	team, e := s.q.SalesTeam(ctx, tx, o.MemberEmails)
	if e != nil {
		err = e
		return
	}
	eligible := func(id pgtype.UUID) bool {
		return slices.ContainsFunc(team, func(r sqlc.SalesTeamRow) bool { return r.ID == id })
	}
	if in.Code != "" {
		link, e := s.q.GetLink(ctx, tx, pgtype.Text{String: in.Code, Valid: true})
		if errors.Is(e, pgx.ErrNoRows) {
			reason = "code_unavailable"
			return
		}
		if e != nil {
			err = e
			return
		}
		if !link.Active || link.Kind != "sales" || !eligible(link.OwnerID) {
			reason = "code_unavailable"
			return
		}
		owner = link.OwnerID
	}
	leads, e := s.q.SalesInquiryLeads(ctx, tx, in.Email)
	if e != nil {
		err = e
		return
	}
	if len(leads) > 0 {
		if len(leads) > 1 {
			reason = "multiple_leads"
			return
		}
		if !eligible(leads[0].OwnerID) {
			reason = "owner_unavailable"
			return
		}
		if owner.Valid && owner != leads[0].OwnerID {
			reason = "owner_conflict"
			return
		}
		owner, lead = leads[0].OwnerID, leads[0].ID
		return
	}
	if owner.Valid {
		return
	}
	reps, e := s.q.SalesTeam(ctx, tx, o.RepEmails)
	if e != nil {
		err = e
		return
	}
	if len(reps) == 0 {
		reason = "no_reps"
		return
	}
	index := 0
	for i, r := range reps {
		if r.ID == last {
			index = (i + 1) % len(reps)
			break
		}
	}
	owner = reps[index].ID
	err = s.q.AdvanceSalesIntake(ctx, tx, owner)
	return
}

func (s *PostgresStore) promoteInquiry(ctx context.Context, tx pgx.Tx, in InquiryInput, owner, lead pgtype.UUID, due string) (pgtype.UUID, pgtype.UUID, error) {
	var err error
	if !lead.Valid {
		lead, err = s.q.CreateIntakeLead(ctx, tx, sqlc.CreateIntakeLeadParams{ID: pgID(uuid.Must(uuid.NewV7()).String()), OwnerID: owner, Name: in.Name, Email: in.Email, Company: in.Company, Note: "Website " + in.Intent + " inquiry (unverified):\n" + in.Message})
		if err != nil {
			return lead, pgtype.UUID{}, err
		}
	}
	task, err := s.q.CreateIntakeTask(ctx, tx, sqlc.CreateIntakeTaskParams{ID: pgID(uuid.NewString()), LeadID: lead, Title: "Review website " + in.Intent + " inquiry", DueOn: dateValue(due)})
	return lead, task, err
}

func (s *PostgresStore) repeatInquiry(ctx context.Context, tx pgx.Tx, r sqlc.SalesInquiry, message string, o IntakeOptions) error {
	if err := s.q.RepeatSalesInquiry(ctx, tx, sqlc.RepeatSalesInquiryParams{ID: r.ID, LatestMessage: message}); err != nil {
		return err
	}
	if r.Status != "assigned" {
		return nil
	}
	lead, err := s.q.IntakeLead(ctx, tx, r.LeadID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	reason := ""
	if errors.Is(err, pgx.ErrNoRows) || lead.Email != r.Email {
		reason = "lead_unavailable"
	} else {
		team, e := s.q.SalesTeam(ctx, tx, o.MemberEmails)
		if e != nil {
			return e
		}
		if !slices.ContainsFunc(team, func(rep sqlc.SalesTeamRow) bool { return rep.ID == lead.OwnerID }) {
			reason = "owner_unavailable"
		}
	}
	if reason != "" {
		return s.q.RouteSalesInquiry(ctx, tx, sqlc.RouteSalesInquiryParams{ID: r.ID, Status: "pending", Reason: reason, LeadID: r.LeadID, TaskID: r.TaskID})
	}
	open, err := s.q.IntakeTaskOpen(ctx, tx, r.TaskID)
	if err != nil {
		return err
	}
	if open {
		return nil
	}
	_, task, err := s.promoteInquiry(ctx, tx, inquiryInput(r), lead.OwnerID, lead.ID, o.DueOn)
	if err != nil {
		return err
	}
	return s.q.RouteSalesInquiry(ctx, tx, sqlc.RouteSalesInquiryParams{ID: r.ID, Status: "assigned", Reason: "", LeadID: lead.ID, TaskID: task})
}

func (s *PostgresStore) AssignInquiry(ctx context.Context, id string, version int64, owner string, o IntakeOptions) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = s.q.LockSalesIntake(ctx, tx); err != nil {
		return err
	}
	r, err := s.q.SalesInquiryByID(ctx, tx, pgID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if r.Version != version || r.Status == "closed" {
		return ErrConflict
	}
	if r.Status == "assigned" && r.LeadID.Valid {
		return ErrConflict
	}
	team, err := s.q.SalesTeam(ctx, tx, o.MemberEmails)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(team, func(rep sqlc.SalesTeamRow) bool { return idString(rep.ID) == owner }) {
		return ErrNotFound
	}
	lead, task, err := s.promoteInquiry(ctx, tx, inquiryInput(r), pgID(owner), pgtype.UUID{}, o.DueOn)
	if err != nil {
		return err
	}
	err = s.q.RouteSalesInquiry(ctx, tx, sqlc.RouteSalesInquiryParams{ID: r.ID, Status: "assigned", Reason: "", LeadID: lead, TaskID: task})
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) CloseInquiry(ctx context.Context, scope Scope, id string, version int64) (bool, error) {
	n, err := s.q.CloseSalesInquiry(ctx, s.pool, sqlc.CloseSalesInquiryParams{ID: pgID(id), Version: version, AllOwners: scope.All, ActorID: pgID(scope.OwnerID)})
	return n > 0, err
}
func (s *PostgresStore) Inquiries(ctx context.Context, scope Scope, f InquiryFilter) ([]Inquiry, error) {
	if f.After == "" {
		f.After = uuid.Nil.String()
	}
	rows, err := s.q.ListSalesInquiries(ctx, s.pool, sqlc.ListSalesInquiriesParams{ActorID: pgID(scope.OwnerID), AllOwners: scope.All, StatusFilter: f.Status, AfterID: pgID(f.After), RowLimit: f.Limit})
	if err != nil {
		return nil, err
	}
	out := make([]Inquiry, 0, len(rows))
	for _, r := range rows {
		reason := r.Reason
		if r.Status == "assigned" && !r.LeadID.Valid {
			reason = "lead_unavailable"
		}
		owner := ""
		if r.OwnerEmail != "" {
			owner = idString(r.OwnerID)
		}
		out = append(out, Inquiry{InquiryInput: InquiryInput{Name: r.Name, Email: r.Email, Company: r.Company, Intent: r.Intent, Message: r.Message, Code: r.RequestedCode}, ID: idString(r.ID), Status: r.EffectiveStatus, Reason: reason, LeadID: nullableID(r.LeadID), TaskID: nullableID(r.TaskID), OwnerID: owner, OwnerEmail: r.OwnerEmail, LatestMessage: r.LatestMessage, RepeatCount: r.RepeatCount, Version: r.Version, CreatedAt: r.CreatedAt.Time, LastReceivedAt: r.LastReceivedAt.Time})
	}
	return out, nil
}
