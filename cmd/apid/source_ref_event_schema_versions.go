package main

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
	"slices"
)

type sourceRefEventVersionsChange struct {
	subscriptionID    string
	previous, applied []string
}

func (s *server) applySourceRefEventVersions(ctx context.Context, account, app string, row state.EventSubscription, declaration gregalemanifest.EventTrigger, created bool, staged *sourceRefManifestStaged) *api.Problem {
	versions, err := api.NormalizeEventSchemaVersions(declaration.SchemaVersions)
	if err != nil {
		return api.ErrValidation(err.Error())
	}
	if slices.Equal(row.SchemaVersions, versions) {
		return nil
	}
	store, ok := s.store.(state.EventSubscriptionSchemaVersionsStore)
	if !ok {
		return api.ErrCapacity("event schema version selection unavailable")
	}
	previous, err := store.SetEventSubscriptionSchemaVersions(ctx, account, app, row.ID, versions, row.SchemaVersions)
	if err != nil {
		return api.ErrCapacity("could not reconcile event schema versions")
	}
	if !created {
		staged.eventVersionChanges = append(staged.eventVersionChanges, sourceRefEventVersionsChange{row.ID, previous, versions})
	}
	return nil
}
func (s *server) rollbackSourceRefEventVersions(ctx context.Context, staged sourceRefManifestStaged) error {
	if len(staged.eventVersionChanges) == 0 {
		return nil
	}
	store, ok := s.store.(state.EventSubscriptionSchemaVersionsStore)
	if !ok {
		return errors.New("event schema version selection unavailable during rollback")
	}
	var errs []error
	for i := len(staged.eventVersionChanges) - 1; i >= 0; i-- {
		c := staged.eventVersionChanges[i]
		if _, err := store.SetEventSubscriptionSchemaVersions(ctx, staged.accountID, staged.appID, c.subscriptionID, c.previous, c.applied); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
