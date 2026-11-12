package support

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/support/sqlc"
)

type Service struct {
	Pool     *pgxpool.Pool
	Operator func(string, string) bool
	Now      func() time.Time
	Webhook  *Webhook
}
type session struct {
	s     *Service
	ctx   context.Context
	tx    pgx.Tx
	q     *q.Queries
	actor Actor
	now   time.Time
	scope Scope
	role  string
}

func newID() string            { return uuid.NewString() }
func uid(s string) pgtype.UUID { v := pgtype.UUID{}; _ = v.Scan(s); return v }
func id(v pgtype.UUID) string {
	if !v.Valid {
		return ""
	}
	return uuid.UUID(v.Bytes).String()
}
func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
func raw(v any) []byte                  { b, _ := json.Marshal(v); return b }
func invalid(s string) error {
	return api.NewProblem(400, "support_invalid", "Invalid support request", s)
}
func conflict(s string) error {
	return api.NewProblem(409, "support_conflict", "Support state changed", s)
}
func forbidden() error {
	return api.NewProblem(403, "support_forbidden", "Support access denied", "Current verified scope or support staff permission is required.")
}
func missing() error {
	return api.NewProblem(404, "support_not_found", "Support record not found", "The record is unavailable in this scope.")
}
func bounded(s string, max int) bool {
	return strings.TrimSpace(s) != "" && len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func oneOf(v string, values ...string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}
func (s *Service) transact(ctx context.Context, a Actor, org string, staff, write bool, fn func(*session) (any, error)) (any, error) {
	if s == nil || s.Pool == nil {
		return nil, api.NewProblem(503, "support_unavailable", "Support unavailable", "Support storage is not configured.")
	}
	ctx, cancel := context.WithTimeout(ctx, api.SupportDispatchTimeout)
	defer cancel()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("support begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	x := &session{s: s, ctx: ctx, tx: tx, q: q.New(), actor: a, now: time.Now().UTC().Truncate(time.Microsecond)}
	if s.Now != nil {
		x.now = s.Now().UTC().Truncate(time.Microsecond)
	}
	// One bounded transaction lock serializes admission, routing and review application.
	// This intentionally favors correctness for a low-volume helpdesk over parallel writes.
	if write {
		if err = x.q.Lock(ctx, tx, "gregale-native-support"); err != nil {
			return nil, fmt.Errorf("support admission: %w", err)
		}
	}
	if err = x.authorize(org, staff); err != nil {
		return nil, err
	}
	result, err := fn(x)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("support commit: %w", err)
	}
	return result, nil
}
func (x *session) authorize(org string, staff bool) error {
	if !uid(x.actor.ID).Valid {
		return forbidden()
	}
	a, e := x.q.Account(x.ctx, x.tx, uid(x.actor.ID))
	if e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return forbidden()
		}
		return e
	}
	if a.Status != "active" || !a.EmailVerifiedAt.Valid {
		return forbidden()
	}
	if staff {
		if !x.actor.Session || !a.MfaEnrolledAt.Valid {
			return forbidden()
		}
		row, e := x.q.Staff(x.ctx, x.tx, a.ID)
		if e == nil {
			if row.Role == "revoked" {
				return forbidden()
			}
			x.role = row.Role
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if x.s.Operator != nil && x.s.Operator(x.actor.ID, a.Email) {
			x.role = "manager"
			return nil
		}
		return forbidden()
	}
	x.scope = Scope{Key: "account:" + x.actor.ID, Plan: a.Plan, AccountID: x.actor.ID}
	if org != "" {
		o, e := x.q.Org(x.ctx, x.tx, q.OrgParams{Slug: org, AccountID: a.ID})
		if e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return forbidden()
			}
			return e
		}
		x.scope = Scope{Key: "org:" + id(o.ID), Plan: o.Plan, OrgID: id(o.ID)}
	}
	if x.actor.KeyOrgID != "" {
		if x.scope.OrgID != "" {
			if x.actor.KeyOrgID != x.scope.OrgID {
				return forbidden()
			}
		} else {
			// Personal keys are bound to the deterministic personal organization.
			if _, e := x.q.PersonalOrg(x.ctx, x.tx, q.PersonalOrgParams{ID: uid(x.actor.KeyOrgID), PersonalOwnerAccountID: a.ID}); e != nil {
				return forbidden()
			}
		}
	}
	return nil
}
func (x *session) ticket(ticketID string) (q.SupportTicket, ticketState, error) {
	var st ticketState
	if !uid(ticketID).Valid {
		return q.SupportTicket{}, st, missing()
	}
	t, e := x.q.TicketLock(x.ctx, x.tx, uid(ticketID))
	if errors.Is(e, pgx.ErrNoRows) {
		return t, st, missing()
	}
	if e != nil {
		return t, st, e
	}
	if x.role == "" && t.ScopeKey != x.scope.Key {
		return t, st, missing()
	}
	if e = json.Unmarshal(t.State, &st); e != nil {
		return t, st, fmt.Errorf("support state: %w", e)
	}
	return t, st, nil
}
func (x *session) manager() error {
	if x.role != "manager" {
		return forbidden()
	}
	return nil
}
func (x *session) receipt(key, op, resource string, input any) (any, error) {
	if !uid(key).Valid {
		return nil, invalid("Idempotency-Key must be a UUID")
	}
	r, e := x.q.Receipt(x.ctx, x.tx, uid(key))
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	h := sha256.Sum256(raw(input))
	if id(r.ActorID) != x.actor.ID || r.Operation != op || r.ResourceID != uid(resource) || !bytes.Equal(r.RequestHash, h[:]) {
		return nil, conflict("idempotency key was used for a different request")
	}
	var v any
	e = json.Unmarshal(r.Result, &v)
	return v, e
}
func (x *session) remember(key, op, resource, ticket string, input, result any) error {
	h := sha256.Sum256(raw(input))
	return x.q.ReceiptSave(x.ctx, x.tx, q.ReceiptSaveParams{ID: uid(key), ActorID: uid(x.actor.ID), TicketID: uid(ticket), Operation: op, ResourceID: uid(resource), RequestHash: h[:], Result: raw(result), CreatedAt: ts(x.now)})
}
func (x *session) event(ticket, kind string, data any) error {
	event := newID()
	if err := x.q.EventInsert(x.ctx, x.tx, q.EventInsertParams{ID: uid(event), TicketID: uid(ticket), ActorID: uid(x.actor.ID), Kind: kind, Data: raw(data), CreatedAt: ts(x.now)}); err != nil {
		return err
	}
	if x.s.Webhook != nil && oneOf(kind, "created", "reply", "resolved", "reopened", "engineering_published") {
		return x.q.OutboxQueue(x.ctx, x.tx, q.OutboxQueueParams{ID: uid(event), TicketID: uid(ticket), AvailableAt: ts(x.now)})
	}
	return nil
}
func (x *session) save(t q.SupportTicket, st ticketState) error {
	st.Episode.observe(x.now)
	var resolved pgtype.Timestamptz
	if st.Episode.ResolvedAt != nil {
		resolved = ts(*st.Episode.ResolvedAt)
	}
	if err := x.q.TicketSave(x.ctx, x.tx, q.TicketSaveParams{ID: t.ID, Status: t.Status, Priority: t.Priority, TeamID: t.TeamID, AssigneeID: t.AssigneeID, State: raw(st), UpdatedAt: ts(x.now), ResolvedAt: resolved}); err != nil {
		return err
	}
	return x.saveEpisode(id(t.ID), st.Episode)
}
func (x *session) saveEpisode(ticket string, e Episode) error {
	return x.q.EpisodeSave(x.ctx, x.tx, q.EpisodeSaveParams{ID: uid(e.ID), TicketID: uid(ticket), StartedAt: ts(e.StartedAt), Data: raw(e)})
}
func (x *session) message(t q.SupportTicket, author, body string) error {
	if !bounded(body, api.SupportBodyMaxBytes) {
		return invalid("body must contain 1–10000 UTF-8 bytes")
	}
	n, e := x.q.MessageCount(x.ctx, x.tx, t.ID)
	if e != nil {
		return e
	}
	if n >= api.SupportMessagesPerTicket {
		return conflict("ticket message limit reached")
	}
	return x.q.MessageInsert(x.ctx, x.tx, q.MessageInsertParams{ID: uid(newID()), TicketID: t.ID, ActorID: uid(x.actor.ID), Author: author, Body: body, CreatedAt: ts(x.now)})
}
func (x *session) submission(key string, t q.SupportTicket, op string, input any) (any, error) {
	result := api.SupportSubmission{TicketID: id(t.ID), SubmissionID: key, Revision: t.Revision + 1}
	if err := x.remember(key, op, id(t.ID), id(t.ID), input, result); err != nil {
		return nil, err
	}
	return result, nil
}
func publicTicket(t q.SupportTicket, st ticketState, now time.Time) api.SupportTicket {
	v := api.SupportTicket{ID: id(t.ID), Subject: t.Subject, Category: t.Category, Status: t.Status, Revision: t.Revision, Context: st.Context, CreatedAt: t.CreatedAt.Time, UpdatedAt: t.UpdatedAt.Time}
	if t.ResolvedAt.Valid {
		r := t.ResolvedAt.Time
		v.ResolvedAt = &r
		until := r.Add(time.Duration(st.Episode.Policy.ReopenWindowSeconds) * time.Second)
		v.ReopenUntil = &until
		v.CanReopen = st.Episode.Policy.ReopenWindowSeconds > 0 && t.Status == "resolved" && !now.After(until) && st.CustomerReopens < st.Episode.Policy.CustomerReopensMax
	}
	return v
}
func staffTicket(t q.SupportTicket, st ticketState, now time.Time) StaffTicket {
	return StaffTicket{Ticket: publicTicket(t, st, now), Priority: t.Priority, TeamID: id(t.TeamID), AssigneeID: id(t.AssigneeID), Episode: st.Episode, RequiredSkills: st.RequiredSkills, CustomerReopens: st.CustomerReopens}
}
