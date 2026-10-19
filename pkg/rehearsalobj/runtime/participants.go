package runtime

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/rehearsalobj"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/state"
)

type memberReader interface {
	ScenarioTestMemberByApp(context.Context, string) (state.ScenarioTestMember, error)
}

func (s *Service) deploymentBindings(ctx context.Context, account, run string) (map[string]rehearsalwork.Binding, error) {
	r, err := s.Catalog.GetScenarioObjects(ctx, account, run)
	if err != nil {
		return nil, err
	}
	members, ok := s.Catalog.(memberReader)
	if !ok {
		return nil, nil
	}
	m, err := members.ScenarioTestMemberByApp(ctx, r.AppID)
	if err != nil {
		return nil, err
	}
	if m.ManagedWorkJSON == "" {
		return nil, nil
	}
	store, ok := s.Catalog.(state.ScenarioActionSnapshotStore)
	if !ok {
		return nil, errors.New("managed-work evidence unavailable")
	}
	snapshot, err := store.SealScenarioTestActions(ctx, account, run)
	if err != nil || snapshot.Status != "complete" || snapshot.ManagedWork == nil || snapshot.ManagedWork.Validate() != nil {
		return nil, errors.New("managed-work evidence incomplete")
	}
	bindings := map[string]rehearsalwork.Binding{}
	for _, b := range snapshot.ManagedWork.Bindings {
		if b.ObjectsResourceID == r.ID {
			bindings[b.AppID] = b
		}
	}
	return bindings, nil
}
func validObjectAttempt(a rehearsalobj.Attempt, r rehearsalobj.Resource, primaryDeployment string, bindings map[string]rehearsalwork.Binding) bool {
	if bindings == nil {
		return (a.AppID == "" || a.AppID == r.AppID) && a.DeploymentID == primaryDeployment
	}
	b, ok := bindings[a.AppID]
	return ok && a.Workload == b.Workload && a.DeploymentID == b.DeploymentID && (a.Mutation == nil || a.Mutation.Workload == b.Workload)
}
