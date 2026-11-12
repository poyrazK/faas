package support

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/support/sqlc"
)

func digest(v any) string { h := sha256.Sum256(raw(v)); return hex.EncodeToString(h[:]) }
func (x *session) engineering(ticket string) (Engineering, error) {
	row, e := x.q.Engineering(x.ctx, x.tx, uid(ticket))
	if errors.Is(e, pgx.ErrNoRows) {
		return Engineering{}, nil
	}
	if e != nil {
		return Engineering{}, e
	}
	var v Engineering
	e = json.Unmarshal(row.Data, &v)
	v.Revision = row.Revision
	return v, e
}
func (x *session) getEngineering(ticket string) (any, error) {
	if _, _, e := x.ticket(ticket); e != nil {
		return nil, e
	}
	return x.engineering(ticket)
}
func (x *session) saveEngineering(key, ticket string, in Engineering) (any, error) {
	t, st, e := x.ticket(ticket)
	if e != nil {
		return nil, e
	}
	if v, e := x.receipt(key, "engineering", ticket, in); v != nil || e != nil {
		return v, e
	}
	if x.role != "manager" && id(t.AssigneeID) != x.actor.ID {
		return nil, forbidden()
	}
	if t.Revision != in.ExpectedRevision {
		return nil, conflict("ticket revision changed")
	}
	old, e := x.engineering(ticket)
	if e != nil {
		return nil, e
	}
	if old.Revision != in.Revision {
		return nil, conflict("engineering revision changed")
	}
	if !oneOf(in.Severity, "low", "normal", "high", "critical") || !oneOf(in.Status, "investigating", "accepted", "in_progress", "fix_ready", "resolved", "cancelled") || !bounded(in.Reproduction, api.SupportBodyMaxBytes) {
		return nil, invalid("invalid engineering severity, status or reproduction steps")
	}
	if _, e = x.eligible(in.OwnerID, nil, false); e != nil {
		return nil, e
	}
	if in.IssueURL != "" {
		u, e := url.Parse(in.IssueURL)
		if e != nil || len(in.IssueURL) > api.SupportSubjectMaxBytes || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, invalid("issue_url must be a plain HTTPS link without credentials, query or fragment")
		}
	}
	value := in
	value.Revision++
	value.ExpectedRevision = 0
	value.Redacted = false
	if e = x.q.EngineeringSave(x.ctx, x.tx, q.EngineeringSaveParams{TicketID: t.ID, Revision: value.Revision, Data: raw(value)}); e != nil {
		return nil, e
	}
	if e = x.save(t, st); e != nil {
		return nil, e
	}
	if e = x.event(ticket, "engineering_saved", map[string]any{"engineering_revision": value.Revision, "owner_id": value.OwnerID, "status": value.Status, "severity": value.Severity}); e != nil {
		return nil, e
	}
	return x.submission(key, t, "engineering", in)
}
func (x *session) preview(ticket, kind string, in PreviewRequest) (any, error) {
	if e := x.manager(); e != nil {
		return nil, e
	}
	t, _, e := x.ticket(ticket)
	if e != nil {
		return nil, e
	}
	if t.Revision != in.ExpectedRevision {
		return nil, conflict("ticket revision changed")
	}
	if e = x.q.ExpirePreviews(x.ctx, x.tx, q.ExpirePreviewsParams{ExpiresAt: ts(x.now), Limit: api.SupportWorkerBatch}); e != nil {
		return nil, e
	}
	n, e := x.q.PreviewCount(x.ctx, x.tx, q.PreviewCountParams{ActorID: uid(x.actor.ID), ExpiresAt: ts(x.now)})
	if e != nil {
		return nil, e
	}
	if n >= api.SupportPreviewsPerActor {
		return nil, conflict("too many active review previews")
	}
	p := Preview{ID: newID(), Revision: t.Revision, ExpiresAt: x.now.Add(api.SupportPreviewTTL)}
	switch kind {
	case "redaction":
		if !oneOf(in.Reason, "secret", "personal_data", "other") {
			return nil, invalid("redaction reason must be secret, personal_data or other")
		}
		p.Reason = in.Reason
		if in.Engineering && in.MessageID != "" {
			return nil, invalid("select exactly one redaction artifact")
		}
		if in.Engineering {
			v, e := x.engineering(ticket)
			if e != nil {
				return nil, e
			}
			if v.Revision == 0 || v.Redacted {
				return nil, conflict("engineering reproduction is unavailable")
			}
			p.Engineering = true
			p.EngineeringRevision = v.Revision
			p.Hash = digest(v.Reproduction)
		} else {
			m, e := x.q.Message(x.ctx, x.tx, q.MessageParams{ID: uid(in.MessageID), TicketID: t.ID})
			if errors.Is(e, pgx.ErrNoRows) {
				return nil, missing()
			}
			if e != nil {
				return nil, e
			}
			if m.Redacted {
				return nil, conflict("message is already redacted")
			}
			copies, e := x.q.RedactionCopies(x.ctx, x.tx, q.RedactionCopiesParams{TicketID: t.ID, Body: m.Body})
			if e != nil {
				return nil, e
			}
			for _, c := range copies {
				p.MessageIDs = append(p.MessageIDs, id(c.ID))
			}
			p.Hash = digest(m.Body)
		}
	case "engineering":
		v, e := x.engineering(ticket)
		if e != nil {
			return nil, e
		}
		if v.Revision == 0 || v.Revision != in.EngineeringRevision {
			return nil, conflict("engineering revision changed")
		}
		if t.Status == "resolved" {
			return nil, conflict("reopen the customer case before publishing an update")
		}
		if !bounded(in.Body, api.SupportBodyMaxBytes) {
			return nil, invalid("public update requires a body")
		}
		p.Body = in.Body
		p.Hash = digest(in.Body)
		p.EngineeringRevision = v.Revision
	default:
		return nil, invalid("unknown preview kind")
	}
	if e = x.q.PreviewInsert(x.ctx, x.tx, q.PreviewInsertParams{ID: uid(p.ID), TicketID: t.ID, ActorID: uid(x.actor.ID), Kind: kind, Snapshot: raw(p), ExpiresAt: ts(p.ExpiresAt)}); e != nil {
		return nil, e
	}
	return p, nil
}
func (x *session) approve(key, ticket, kind string, in Approval) (any, error) {
	if e := x.manager(); e != nil {
		return nil, e
	}
	t, st, e := x.ticket(ticket)
	if e != nil {
		return nil, e
	}
	op := kind + "_apply"
	if v, e := x.receipt(key, op, ticket, in); v != nil || e != nil {
		return v, e
	}
	row, e := x.q.Preview(x.ctx, x.tx, q.PreviewParams{ID: uid(in.PreviewID), TicketID: t.ID, ActorID: uid(x.actor.ID), Kind: kind})
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, conflict("review preview is unavailable")
	}
	if e != nil {
		return nil, e
	}
	var p Preview
	if e = json.Unmarshal(row.Snapshot, &p); e != nil {
		return nil, e
	}
	if !x.now.Before(row.ExpiresAt.Time) || t.Revision != p.Revision || t.Revision != in.ExpectedRevision {
		return nil, conflict("review preview expired or case changed; create a fresh preview")
	}
	if kind == "redaction" {
		e = x.applyRedaction(t, p)
	} else {
		e = x.publishEngineering(t, &st, p)
	}
	if e != nil {
		return nil, e
	}
	if e = x.q.PreviewsDelete(x.ctx, x.tx, t.ID); e != nil {
		return nil, e
	}
	if e = x.save(t, st); e != nil {
		return nil, e
	}
	audit := map[string]any{"preview_id": p.ID, "message_ids": p.MessageIDs, "engineering": p.Engineering, "reason": p.Reason, "engineering_revision": p.EngineeringRevision}
	event := "redacted"
	if kind == "engineering" {
		event = "engineering_published"
	}
	if e = x.event(ticket, event, audit); e != nil {
		return nil, e
	}
	return x.submission(key, t, op, in)
}
func (x *session) applyRedaction(t q.SupportTicket, p Preview) error {
	if p.Engineering {
		v, e := x.engineering(id(t.ID))
		if e != nil {
			return e
		}
		if v.Revision != p.EngineeringRevision || digest(v.Reproduction) != p.Hash {
			return conflict("engineering content changed")
		}
		v.Reproduction = "[redacted]"
		v.Redacted = true
		v.Revision++
		return x.q.EngineeringSave(x.ctx, x.tx, q.EngineeringSaveParams{TicketID: t.ID, Revision: v.Revision, Data: raw(v)})
	}
	if len(p.MessageIDs) == 0 {
		return conflict("redaction has no artifacts")
	}
	m, e := x.q.Message(x.ctx, x.tx, q.MessageParams{ID: uid(p.MessageIDs[0]), TicketID: t.ID})
	if e != nil {
		return e
	}
	if digest(m.Body) != p.Hash || m.Redacted {
		return conflict("message content changed")
	}
	copies, e := x.q.RedactionCopies(x.ctx, x.tx, q.RedactionCopiesParams{TicketID: t.ID, Body: m.Body})
	if e != nil {
		return e
	}
	ids := make([]pgtype.UUID, 0, len(copies))
	strings := []string{}
	for _, c := range copies {
		ids = append(ids, c.ID)
		strings = append(strings, id(c.ID))
	}
	if !slices.Equal(strings, p.MessageIDs) {
		return conflict("redaction artifacts changed")
	}
	return x.q.RedactMessages(x.ctx, x.tx, q.RedactMessagesParams{TicketID: t.ID, Ids: ids})
}
func (x *session) publishEngineering(t q.SupportTicket, st *ticketState, p Preview) error {
	v, e := x.engineering(id(t.ID))
	if e != nil {
		return e
	}
	if v.Revision != p.EngineeringRevision || digest(p.Body) != p.Hash || t.Status == "resolved" {
		return conflict("engineering review changed")
	}
	if e = x.message(t, "staff", p.Body); e != nil {
		return e
	}
	st.Episode.reply(x.now)
	return nil
}
func (x *session) configEvent(kind, resource string, value any) error {
	return x.q.ConfigEvent(x.ctx, x.tx, q.ConfigEventParams{ID: uid(newID()), ActorID: uid(x.actor.ID), Kind: kind, ResourceID: uid(resource), Data: raw(value), CreatedAt: ts(x.now)})
}
func (x *session) configEvents(after int64) (any, error) {
	if e := x.manager(); e != nil {
		return nil, e
	}
	rows, e := x.q.ConfigEvents(x.ctx, x.tx, q.ConfigEventsParams{Sequence: pgtype.Int8{Int64: after, Valid: true}, Limit: api.SupportPageSize})
	if e != nil {
		return nil, e
	}
	out := []Event{}
	for _, r := range rows {
		out = append(out, Event{ID: id(r.ID), Sequence: r.Sequence.Int64, ActorID: id(r.ActorID), Kind: r.Kind, Data: json.RawMessage(r.Data), CreatedAt: r.CreatedAt.Time})
	}
	return map[string]any{"items": out}, nil
}

// Cleanup is bounded and participates in the same lock as preview creation and application.
func (s *Service) Cleanup(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, api.SupportDispatchTimeout)
	defer cancel()
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := q.New()
	if e = queries.Lock(ctx, tx, "gregale-native-support"); e != nil {
		return e
	}
	if e = queries.ExpirePreviews(ctx, tx, q.ExpirePreviewsParams{ExpiresAt: ts(time.Now().UTC()), Limit: api.SupportWorkerBatch}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
