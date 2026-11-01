package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/labels"
	rlsql "github.com/onebox-faas/faas/pkg/state/resourcelabels/sqlc"
)

// LabelKind names a resource other than an app that carries labels
// (ADR-20261031100025 step 5). Apps keep AppLabelStore.
type LabelKind string

const (
	LabelKindDatabase LabelKind = "database"
	LabelKindCron     LabelKind = "cron"
	LabelKindJob      LabelKind = "job"
)

// ResourceLabelStore reads and writes the labels of managed databases, crons
// and jobs, as AppLabelStore does for apps. Only apid uses it, after it has
// checked that the caller owns the resource.
type ResourceLabelStore interface {
	// ResourceLabels returns a resource's labels, empty when it has none.
	ResourceLabels(ctx context.Context, kind LabelKind, id string) (map[string]string, error)
	// ResourceLabelsByID returns the labels of the given resources that carry
	// any, keyed by ID.
	ResourceLabelsByID(ctx context.Context, kind LabelKind, ids []string) (map[string]map[string]string, error)
	// UpdateResourceLabels applies a customer's change atomically and returns
	// the labels the resource carries after it. A result over
	// api.LabelsPerResourceMax is refused with the *labels.Error that names
	// the limit.
	UpdateResourceLabels(ctx context.Context, kind LabelKind, id string, change labels.Change) (map[string]string, error)
}

// labelQueries is one table's three statements; each kind has its own sqlc
// queries, so no statement is built from the kind.
type labelQueries struct {
	get    func(context.Context, rlsql.DBTX, pgtype.UUID) ([]byte, error)
	byID   func(context.Context, rlsql.DBTX, []pgtype.UUID) (map[string][]byte, error)
	update func(context.Context, rlsql.DBTX, rlsql.UpdateDatabaseLabelsParams) ([]byte, error)
}

func resourceLabelQueries(kind LabelKind) (labelQueries, error) {
	q := rlsql.New()
	switch kind {
	case LabelKindDatabase:
		return labelQueries{
			get: q.DatabaseLabels,
			byID: func(ctx context.Context, db rlsql.DBTX, ids []pgtype.UUID) (map[string][]byte, error) {
				rows, err := q.DatabaseLabelsByID(ctx, db, ids)
				out := make(map[string][]byte, len(rows))
				for _, r := range rows {
					out[pgUUIDString(r.ID)] = r.Labels
				}
				return out, err
			},
			update: q.UpdateDatabaseLabels,
		}, nil
	case LabelKindCron:
		return labelQueries{
			get: q.CronLabels,
			byID: func(ctx context.Context, db rlsql.DBTX, ids []pgtype.UUID) (map[string][]byte, error) {
				rows, err := q.CronLabelsByID(ctx, db, ids)
				out := make(map[string][]byte, len(rows))
				for _, r := range rows {
					out[pgUUIDString(r.ID)] = r.Labels
				}
				return out, err
			},
			update: func(ctx context.Context, db rlsql.DBTX, p rlsql.UpdateDatabaseLabelsParams) ([]byte, error) {
				return q.UpdateCronLabels(ctx, db, rlsql.UpdateCronLabelsParams(p))
			},
		}, nil
	case LabelKindJob:
		return labelQueries{
			get: q.JobLabels,
			byID: func(ctx context.Context, db rlsql.DBTX, ids []pgtype.UUID) (map[string][]byte, error) {
				rows, err := q.JobLabelsByID(ctx, db, ids)
				out := make(map[string][]byte, len(rows))
				for _, r := range rows {
					out[pgUUIDString(r.ID)] = r.Labels
				}
				return out, err
			},
			update: func(ctx context.Context, db rlsql.DBTX, p rlsql.UpdateDatabaseLabelsParams) ([]byte, error) {
				return q.UpdateJobLabels(ctx, db, rlsql.UpdateJobLabelsParams(p))
			},
		}, nil
	}
	return labelQueries{}, fmt.Errorf("state: no labels on %q", kind)
}

func (s *PgStore) ResourceLabels(ctx context.Context, kind LabelKind, id string) (map[string]string, error) {
	q, err := resourceLabelQueries(kind)
	if err != nil {
		return nil, err
	}
	raw, err := q.get(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return nil, mapErr(err)
	}
	return decodeLabels(raw)
}

func (s *PgStore) ResourceLabelsByID(ctx context.Context, kind LabelKind, ids []string) (map[string]map[string]string, error) {
	q, err := resourceLabelQueries(kind)
	if err != nil {
		return nil, err
	}
	uuids := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		if u := mustPgUUID(id); u.Valid {
			uuids = append(uuids, u)
		}
	}
	out := make(map[string]map[string]string)
	if len(uuids) == 0 {
		return out, nil
	}
	rows, err := q.byID(ctx, s.pool, uuids)
	if err != nil {
		return nil, fmt.Errorf("state: %s labels: %w", kind, err)
	}
	for id, raw := range rows {
		decoded, err := decodeLabels(raw)
		if err != nil {
			return nil, err
		}
		out[id] = decoded
	}
	return out, nil
}

func (s *PgStore) UpdateResourceLabels(ctx context.Context, kind LabelKind, id string, change labels.Change) (map[string]string, error) {
	q, err := resourceLabelQueries(kind)
	if err != nil {
		return nil, err
	}
	if err := change.Validate(); err != nil {
		return nil, err
	}
	set, err := json.Marshal(nonNilLabels(change.Set))
	if err != nil {
		return nil, fmt.Errorf("state: encode labels: %w", err)
	}
	raw, err := q.update(ctx, s.pool, rlsql.UpdateDatabaseLabelsParams{
		ID:        mustPgUUID(id),
		Remove:    nonNilStrings(change.Remove),
		Set:       set,
		MaxLabels: api.LabelsPerResourceMax,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The row is gone, or the result passed the limit: read the labels
		// again to tell which, as UpdateAppLabels does.
		current, readErr := s.ResourceLabels(ctx, kind, id)
		if readErr != nil {
			return nil, readErr
		}
		if _, applyErr := labels.Apply(current, change); applyErr != nil {
			return nil, applyErr
		}
		return nil, fmt.Errorf("state: update %s labels: %w", kind, ErrConflict)
	}
	if err != nil {
		return nil, fmt.Errorf("state: update %s labels: %w", kind, err)
	}
	return decodeLabels(raw)
}

// MemStore keeps labels apart from the resources and does not check that
// one exists: apid resolves the resource before it reads or writes labels.

func (m *MemStore) ResourceLabels(_ context.Context, kind LabelKind, id string) (map[string]string, error) {
	if _, err := resourceLabelQueries(kind); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return nonNilLabels(maps.Clone(m.resourceLabels[kind][id])), nil
}

func (m *MemStore) ResourceLabelsByID(_ context.Context, kind LabelKind, ids []string) (map[string]map[string]string, error) {
	if _, err := resourceLabelQueries(kind); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]map[string]string)
	for _, id := range ids {
		if l := m.resourceLabels[kind][id]; len(l) > 0 {
			out[id] = maps.Clone(l)
		}
	}
	return out, nil
}

func (m *MemStore) UpdateResourceLabels(_ context.Context, kind LabelKind, id string, change labels.Change) (map[string]string, error) {
	if _, err := resourceLabelQueries(kind); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next, err := labels.Apply(m.resourceLabels[kind][id], change)
	if err != nil {
		return nil, err
	}
	if m.resourceLabels == nil {
		m.resourceLabels = make(map[LabelKind]map[string]map[string]string)
	}
	if m.resourceLabels[kind] == nil {
		m.resourceLabels[kind] = make(map[string]map[string]string)
	}
	m.resourceLabels[kind][id] = next
	return maps.Clone(next), nil
}
