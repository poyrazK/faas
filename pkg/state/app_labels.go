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
	labelsql "github.com/onebox-faas/faas/pkg/state/applabels/sqlc"
)

// AppLabelStore reads and writes app labels (ADR-20261031100025). Only apid
// uses it; the column stays off every App read, so nothing on the request
// path loads labels.
type AppLabelStore interface {
	// AppLabels returns an app's labels, empty when it has none.
	AppLabels(ctx context.Context, appID string) (map[string]string, error)
	// AppLabelsByID returns the labels of the given apps that carry any,
	// keyed by app ID.
	AppLabelsByID(ctx context.Context, appIDs []string) (map[string]map[string]string, error)
	// UpdateAppLabels applies a customer's change atomically and returns the
	// labels the app carries after it. A result over api.LabelsPerResourceMax
	// is refused with the *labels.Error that names the limit.
	UpdateAppLabels(ctx context.Context, appID string, change labels.Change) (map[string]string, error)
}

func (s *PgStore) AppLabels(ctx context.Context, appID string) (map[string]string, error) {
	raw, err := labelsql.New().AppLabels(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return nil, mapErr(err)
	}
	return decodeLabels(raw)
}

func (s *PgStore) AppLabelsByID(ctx context.Context, appIDs []string) (map[string]map[string]string, error) {
	ids := make([]pgtype.UUID, 0, len(appIDs))
	for _, id := range appIDs {
		if u := mustPgUUID(id); u.Valid {
			ids = append(ids, u)
		}
	}
	out := make(map[string]map[string]string)
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := labelsql.New().AppLabelsByID(ctx, s.pool, ids)
	if err != nil {
		return nil, fmt.Errorf("state: app labels: %w", err)
	}
	for _, row := range rows {
		decoded, err := decodeLabels(row.Labels)
		if err != nil {
			return nil, err
		}
		out[pgUUIDString(row.ID)] = decoded
	}
	return out, nil
}

func (s *PgStore) UpdateAppLabels(ctx context.Context, appID string, change labels.Change) (map[string]string, error) {
	if err := change.Validate(); err != nil {
		return nil, err
	}
	set, err := json.Marshal(nonNilLabels(change.Set))
	if err != nil {
		return nil, fmt.Errorf("state: encode labels: %w", err)
	}
	raw, err := labelsql.New().UpdateAppLabels(ctx, s.pool, labelsql.UpdateAppLabelsParams{
		ID:        mustPgUUID(appID),
		Remove:    nonNilStrings(change.Remove),
		Set:       set,
		MaxLabels: api.LabelsPerResourceMax,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// The app is gone, or the result passed the limit: read the labels
		// again to tell which, and to name the count the change would reach.
		current, readErr := s.AppLabels(ctx, appID)
		if readErr != nil {
			return nil, readErr
		}
		if _, applyErr := labels.Apply(current, change); applyErr != nil {
			return nil, applyErr
		}
		return nil, fmt.Errorf("state: update app labels: %w", ErrConflict)
	}
	if err != nil {
		return nil, fmt.Errorf("state: update app labels: %w", err)
	}
	return decodeLabels(raw)
}

func (m *MemStore) AppLabels(_ context.Context, appID string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[appID]; !ok {
		return nil, ErrNotFound
	}
	return nonNilLabels(maps.Clone(m.appLabels[appID])), nil
}

func (m *MemStore) AppLabelsByID(_ context.Context, appIDs []string) (map[string]map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]map[string]string)
	for _, id := range appIDs {
		if l := m.appLabels[id]; len(l) > 0 {
			out[id] = maps.Clone(l)
		}
	}
	return out, nil
}

func (m *MemStore) UpdateAppLabels(_ context.Context, appID string, change labels.Change) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[appID]; !ok {
		return nil, ErrNotFound
	}
	next, err := labels.Apply(m.appLabels[appID], change)
	if err != nil {
		return nil, err
	}
	if m.appLabels == nil {
		m.appLabels = make(map[string]map[string]string)
	}
	m.appLabels[appID] = next
	return maps.Clone(next), nil
}

func decodeLabels(raw []byte) (map[string]string, error) {
	out := map[string]string{}
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("state: decode labels: %w", err)
	}
	return out, nil
}

func nonNilLabels(l map[string]string) map[string]string {
	if l == nil {
		return map[string]string{}
	}
	return l
}
