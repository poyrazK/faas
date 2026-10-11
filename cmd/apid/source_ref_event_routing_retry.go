package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

type sourceRefEventRetryChange struct {
	subscriptionID    string
	previous, applied *api.EventRoutingRetryPolicy
}

func (s *server) applySourceRefEventRetry(ctx context.Context, account, app string, row state.EventSubscription, declaration gregalemanifest.EventTrigger, created bool, staged *sourceRefManifestStaged) *api.Problem {
	policy, err := declaration.RoutingRetryPolicy()
	if err != nil {
		return api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid, "Invalid manifest", err.Error())
	}
	if sameSourceRefEventRetry(row.RoutingRetryPolicy, policy) {
		return nil
	}
	store, ok := s.store.(state.EventRoutingRetryPolicyStore)
	if !ok {
		return api.ErrCapacity("event routing retry policies unavailable")
	}
	previous, err := store.SetEventSubscriptionRetryPolicy(ctx, account, app, row.ID, policy, row.RoutingRetryPolicy)
	if err != nil {
		return api.ErrCapacity("could not reconcile event routing retry policy")
	}
	if !created {
		staged.eventRetryChanges = append(staged.eventRetryChanges, sourceRefEventRetryChange{subscriptionID: row.ID, previous: previous, applied: policy})
	}
	return nil
}
func sameSourceRefEventRetry(a, b *api.EventRoutingRetryPolicy) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func (s *server) rollbackSourceRefEventRetry(ctx context.Context, staged sourceRefManifestStaged) error {
	if len(staged.eventRetryChanges) == 0 {
		return nil
	}
	store, ok := s.store.(state.EventRoutingRetryPolicyStore)
	if !ok {
		return errors.New("event routing retry policies unavailable during rollback")
	}
	var errs []error
	for i := len(staged.eventRetryChanges) - 1; i >= 0; i-- {
		c := staged.eventRetryChanges[i]
		if _, err := store.SetEventSubscriptionRetryPolicy(ctx, staged.accountID, staged.appID, c.subscriptionID, c.previous, c.applied); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
