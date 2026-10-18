package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type MCPCatalogOAuthRecoveryStore interface {
	RetryMCPCatalogOAuthRenewal(context.Context, string, string, string, string) (api.MCPCatalogOAuthResponse, error)
}

func retryCatalogOAuthRenewal(root *api.MCPCatalogMonitorSnapshot, role, revision string, at time.Time) (api.MCPCatalogOAuthResponse, error) {
	view, ok := api.CatalogMonitorRoleSnapshot(*root, role)
	if !ok {
		return api.MCPCatalogOAuthResponse{}, ErrNotFound
	}
	if view.Config.Revision != revision {
		return api.MCPCatalogOAuthResponse{}, ErrMCPCatalogMonitorChanged
	}
	if !api.MCPOAuthRenewalRetryAllowed(view, at) {
		return api.MCPCatalogOAuthResponse{}, ErrConflict
	}
	status := *view.ObserverStatus
	status.State = "scheduled"
	status.NextCheckAt = &at
	status.LeaseID, status.LeaseUntil = "", nil
	view.ObserverStatus = &status
	view.Config.Revision = uuid.NewString()
	setObserverView(root, role, view)
	return api.MCPCatalogOAuthResponse{State: "renewal_scheduled", Revision: view.Config.Revision}, nil
}

func (s *PgStore) RetryMCPCatalogOAuthRenewal(ctx context.Context, account, app, role, revision string) (out api.MCPCatalogOAuthResponse, err error) {
	_, err = s.editMCPCatalogMonitor(ctx, account, app, false, func(root *api.MCPCatalogMonitorSnapshot, at time.Time) error {
		var e error
		out, e = retryCatalogOAuthRenewal(root, role, revision, at)
		return e
	})
	return
}
func (m *MemStore) RetryMCPCatalogOAuthRenewal(_ context.Context, account, app, role, revision string) (out api.MCPCatalogOAuthResponse, err error) {
	_, err = m.editMCPCatalogMonitor(account, app, false, func(root *api.MCPCatalogMonitorSnapshot, at time.Time) error {
		var e error
		out, e = retryCatalogOAuthRenewal(root, role, revision, at)
		return e
	})
	return
}
