package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	ErrAPISandboxScenarioLibraryFull = errors.New("state: API sandbox scenario library is full")
	ErrAPISandboxScenarioNameTaken   = errors.New("state: API sandbox scenario name already exists")
)

type APISandboxScenarioLibraryStore interface {
	ListAPISandboxLibraryScenarios(context.Context, string) ([]api.APISandboxLibraryScenarioSummary, error)
	GetAPISandboxLibraryScenario(context.Context, string, string) (api.APISandboxLibraryScenario, error)
	GetAPISandboxLibraryScenarioByRevision(context.Context, string, string) (api.APISandboxScenario, error)
	PutAPISandboxLibraryScenario(context.Context, string, string, api.APISandboxScenarioInput, APISandboxScenarioOrigin) (api.APISandboxLibraryScenario, error)
	DeleteAPISandboxLibraryScenario(context.Context, string, string, string) error
}

type sandboxScenarioLibraryEntry struct {
	savedAPISandboxScenario
	name string
}

func (entry sandboxScenarioLibraryEntry) response(id string) (api.APISandboxLibraryScenario, error) {
	scenario, err := entry.decode()
	return api.APISandboxLibraryScenario{ID: id, Scenario: scenario, SourceRequestID: entry.sourceRequestID}, err
}

// update returns the current entry for an identical retry. On an edit the
// revision changes while the private source request stays with the scenario.
func (entry sandboxScenarioLibraryEntry) update(input api.APISandboxScenarioInput, origin APISandboxScenarioOrigin) (sandboxScenarioLibraryEntry, error) {
	definition, err := json.Marshal(input)
	if err != nil {
		return sandboxScenarioLibraryEntry{}, err
	}
	if bytes.Equal(entry.definition, definition) && (origin.RequestID == "" || origin.RequestID == entry.sourceRequestID) {
		return entry, nil
	}
	if entry.revision != origin.ExpectedRevision || (entry.revision != "" && origin.RequestID != "") {
		return sandboxScenarioLibraryEntry{}, ErrConflict
	}
	if entry.revision == "" {
		entry.sourceRequestID = origin.RequestID
	}
	entry.revision, entry.definition, entry.name = uuid.NewString(), definition, input.Name
	return entry, nil
}

func pgSandboxScenarioLibraryEntry(row sqlc.AppApiSandboxScenario) sandboxScenarioLibraryEntry {
	return sandboxScenarioLibraryEntry{savedAPISandboxScenario{pgUUIDString(row.Revision), row.Definition, pgUUIDString(row.SourceRequestID)}, row.Name}
}

func (s *PgStore) ListAPISandboxLibraryScenarios(ctx context.Context, appID string) ([]api.APISandboxLibraryScenarioSummary, error) {
	rows, err := sqlc.New().ListAPISandboxLibraryScenarios(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]api.APISandboxLibraryScenarioSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.APISandboxLibraryScenarioSummary{ID: pgUUIDString(row.ID), Name: row.Name, Revision: pgUUIDString(row.Revision)})
	}
	return out, nil
}

func (s *PgStore) GetAPISandboxLibraryScenario(ctx context.Context, appID, id string) (api.APISandboxLibraryScenario, error) {
	row, err := sqlc.New().GetAPISandboxLibraryScenario(ctx, s.pool, sqlc.GetAPISandboxLibraryScenarioParams{AppID: mustPgUUID(appID), ID: mustPgUUID(id)})
	if err != nil {
		return api.APISandboxLibraryScenario{}, mapErr(err)
	}
	return pgSandboxScenarioLibraryEntry(row).response(id)
}

func (s *PgStore) GetAPISandboxLibraryScenarioByRevision(ctx context.Context, appID, revision string) (api.APISandboxScenario, error) {
	row, err := sqlc.New().GetAPISandboxLibraryScenarioByRevision(ctx, s.pool, sqlc.GetAPISandboxLibraryScenarioByRevisionParams{AppID: mustPgUUID(appID), Revision: mustPgUUID(revision)})
	if err != nil {
		return api.APISandboxScenario{}, mapErr(err)
	}
	return pgSandboxScenarioLibraryEntry(row).decode()
}

func (s *PgStore) PutAPISandboxLibraryScenario(ctx context.Context, appID, id string, input api.APISandboxScenarioInput, origin APISandboxScenarioOrigin) (api.APISandboxLibraryScenario, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.APISandboxLibraryScenario{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	app, scenarioID := mustPgUUID(appID), mustPgUUID(id)
	if _, err := q.LockAPISandboxScenarioLibrary(ctx, tx, app); err != nil {
		return api.APISandboxLibraryScenario{}, mapErr(err)
	}
	row, err := q.GetAPISandboxLibraryScenario(ctx, tx, sqlc.GetAPISandboxLibraryScenarioParams{AppID: app, ID: scenarioID})
	if err != nil && !errors.Is(mapErr(err), ErrNotFound) {
		return api.APISandboxLibraryScenario{}, mapErr(err)
	}
	current := pgSandboxScenarioLibraryEntry(row)
	entry, err := current.update(input, origin)
	if err != nil {
		return api.APISandboxLibraryScenario{}, err
	}
	if current.revision == entry.revision {
		return current.response(id)
	}
	if current.revision == "" {
		count, err := q.CountAPISandboxLibraryScenarios(ctx, tx, app)
		if err != nil {
			return api.APISandboxLibraryScenario{}, mapErr(err)
		}
		if count >= api.APISandboxScenarioLibraryMaxItems {
			return api.APISandboxLibraryScenario{}, ErrAPISandboxScenarioLibraryFull
		}
	}
	var requestID pgtype.UUID
	if entry.sourceRequestID != "" {
		requestID = mustPgUUID(entry.sourceRequestID)
	}
	_, err = q.PutAPISandboxLibraryScenario(ctx, tx, sqlc.PutAPISandboxLibraryScenarioParams{
		AppID: app, ID: scenarioID, Revision: mustPgUUID(entry.revision),
		Name: entry.name, Definition: entry.definition, SourceRequestID: requestID,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "app_api_sandbox_scenarios_app_id_name_key" {
			return api.APISandboxLibraryScenario{}, ErrAPISandboxScenarioNameTaken
		}
		return api.APISandboxLibraryScenario{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return api.APISandboxLibraryScenario{}, mapErr(err)
	}
	return entry.response(id)
}

func (s *PgStore) DeleteAPISandboxLibraryScenario(ctx context.Context, appID, id, revision string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	app := mustPgUUID(appID)
	if _, err := q.LockAPISandboxScenarioLibrary(ctx, tx, app); err != nil {
		return mapErr(err)
	}
	n, err := q.DeleteAPISandboxLibraryScenario(ctx, tx, sqlc.DeleteAPISandboxLibraryScenarioParams{AppID: app, ID: mustPgUUID(id), Revision: mustPgUUID(revision)})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}

func (m *MemStore) ListAPISandboxLibraryScenarios(_ context.Context, appID string) ([]api.APISandboxLibraryScenarioSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries := m.apiSandboxScenarioLibrary[appID]
	out := make([]api.APISandboxLibraryScenarioSummary, 0, len(entries))
	for id, entry := range entries {
		out = append(out, api.APISandboxLibraryScenarioSummary{ID: id, Name: entry.name, Revision: entry.revision})
	}
	slices.SortFunc(out, func(a, b api.APISandboxLibraryScenarioSummary) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (m *MemStore) GetAPISandboxLibraryScenario(_ context.Context, appID, id string) (api.APISandboxLibraryScenario, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.apiSandboxScenarioLibrary[appID][id]
	if !ok {
		return api.APISandboxLibraryScenario{}, ErrNotFound
	}
	return entry.response(id)
}

func (m *MemStore) GetAPISandboxLibraryScenarioByRevision(_ context.Context, appID, revision string) (api.APISandboxScenario, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range m.apiSandboxScenarioLibrary[appID] {
		if entry.revision == revision {
			return entry.decode()
		}
	}
	return api.APISandboxScenario{}, ErrNotFound
}

func (m *MemStore) PutAPISandboxLibraryScenario(ctx context.Context, appID, id string, input api.APISandboxScenarioInput, origin APISandboxScenarioOrigin) (api.APISandboxLibraryScenario, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return api.APISandboxLibraryScenario{}, err
	}
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted {
		return api.APISandboxLibraryScenario{}, ErrNotFound
	}
	entries := m.apiSandboxScenarioLibrary[appID]
	current := entries[id]
	entry, err := current.update(input, origin)
	if err != nil {
		return api.APISandboxLibraryScenario{}, err
	}
	if current.revision == "" && len(entries) >= api.APISandboxScenarioLibraryMaxItems {
		return api.APISandboxLibraryScenario{}, ErrAPISandboxScenarioLibraryFull
	}
	for other, existing := range entries {
		if other != id && existing.name == entry.name {
			return api.APISandboxLibraryScenario{}, ErrAPISandboxScenarioNameTaken
		}
	}
	if m.apiSandboxScenarioLibrary == nil {
		m.apiSandboxScenarioLibrary = make(map[string]map[string]sandboxScenarioLibraryEntry)
	}
	if entries == nil {
		entries = make(map[string]sandboxScenarioLibraryEntry)
		m.apiSandboxScenarioLibrary[appID] = entries
	}
	entries[id] = entry
	return entry.response(id)
}

func (m *MemStore) DeleteAPISandboxLibraryScenario(ctx context.Context, appID, id, revision string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return err
	}
	entries := m.apiSandboxScenarioLibrary[appID]
	current, ok := entries[id]
	if !ok || current.revision != revision {
		return ErrConflict
	}
	delete(entries, id)
	return nil
}
