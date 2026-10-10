package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// DevWatchStore holds developer watch-mode settings (ADR-970): apid writes
// the development command when the CLI upserts a developer session, and
// builderd reads it for that app's developer builds. It is an optional
// Store capability so older test doubles keep working.
type DevWatchStore interface {
	// SetDevWatchCommand stores command for appID; an empty command turns
	// watch mode off.
	SetDevWatchCommand(ctx context.Context, appID, command string) error
	// DevWatchCommand returns appID's command, or "" when watch mode is off.
	DevWatchCommand(ctx context.Context, appID string) (string, error)
}

var (
	_ DevWatchStore = (*PgStore)(nil)
	_ DevWatchStore = (*MemStore)(nil)
)

// SetDevWatchCommand implements DevWatchStore.
func (s *PgStore) SetDevWatchCommand(ctx context.Context, appID, command string) error {
	app, err := parsePgUUID(appID)
	if err != nil {
		return err
	}
	if command == "" {
		err = sqlc.New().DeleteDevWatchSetting(ctx, s.pool, app)
	} else {
		err = sqlc.New().UpsertDevWatchSetting(ctx, s.pool, sqlc.UpsertDevWatchSettingParams{AppID: app, Command: command})
	}
	if err != nil {
		return fmt.Errorf("state: set developer watch command: %w", err)
	}
	return nil
}

// DevWatchCommand implements DevWatchStore.
func (s *PgStore) DevWatchCommand(ctx context.Context, appID string) (string, error) {
	app, err := parsePgUUID(appID)
	if err != nil {
		return "", err
	}
	command, err := sqlc.New().GetDevWatchSetting(ctx, s.pool, app)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("state: load developer watch command: %w", err)
	}
	return command, nil
}

// SetDevWatchCommand mirrors PgStore.SetDevWatchCommand.
func (m *MemStore) SetDevWatchCommand(_ context.Context, appID, command string) error {
	if appID == "" {
		return errors.New("state: developer watch command needs an app id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if command == "" {
		delete(m.devWatchCommands, appID)
		return nil
	}
	if m.devWatchCommands == nil {
		m.devWatchCommands = map[string]string{}
	}
	m.devWatchCommands[appID] = command
	return nil
}

// DevWatchCommand mirrors PgStore.DevWatchCommand.
func (m *MemStore) DevWatchCommand(_ context.Context, appID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.devWatchCommands[appID], nil
}
