package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardLogConsumerClosureStore = (*PgStore)(nil)

func (s *PgStore) CloseApplicationStandardLogConsumer(ctx context.Context, session ApplicationStandardLogConsumerSession) (ApplicationStandardLogConsumerClosure, error) {
	if !validStandardLogSession(session) {
		return ApplicationStandardLogConsumerClosure{}, ErrInvalidArgument
	}
	raw, err := sqlc.New().CloseApplicationStandardLogConsumer(ctx, s.pool, sqlc.CloseApplicationStandardLogConsumerParams{NodeID: mustPgUUID(session.NodeID), SessionID: mustPgUUID(session.SessionID), Generation: session.Generation})
	if errors.Is(err, pgx.ErrNoRows) {
		return ApplicationStandardLogConsumerClosure{}, ErrApplicationStandardLogConsumerFenced
	}
	if err != nil {
		return ApplicationStandardLogConsumerClosure{}, standardLogInventoryPGError(err)
	}
	var c ApplicationStandardLogConsumerClosure
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("decode standard logging consumer closure: %w", err)
	}
	return c, nil
}
