package sched

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Engine) checkManagedPostgresAdmission(ctx context.Context, appID string) error {
	reader, ok := e.store.(state.ManagedPostgresAdmissionReader)
	if !ok {
		return nil
	}
	fenced, err := reader.ManagedPostgresAdmissionFenced(ctx, appID)
	if err != nil {
		return fmt.Errorf("sched: read database cutover admission barrier: %w", err)
	}
	if fenced {
		return state.ErrManagedPostgresAdmissionFenced
	}
	return nil
}
