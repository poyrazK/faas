package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PreviewDatabaseEnvKeyStore reports the environment keys a PR preview of an
// app receives from its own preview databases (ADR-20261102123523): the
// keys of the app's bindings in the scopes production serves, on personally
// owned databases whose previews setting is empty or copy. It is optional; a
// store without it reports nothing, and callers keep their stricter answer.
type PreviewDatabaseEnvKeyStore interface {
	AppPreviewDatabaseEnvKeys(ctx context.Context, accountID, appID string) ([]string, error)
}

func (s *PgStore) AppPreviewDatabaseEnvKeys(ctx context.Context, accountID, appID string) ([]string, error) {
	keys, err := sqlc.New().AppPreviewDatabaseEnvKeys(ctx, s.pool, sqlc.AppPreviewDatabaseEnvKeysParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list preview database keys: %w", err)
	}
	return keys, nil
}
