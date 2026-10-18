package state

import (
	"context"
	"crypto/subtle"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type MCPCatalogOAuthStore interface {
	ChangeMCPCatalogOAuth(context.Context, string, string, string, string, string, string, *api.MCPCatalogOAuthFlow, *api.MCPCatalogObserverOAuthStatus, ...[]byte) (api.MCPCatalogMonitorSnapshot, error)
}

func changeCatalogOAuth(root *api.MCPCatalogMonitorSnapshot, role, revision, action, hash string, flow *api.MCPCatalogOAuthFlow, oauth *api.MCPCatalogObserverOAuthStatus, at time.Time) error {
	view, ok := api.CatalogMonitorRoleSnapshot(*root, role)
	if !ok {
		return ErrNotFound
	}
	cfg := view.Config.Observer
	if cfg == nil || cfg.OAuth == nil || !cfg.OAuth.Guided || view.Config.Revision != revision {
		return ErrMCPCatalogMonitorChanged
	}
	if view.ObserverStatus == nil {
		view.ObserverStatus = &api.MCPCatalogObserverStatus{State: "scheduled"}
	}
	status := *view.ObserverStatus
	switch action {
	case "begin":
		if flow != nil && flow.BrowserSessionSHA256 != "" {
			if len(flow.BrowserSessionSHA256) != 64 {
				return ErrInvalidArgument
			}
			if status.LeaseUntil != nil && status.LeaseUntil.After(at) || status.OAuthFlow != nil && status.OAuthFlow.Consumed && status.OAuthFlow.ExpiresAt.After(at) {
				return ErrConflict
			}
		}
		if view.OAuthLifecycle != nil && view.OAuthLifecycle.Revocation != nil && view.OAuthLifecycle.Revocation.State == "pending" {
			return ErrMCPCatalogMonitorChanged
		}
		if flow == nil || flow.Consumed || len(flow.StateSHA256) != 64 || len(flow.Ciphertext) == 0 || len(flow.Ciphertext) > api.MCPCatalogOAuthMaxResponseBytes || !flow.ExpiresAt.After(at) || flow.ExpiresAt.After(at.Add(time.Duration(api.MCPCatalogOAuthFlowSeconds)*time.Second)) {
			return ErrInvalidArgument
		}
		status.OAuthFlow = flow
	case "consume", "finish", "abort":
		pending := status.OAuthFlow
		if pending == nil || !pending.ExpiresAt.After(at) || len(hash) != 64 || subtle.ConstantTimeCompare([]byte(hash), []byte(pending.StateSHA256)) != 1 {
			return ErrMCPCatalogMonitorChanged
		}
		switch action {
		case "abort":
			if !pending.Consumed {
				return ErrMCPCatalogMonitorChanged
			}
			status.OAuthFlow = nil
		case "consume":
			if pending.Consumed {
				return ErrMCPCatalogMonitorChanged
			}
			copy := *pending
			copy.Consumed = true
			status.OAuthFlow = &copy
		default:
			if !pending.Consumed || oauth == nil || oauth.State != "connected" || oauth.ExpiresAt == nil || !oauth.ExpiresAt.After(at) || oauth.LastRenewalAt == nil || len(oauth.Ciphertext) == 0 || len(oauth.Ciphertext) > api.MCPCatalogOAuthMaxResponseBytes || len(oauth.SourceSHA256) != 64 {
				return ErrInvalidArgument
			}
			if status.OAuth != nil {
				phase := retirementPhase(cfg.OAuth, status.OAuth, at)
				lifecycleEvent(&view, "disconnected", at)
				view.OAuthLifecycle.Revocation = phase
			}
			status.Renewal = nil
			status.OAuth = oauth
			lifecycleEvent(&view, "connected", at)
			view.OAuthLifecycle.LocalState = "connected"
			view.OAuthLifecycle.CredentialState = ""
			status.OAuthFlow = nil
		}
	case "disconnect":
		if status.OAuth != nil {
			if view.OAuthLifecycle != nil && view.OAuthLifecycle.Revocation != nil && view.OAuthLifecycle.Revocation.State == "pending" {
				return ErrMCPCatalogMonitorChanged
			}
			lifecycleEvent(&view, "disconnected", at)
			view.OAuthLifecycle.LocalState = "disconnected"
			job := retirementPhase(cfg.OAuth, status.OAuth, at)
			if job.State == "manual_required" {
				lifecycleEvent(&view, "revocation_manual_required", at)
			}
			view.OAuthLifecycle.Revocation = job
		}
		status.Renewal = nil
		view.OAuthLifecycle.CredentialState = ""
		status.OAuth = nil
		status.OAuthFlow = nil
	default:
		return ErrInvalidArgument
	}
	if action != "consume" {
		view.Config.Revision = uuid.NewString()
		view.Latest = nil
		view.CheckedAt = nil
		status.State = "scheduled"
		status.LeaseID = ""
		status.LeaseUntil = nil
		status.NextCheckAt = nil
		status.FailureReason = ""
		status.ConsecutiveFailures = 0
	}
	view.ObserverStatus = &status
	setObserverView(root, role, view)
	return nil
}
func (s *PgStore) ChangeMCPCatalogOAuth(ctx context.Context, account, app, role, revision, action, hash string, flow *api.MCPCatalogOAuthFlow, oauth *api.MCPCatalogObserverOAuthStatus, clientCiphertext ...[]byte) (api.MCPCatalogMonitorSnapshot, error) {
	root, err := s.editMCPCatalogMonitor(ctx, account, app, false, func(root *api.MCPCatalogMonitorSnapshot, at time.Time) error {
		if err := changeCatalogOAuth(root, role, revision, action, hash, flow, oauth, at); err != nil {
			return err
		}
		return captureRevocationClient(root, role, action, clientCiphertext)
	})
	view, _ := api.CatalogMonitorRoleSnapshot(root, role)
	return view, err
}
func (m *MemStore) ChangeMCPCatalogOAuth(_ context.Context, account, app, role, revision, action, hash string, flow *api.MCPCatalogOAuthFlow, oauth *api.MCPCatalogObserverOAuthStatus, clientCiphertext ...[]byte) (api.MCPCatalogMonitorSnapshot, error) {
	root, err := m.editMCPCatalogMonitor(account, app, false, func(root *api.MCPCatalogMonitorSnapshot, at time.Time) error {
		if err := changeCatalogOAuth(root, role, revision, action, hash, flow, oauth, at); err != nil {
			return err
		}
		return captureRevocationClient(root, role, action, clientCiphertext)
	})
	view, _ := api.CatalogMonitorRoleSnapshot(root, role)
	return view, err
}

func lifecycleEvent(view *api.MCPCatalogMonitorSnapshot, kind string, at time.Time) {
	if view.OAuthLifecycle == nil {
		view.OAuthLifecycle = &api.MCPCatalogOAuthLifecycle{}
	}
	history := append(view.OAuthLifecycle.History, api.MCPCatalogOAuthEvent{ID: uuid.NewString(), At: at, Kind: kind})
	if len(history) > api.MCPCatalogOAuthMaxLifecycleHistory {
		history = history[len(history)-api.MCPCatalogOAuthMaxLifecycleHistory:]
	}
	view.OAuthLifecycle.History = history
}

func captureRevocationClient(root *api.MCPCatalogMonitorSnapshot, role, action string, ciphertext [][]byte) error {
	if action != "disconnect" || len(ciphertext) == 0 {
		return nil
	}
	if len(ciphertext) != 1 || len(ciphertext[0]) > api.MCPCatalogOAuthMaxResponseBytes {
		return ErrInvalidArgument
	}
	view, ok := api.CatalogMonitorRoleSnapshot(*root, role)
	if !ok {
		return ErrNotFound
	}
	if view.OAuthLifecycle != nil && view.OAuthLifecycle.Revocation != nil && view.OAuthLifecycle.Revocation.State == "pending" && view.OAuthLifecycle.Revocation.ClientCiphertext == nil {
		view.OAuthLifecycle.Revocation.ClientCiphertext = append([]byte(nil), ciphertext[0]...)
		setObserverView(root, role, view)
	}
	return nil
}
