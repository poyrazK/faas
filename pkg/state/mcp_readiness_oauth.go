package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrMCPReadinessOAuthBusy = errors.New("MCP OAuth connection is busy")
var ErrMCPReadinessOAuthReconnect = errors.New("MCP OAuth connection requires reconnection")
var ErrMCPReadinessOAuthChanged = errors.New("MCP OAuth connection binding changed")

type MCPReadinessOAuthStore interface {
	ClaimMCPReadinessOAuth(context.Context, string, string, api.MCPReadinessOAuthConnection, string) (MCPCatalogObserverJob, error)
	ReleaseMCPReadinessOAuth(context.Context, MCPCatalogObserverJob) error
}

func readinessOAuthView(root api.MCPCatalogMonitorSnapshot, ref api.MCPReadinessOAuthConnection, endpoint string) (api.MCPCatalogMonitorSnapshot, error) {
	view, ok := api.CatalogMonitorRoleSnapshot(root, ref.Role)
	cfg := view.Config.Observer
	if !ok || api.MCPConnectionScope(view.Config.Scope) != api.MCPConnectionScope(ref.Scope) || view.Config.Revision != ref.Revision || !view.Config.Enabled || cfg == nil || !cfg.Enabled || cfg.Paused || cfg.OAuth == nil || cfg.Endpoint != endpoint {
		return view, ErrMCPReadinessOAuthChanged
	}
	return view, nil
}
func claimReadinessOAuth(root *api.MCPCatalogMonitorSnapshot, job *MCPCatalogObserverJob, ref api.MCPReadinessOAuthConnection, endpoint string, at time.Time) error {
	view, err := readinessOAuthView(*root, ref, endpoint)
	if err != nil {
		return err
	}
	st := view.ObserverStatus
	if st != nil && (st.LeaseUntil != nil && st.LeaseUntil.After(at) || st.OAuthFlow != nil && st.OAuthFlow.ExpiresAt.After(at)) {
		return ErrMCPReadinessOAuthBusy
	}
	if st == nil || st.OAuth == nil || st.OAuth.State != "connected" {
		return ErrMCPReadinessOAuthReconnect
	}
	// Do not alter catalog cadence or claim to have performed a catalog check.
	stCopy := *st
	until := at.Add(time.Duration(api.MCPCatalogObserverLeaseSeconds) * time.Second)
	stCopy.LeaseID = uuid.NewString()
	stCopy.LeaseUntil = &until
	stCopy.State = "checking"
	view.ObserverStatus = &stCopy
	setObserverView(root, ref.Role, view)
	job.Role = ref.Role
	job.LeaseID = stCopy.LeaseID
	job.Snapshot = view
	return nil
}
func releaseReadinessOAuth(root *api.MCPCatalogMonitorSnapshot, job MCPCatalogObserverJob, at time.Time) error {
	view, ok := api.CatalogMonitorRoleSnapshot(*root, job.Role)
	st := view.ObserverStatus
	if !ok || view.Config.Revision != job.Snapshot.Config.Revision || st == nil || job.LeaseID == "" || st.LeaseID != job.LeaseID || st.LeaseUntil == nil || !st.LeaseUntil.After(at) {
		return ErrMCPCatalogMonitorChanged
	}
	copy := *st
	copy.LeaseID = ""
	copy.LeaseUntil = nil
	copy.State = "scheduled"
	if copy.FailureReason != "" {
		copy.State = "degraded"
	}
	view.ObserverStatus = &copy
	setObserverView(root, job.Role, view)
	return nil
}
func (s *PgStore) ClaimMCPReadinessOAuth(ctx context.Context, account, app string, ref api.MCPReadinessOAuthConnection, endpoint string) (MCPCatalogObserverJob, error) {
	job := MCPCatalogObserverJob{AccountID: account, AppID: app}
	_, err := s.editMCPCatalogMonitor(ctx, account, app, false, func(root *api.MCPCatalogMonitorSnapshot, at time.Time) error {
		return claimReadinessOAuth(root, &job, ref, endpoint, at)
	})
	return job, err
}
func (m *MemStore) ClaimMCPReadinessOAuth(_ context.Context, account, app string, ref api.MCPReadinessOAuthConnection, endpoint string) (MCPCatalogObserverJob, error) {
	job := MCPCatalogObserverJob{AccountID: account, AppID: app}
	_, err := m.editMCPCatalogMonitor(account, app, false, func(root *api.MCPCatalogMonitorSnapshot, at time.Time) error {
		return claimReadinessOAuth(root, &job, ref, endpoint, at)
	})
	return job, err
}
func (s *PgStore) ReleaseMCPReadinessOAuth(ctx context.Context, job MCPCatalogObserverJob) error {
	_, err := s.editMCPCatalogMonitor(ctx, job.AccountID, job.AppID, false, func(root *api.MCPCatalogMonitorSnapshot, at time.Time) error {
		return releaseReadinessOAuth(root, job, at)
	})
	return err
}
func (m *MemStore) ReleaseMCPReadinessOAuth(_ context.Context, job MCPCatalogObserverJob) error {
	_, err := m.editMCPCatalogMonitor(job.AccountID, job.AppID, false, func(root *api.MCPCatalogMonitorSnapshot, at time.Time) error {
		return releaseReadinessOAuth(root, job, at)
	})
	return err
}

// This binding deliberately excludes token ciphertext, expiry, lease and ordinary
// renewal timestamps. Rotating tokens under the same grant preserves evidence;
// changing the grant, authorization state or connection invalidates it.
func readinessOAuthBinding(view api.MCPCatalogMonitorSnapshot) any {
	out := map[string]any{"scope": api.MCPConnectionScope(view.Config.Scope), "revision": view.Config.Revision, "enabled": view.Config.Enabled}
	if cfg := view.Config.Observer; cfg != nil {
		out["observer_enabled"] = cfg.Enabled
		out["paused"] = cfg.Paused
		out["endpoint"] = cfg.Endpoint
		out["oauth"] = cfg.OAuth != nil
	}
	if st := view.ObserverStatus; st != nil {
		if auth := st.OAuth; auth != nil {
			out["state"] = auth.State
			out["source"] = auth.SourceSHA256
			out["grant"] = auth.RetirementID
			out["scopes"] = auth.GrantedScopes
		}
		if flow := st.OAuthFlow; flow != nil {
			out["flow"] = flow.StateSHA256
			out["flow_consumed"] = flow.Consumed
		}
	}
	if lifecycle := view.OAuthLifecycle; lifecycle != nil {
		out["credential_state"] = lifecycle.CredentialState
		out["local_state"] = lifecycle.LocalState
	}
	return out
}
func (m *MemStore) readinessArtifactLocked(d Deployment) string {
	original := readinessArtifact(d, m.apps[d.AppID], m.secrets)
	bindings := map[string]any{}
	for _, role := range m.readinessPolicyLocked(d.AppID, d.Scope).Roles {
		if !role.Monitor.ManagedOAuth() {
			continue
		}
		ref := role.Monitor.OAuthForScope(d.Scope)
		if ref == nil {
			bindings[role.Name] = nil
			continue
		}
		var view api.MCPCatalogMonitorSnapshot
		if m.mcpCatalogMonitorOwners[d.AppID] == m.apps[d.AppID].AccountID {
			view, _ = api.CatalogMonitorRoleSnapshot(m.mcpCatalogMonitors[d.AppID], ref.Role)
		}
		bindings[role.Name] = readinessOAuthBinding(view)
	}
	if len(bindings) == 0 {
		return original
	}
	data, _ := json.Marshal([]any{original, bindings})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validateReadinessOAuthReferences(root api.MCPCatalogMonitorSnapshot, r api.SetMCPReadinessPolicyRequest) error {
	for _, role := range r.Roles {
		if role.Monitor.ManagedOAuth() && (r.Enabled || role.Monitor.Enabled) {
			ref := role.Monitor.OAuthForScope(r.Scope)
			if ref == nil {
				return ErrInvalidArgument
			}
			if _, err := readinessOAuthView(root, *ref, r.Endpoint); err != nil {
				return ErrInvalidArgument
			}
		}
	}
	return nil
}
