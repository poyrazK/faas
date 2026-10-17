package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func issueTrackerLinkRow(r sqlc.IssueTrackerLink) api.IssueTrackerLink {
	return api.IssueTrackerLink{ID: issueID(r.ID), OriginalIssueID: issueID(r.IssueID), Provider: r.Provider, ExternalKey: r.ExternalKey, URL: r.Url, Title: r.Title, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time, CreatedByAccountID: issueID(r.CreatedByAccountID), UpdatedByAccountID: issueID(r.UpdatedByAccountID)}
}
func issueTrackerCursorHash(app, id string) string {
	hash := sha256.Sum256([]byte("issue_tracker_links/" + app + "/" + id))
	return hex.EncodeToString(hash[:])
}
func (s *PgStore) ListIssueTrackerLinks(ctx context.Context, app, account, id string, cur IssueCursor) (api.ListIssueTrackerLinksResponse, error) {
	out := api.ListIssueTrackerLinksResponse{Items: []api.IssueTrackerLink{}}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := issueCanonicalRow(ctx, q, tx, app, id)
	if err != nil {
		return out, err
	}
	if issueID(row.AccountID) != account {
		return out, ErrNotFound
	}
	hash := issueTrackerCursorHash(app, issueID(row.ID))
	if !cur.Time.IsZero() && cur.FilterHash != hash || cur.Sort != "" || cur.Tag != "" || cur.ImpactWindowEnd != nil || cur.MinCustomers != 0 {
		return out, ErrInvalidArgument
	}
	rows, err := q.IssueListTrackerLinks(ctx, tx, sqlc.IssueListTrackerLinksParams{IssueID: row.ID, CursorTime: issueTime(cur.Time), CursorID: issueUUID(cur.ID), PageLimit: api.IssuePageSize + 1})
	if err != nil {
		return out, err
	}
	for i, r := range rows {
		if i == api.IssuePageSize {
			last := out.Items[len(out.Items)-1]
			out.NextCursor = EncodeIssueCursor(IssueCursor{Time: last.CreatedAt, ID: last.ID, FilterHash: hash})
			break
		}
		out.Items = append(out.Items, issueTrackerLinkRow(r))
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) PutIssueTrackerLink(ctx context.Context, app, account, id, actor string, in api.PutIssueTrackerLinkRequest, now time.Time) (api.IssueTrackerLink, error) {
	normalized, err := api.NormalizeIssueTrackerLink(in)
	if err != nil {
		return api.IssueTrackerLink{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.IssueTrackerLink{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return api.IssueTrackerLink{}, mapErr(err)
	}
	if issueID(owner.AccountID) != account {
		return api.IssueTrackerLink{}, ErrNotFound
	}
	root, err := issueCanonicalRow(ctx, q, tx, app, id)
	if err != nil {
		return api.IssueTrackerLink{}, err
	}
	existing, err := q.IssueFindTrackerLink(ctx, tx, sqlc.IssueFindTrackerLinkParams{IssueID: root.ID, Provider: normalized.Provider, ExternalKey: normalized.ExternalKey})
	var row sqlc.IssueTrackerLink
	action := "tracker_linked"
	if errors.Is(err, pgx.ErrNoRows) {
		n, e := q.IssueCountTrackerLinks(ctx, tx, root.ID)
		if e != nil {
			return api.IssueTrackerLink{}, e
		}
		if n >= api.IssueTrackerLinksMax {
			return api.IssueTrackerLink{}, &IssueLimitError{Resource: "tracker_links", Limit: api.IssueTrackerLinksMax, Observed: n + 1}
		}
		row, err = q.IssueCreateTrackerLink(ctx, tx, sqlc.IssueCreateTrackerLinkParams{IssueID: root.ID, Provider: normalized.Provider, ExternalKey: normalized.ExternalKey, Url: normalized.URL, Title: normalized.Title, Now: issueTime(now), Actor: issueUUID(actor)})
	} else if err != nil {
		return api.IssueTrackerLink{}, err
	} else {
		if existing.Title == normalized.Title {
			return issueTrackerLinkRow(existing), tx.Commit(ctx)
		}
		action = "tracker_link_updated"
		row, err = q.IssueUpdateTrackerLink(ctx, tx, sqlc.IssueUpdateTrackerLinkParams{ID: existing.ID, Title: normalized.Title, Now: issueTime(now), Actor: issueUUID(actor)})
	}
	if err != nil {
		return api.IssueTrackerLink{}, mapErr(err)
	}
	if err = issueRegroupAudit(ctx, q, tx, root, action, actor, map[string]string{"link_id": issueID(row.ID), "provider": row.Provider, "external_key": row.ExternalKey, "url": row.Url, "original_issue_id": issueID(row.IssueID)}, now); err != nil {
		return api.IssueTrackerLink{}, err
	}
	return issueTrackerLinkRow(row), tx.Commit(ctx)
}
func (s *PgStore) DeleteIssueTrackerLink(ctx context.Context, app, account, id, link, actor string, now time.Time) error {
	parsed, err := uuid.Parse(link)
	if err != nil || parsed == uuid.Nil {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return mapErr(err)
	}
	if issueID(owner.AccountID) != account {
		return ErrNotFound
	}
	root, err := issueCanonicalRow(ctx, q, tx, app, id)
	if err != nil {
		return err
	}
	row, err := q.IssueGetTrackerLink(ctx, tx, sqlc.IssueGetTrackerLinkParams{IssueID: root.ID, ID: issueUUID(parsed.String())})
	if err != nil {
		return mapErr(err)
	}
	if err = q.IssueDeleteTrackerLink(ctx, tx, row.ID); err != nil {
		return err
	}
	if err = issueRegroupAudit(ctx, q, tx, root, "tracker_unlinked", actor, map[string]string{"link_id": issueID(row.ID), "provider": row.Provider, "external_key": row.ExternalKey, "url": row.Url, "original_issue_id": issueID(row.IssueID)}, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
