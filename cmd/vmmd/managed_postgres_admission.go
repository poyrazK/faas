// adr: 468 — vmmd reads admission; schedd retains lifecycle ownership.
package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

func managedPostgresAdmissionGuard(store state.Store) func(context.Context, string) error {
	if store == nil {
		// Legacy DB-less nodes retain their existing behavior. They cannot
		// supply cutover drain evidence; activation is still unavailable.
		return nil
	}
	reader, ok := store.(state.ManagedPostgresAdmissionReader)
	return func(ctx context.Context, appID string) error {
		if !ok {
			return fcvm.ErrAppAdmissionUnavailable
		}
		fenced, err := reader.ManagedPostgresAdmissionFenced(ctx, appID)
		if err != nil {
			if cancelled := ctx.Err(); cancelled != nil {
				return cancelled
			}
			return errors.Join(fcvm.ErrAppAdmissionUnavailable, err)
		}
		if fenced {
			return fcvm.ErrAppAdmissionFenced
		}
		return nil
	}
}
