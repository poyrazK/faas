package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const incidentNoteColumns = `id::text, account_id::text, incident_id, kind, body, author, created_at`

// AddIncidentNote implements IncidentNoteStore. A per-incident advisory lock
// serialises the acknowledgement lookup, the count and the insert.
func (s *PgStore) AddIncidentNote(ctx context.Context, n IncidentNote, limit int) (IncidentNote, error) {
	n, prefix, key, err := prepareIncidentNote(n)
	if err != nil {
		return IncidentNote{}, err
	}
	var alertEpisode, routeIncident, statusIncident *string
	switch prefix {
	case "alert-":
		alertEpisode = &key
	case "route-":
		routeIncident = &key
	default:
		statusIncident = &key
	}
	var out IncidentNote
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('incident_notes:' || $1::text || ':' || $2::text, 0))`, n.AccountID, n.IncidentID); err != nil {
			return err
		}
		if n.Kind == IncidentNoteAcknowledged {
			err := scanIncidentNote(tx.QueryRow(ctx, `select `+incidentNoteColumns+` from incident_notes
				 where account_id = $1 and incident_id = $2 and kind = 'acknowledged'`, n.AccountID, n.IncidentID), &out)
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		var count int
		if err := tx.QueryRow(ctx, `select count(*) from incident_notes where account_id = $1 and incident_id = $2`, n.AccountID, n.IncidentID).Scan(&count); err != nil {
			return err
		}
		if count >= limit {
			return &IncidentNotesQuotaError{Limit: limit, Observed: count}
		}
		return scanIncidentNote(tx.QueryRow(ctx, `
			insert into incident_notes (id, account_id, incident_id, alert_episode_id, route_incident_id, status_incident_id, kind, body, author, created_at)
			values ($1, $2, $3, $4::uuid, $5::uuid, $6::uuid, $7, $8, $9, $10)
			returning `+incidentNoteColumns,
			n.ID, n.AccountID, n.IncidentID, alertEpisode, routeIncident, statusIncident, string(n.Kind), n.Body, n.Author, n.CreatedAt), &out)
	})
	var quota *IncidentNotesQuotaError
	var pgErr *pgconn.PgError
	switch {
	case err == nil:
		return out, nil
	case errors.As(err, &quota):
		return IncidentNote{}, quota
	case errors.As(err, &pgErr) && pgErr.Code == "23503":
		return IncidentNote{}, ErrNotFound
	}
	return IncidentNote{}, fmt.Errorf("state: add incident note: %w", err)
}

// ListIncidentNotes implements IncidentNoteStore.
func (s *PgStore) ListIncidentNotes(ctx context.Context, accountID, incidentID string, limit int) ([]IncidentNote, error) {
	rows, err := s.pool.Query(ctx, `select `+incidentNoteColumns+` from incident_notes
		 where account_id = $1 and incident_id = $2
		 order by created_at, id
		 limit $3`, accountID, incidentID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list incident notes: %w", err)
	}
	return collectIncidentNotes(rows)
}

// IncidentAcknowledgements implements IncidentNoteStore.
func (s *PgStore) IncidentAcknowledgements(ctx context.Context, accountID string, incidentIDs []string) (map[string]IncidentNote, error) {
	out := map[string]IncidentNote{}
	if len(incidentIDs) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `select `+incidentNoteColumns+` from incident_notes
		 where account_id = $1 and kind = 'acknowledged' and incident_id = any($2::text[])`, accountID, incidentIDs)
	if err != nil {
		return nil, fmt.Errorf("state: read incident acknowledgements: %w", err)
	}
	notes, err := collectIncidentNotes(rows)
	for _, n := range notes {
		out[n.IncidentID] = n
	}
	return out, err
}

func collectIncidentNotes(rows pgx.Rows) ([]IncidentNote, error) {
	defer rows.Close()
	out := []IncidentNote{}
	for rows.Next() {
		var n IncidentNote
		if err := scanIncidentNote(rows, &n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func scanIncidentNote(row pgx.Row, n *IncidentNote) error {
	var kind string
	if err := row.Scan(&n.ID, &n.AccountID, &n.IncidentID, &kind, &n.Body, &n.Author, &n.CreatedAt); err != nil {
		return err
	}
	n.Kind, n.CreatedAt = IncidentNoteKind(kind), n.CreatedAt.UTC()
	return nil
}
