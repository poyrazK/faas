package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// applyBatchEventTriggers applies a batch manifest's event_triggers to the
// target app through the same manifest apply a source deploy uses
// (ADR-1342). A functions manifest subscribes each function this way after
// its deployment, since function deploys never apply manifest triggers.
// Declarations for other apps are skipped, existing subscriptions are kept,
// and a failure rolls back what this call created or changed.
func (s *server) applyBatchEventTriggers(ctx context.Context, acct state.Account, app state.App, declared []gregalemanifest.EventTrigger) *batchError {
	if len(declared) == 0 {
		return nil
	}
	manifest := &gregalemanifest.Manifest{EventTriggers: declared}
	// A source deploy refuses event_triggers while event subscriptions are
	// off for the account; this path does too (ADR-20261027125625).
	if problem := s.manifestSettingsProblem(acct, manifest); problem != nil {
		return &batchError{Message: "event_triggers: " + problem.Detail}
	}
	staged, problem := s.applySourceRefManifest(ctx, acct, app, manifest, "", true)
	if problem == nil {
		return nil
	}
	if err := s.rollbackSourceRefManifest(ctx, staged); err != nil {
		s.log.Warn("roll back batch event triggers", "app_id", app.ID, "err", err)
	}
	return &batchError{Message: "event_triggers: " + problem.Detail}
}
