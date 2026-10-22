package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// DevWatchSetting is a developer app's watch mode (ADR-1170).
type DevWatchSetting struct {
	// Command is the development command; "" means watch mode is off.
	Command string
	// Host is the developer app's public hostname, which the watch-mode
	// image hands to its development server. It may be empty.
	Host string
}

// DevWatchStore holds developer watch-mode settings (ADR-1170): apid writes
// them when the CLI upserts a developer session, and builderd reads them for
// that app's developer builds. It is an optional Store capability so older
// test doubles keep working.
type DevWatchStore interface {
	// SetDevWatch stores setting for appID; an empty Command turns watch
	// mode off.
	SetDevWatch(ctx context.Context, appID string, setting DevWatchSetting) error
	// DevWatch returns appID's setting, with an empty Command when watch
	// mode is off.
	DevWatch(ctx context.Context, appID string) (DevWatchSetting, error)
}

var (
	_ DevWatchStore = (*PgStore)(nil)
	_ DevWatchStore = (*MemStore)(nil)
)

// SetDevWatch implements DevWatchStore.
func (s *PgStore) SetDevWatch(ctx context.Context, appID string, setting DevWatchSetting) error {
	app, err := parsePgUUID(appID)
	if err != nil {
		return err
	}
	if setting.Command == "" {
		err = sqlc.New().DeleteDevWatchSetting(ctx, s.pool, app)
	} else {
		err = sqlc.New().UpsertDevWatchSetting(ctx, s.pool, sqlc.UpsertDevWatchSettingParams{AppID: app, Command: setting.Command, Host: setting.Host})
	}
	if err != nil {
		return fmt.Errorf("state: set developer watch mode: %w", err)
	}
	return nil
}

// DevWatch implements DevWatchStore.
func (s *PgStore) DevWatch(ctx context.Context, appID string) (DevWatchSetting, error) {
	app, err := parsePgUUID(appID)
	if err != nil {
		return DevWatchSetting{}, err
	}
	row, err := sqlc.New().GetDevWatchSetting(ctx, s.pool, app)
	if errors.Is(err, pgx.ErrNoRows) {
		return DevWatchSetting{}, nil
	}
	if err != nil {
		return DevWatchSetting{}, fmt.Errorf("state: load developer watch mode: %w", err)
	}
	return DevWatchSetting{Command: row.Command, Host: row.Host}, nil
}

// SetDevWatch mirrors PgStore.SetDevWatch.
func (m *MemStore) SetDevWatch(_ context.Context, appID string, setting DevWatchSetting) error {
	if appID == "" {
		return errors.New("state: developer watch mode needs an app id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if setting.Command == "" {
		delete(m.devWatch, appID)
		return nil
	}
	if m.devWatch == nil {
		m.devWatch = map[string]DevWatchSetting{}
	}
	m.devWatch[appID] = setting
	return nil
}

// DevWatch mirrors PgStore.DevWatch.
func (m *MemStore) DevWatch(_ context.Context, appID string) (DevWatchSetting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.devWatch[appID], nil
}
