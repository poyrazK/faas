package sched

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

// jobRunAttach returns an interactive image run's session (ADR-1330), or nil
// for an ordinary run. A store that cannot hold sessions cannot have admitted
// an interactive run.
func (e *Engine) jobRunAttach(ctx context.Context, runID string) (*state.JobRunAttach, error) {
	store, ok := e.store.(state.JobRunAttachStore)
	if !ok {
		return nil, nil
	}
	attach, err := store.JobRunAttachSession(ctx, runID)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &attach, nil
}
