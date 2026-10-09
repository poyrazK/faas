package main

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
)

type checkpointObjectPublicationStore interface {
	state.ProjectEnvironmentCloneCheckpointObjectPublicationPlanStore
	state.ProjectEnvironmentCloneCheckpointObjectPublicationStore
}

// Private coordination only: gateway visibility still requires final ready state.
func (s *server) publishCheckpointObjects(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointReleaseDriver) (state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentCloneCheckpointObjectPublication, error) {
	zero := state.ProjectEnvironmentCloneCheckpointObjectPublication{}
	store, ok := s.store.(checkpointObjectPublicationStore)
	if !ok {
		return l, zero, state.ErrProjectEnvironmentCloneBindingCaptureUnavailable
	}
	var err error
	l, _, err = s.prepareCheckpointObjectCredentialValues(ctx, l, d)
	if err != nil {
		return l, zero, err
	}
	retention, err := s.checkpointTargetRetention(ctx, l, d)
	if err != nil {
		return l, zero, err
	}
	plan, err := store.ProjectEnvironmentCloneCheckpointObjectPublicationPlan(ctx, l, retention)
	if err != nil {
		return l, zero, err
	}
	retention, err = s.checkpointTargetRetention(ctx, l, d)
	if err != nil {
		return l, zero, err
	}
	out, err := store.PublishProjectEnvironmentCloneCheckpointObjects(ctx, l, plan.SHA256, retention)
	return l, out, err
}
