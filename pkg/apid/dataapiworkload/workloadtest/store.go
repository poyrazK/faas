// Package workloadtest is an in-memory dataapiworkload.Store for tests.
package workloadtest

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// Store holds one account, app, instance and deployment.
type Store struct {
	Account    state.Account
	App        state.App
	Instance   state.Instance
	Deployment state.Deployment
}

// Production is a live production instance of an active app.
func Production(accountID, appID, instanceID string) *Store {
	deployment := "dep-" + instanceID
	return &Store{
		Account:    state.Account{ID: accountID, Status: state.AccountActive},
		App:        state.App{ID: appID, AccountID: accountID, Status: state.AppActive},
		Instance:   state.Instance{ID: instanceID, AppID: appID, DeploymentID: deployment, State: string(state.StateRunning)},
		Deployment: state.Deployment{ID: deployment, AppID: appID, Scope: "production"},
	}
}

func (s *Store) AppByID(_ context.Context, id string) (state.App, error) {
	if id != s.App.ID {
		return state.App{}, state.ErrNotFound
	}
	return s.App, nil
}

func (s *Store) InstanceByID(_ context.Context, id string) (state.Instance, error) {
	if id != s.Instance.ID {
		return state.Instance{}, state.ErrNotFound
	}
	return s.Instance, nil
}

func (s *Store) DeploymentByID(_ context.Context, id string) (state.Deployment, error) {
	if id != s.Deployment.ID {
		return state.Deployment{}, state.ErrNotFound
	}
	return s.Deployment, nil
}

func (s *Store) AccountByID(_ context.Context, id string) (state.Account, error) {
	if id != s.Account.ID {
		return state.Account{}, state.ErrNotFound
	}
	return s.Account, nil
}

func (s *Store) OrgByID(context.Context, string) (state.Org, error) {
	return state.Org{}, state.ErrNotFound
}
