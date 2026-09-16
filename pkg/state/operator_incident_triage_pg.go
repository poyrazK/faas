package state

import (
	"context"
)

// ListOperatorIncidentTriage reads only the requested inbox keys. The
// caller's incident projection supplies the bounded key set, so this remains
// a single query even for a full 200-row page.
func (s *PgStore) ListOperatorIncidentTriage(ctx context.Context, dedupeKeys []string) (map[string]OperatorIncidentTriage, error) {
	out := make(map[string]OperatorIncidentTriage, len(dedupeKeys))
	if len(dedupeKeys) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		select dedupe_key, status, owner, note, updated_at, updated_by
		  from operator_incident_triage
		 where dedupe_key = any($1::text[])`, dedupeKeys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row OperatorIncidentTriage
		if err := rows.Scan(&row.DedupeKey, &row.Status, &row.Owner, &row.Note, &row.UpdatedAt, &row.UpdatedBy); err != nil {
			return nil, err
		}
		out[row.DedupeKey] = row
	}
	return out, rows.Err()
}

// UpsertOperatorIncidentTriage records the latest operator workflow state for
// a stable inbox dedupe key. It is idempotent for retries with the same body;
// updated_at is intentionally refreshed so the audit trail reflects the last
// operator action.
func (s *PgStore) UpsertOperatorIncidentTriage(ctx context.Context, dedupeKey, status, owner, note, updatedBy string) (OperatorIncidentTriage, error) {
	if err := validateOperatorIncidentTriage(dedupeKey, status, owner, note, updatedBy); err != nil {
		return OperatorIncidentTriage{}, err
	}
	var row OperatorIncidentTriage
	err := s.pool.QueryRow(ctx, `
		insert into operator_incident_triage (dedupe_key, status, owner, note, updated_at, updated_by)
		values ($1, $2, $3, $4, now(), $5)
		on conflict (dedupe_key) do update set
			status = excluded.status,
			owner = excluded.owner,
			note = excluded.note,
			updated_at = now(),
			updated_by = excluded.updated_by
		returning dedupe_key, status, owner, note, updated_at, updated_by`,
		dedupeKey, status, owner, note, updatedBy,
	).Scan(&row.DedupeKey, &row.Status, &row.Owner, &row.Note, &row.UpdatedAt, &row.UpdatedBy)
	if err != nil {
		return OperatorIncidentTriage{}, err
	}
	return row, nil
}
