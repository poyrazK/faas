package state

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"strconv"
	"time"
)

// MCPCatalogOAuthSecretSource identifies a customer-controlled secret generation.
// Maintenance resealing preserves SecretVersion and therefore this identifier.
func MCPCatalogOAuthSecretSource(secret AppSecret) string {
	sum := sha256.Sum256([]byte(secret.AppID + "\n" + secret.Scope + "\n" + secret.Key + "\n" + strconv.FormatInt(secret.SecretVersion, 10)))
	return hex.EncodeToString(sum[:])
}

func (m *MemStore) credentialChangeLocked(account, app, scope, key string, deleted bool, at time.Time) error {
	if m.mcpCatalogMonitorOwners[app] != account {
		return nil
	}
	root, ok := m.mcpCatalogMonitors[app]
	if !ok {
		return nil
	}
	root = cloneMCPCatalogMonitor(root)
	before := cloneMCPCatalogMonitor(root)
	saved := make(map[string]mcpRetirementEntry, len(m.mcpOAuthRetirements))
	for id, r := range m.mcpOAuthRetirements {
		saved[id] = r
	}
	success := false
	defer func() {
		if !success {
			m.mcpOAuthRetirements = saved
		}
	}()
	for role, view := range retirementViews(root) {
		if api.MCPConnectionScope(view.Config.Scope) != api.MCPConnectionScope(scope) || view.Config.Observer == nil || view.Config.Observer.OAuth == nil {
			continue
		}
		cfg := view.Config.Observer.OAuth
		refresh := cfg.RefreshTokenSecretRef == api.SecretRefPrefix+key
		client := cfg.ClientSecretRef == api.SecretRefPrefix+key
		if !refresh && !client {
			continue
		}
		if view.ObserverStatus == nil {
			view.ObserverStatus = &api.MCPCatalogObserverStatus{State: "scheduled"}
		}
		st := *view.ObserverStatus
		if journal := st.Renewal; journal != nil && (journal.State == "pending" || journal.State == "reauthorization_required") && journal.Result == nil {
			frozen := *cfg
			phase := &api.MCPCatalogOAuthRevocation{ID: journal.ID, State: "pending", NextAttemptAt: at.Add(time.Duration(api.MCPCatalogOAuthFlowSeconds) * time.Second), Config: &frozen}
			r := newRetirement(app, m.apps[app].Slug, role, "credential_changed", phase, at, scope)
			r.State = "awaiting_issuance"
			m.captureRetirementClientLocked(account, app, phase, scope)
			if err := m.insertRetirementLocked(account, r); err != nil {
				return err
			}
		}
		if refresh || deleted {
			if st.OAuth != nil {
				phase := retirementPhase(cfg, st.OAuth, at)
				lifecycleEvent(&view, "credentials_discarded", at)
				view.OAuthLifecycle.Revocation = phase
			}
			st.OAuth = nil
			st.Renewal = nil
		} else if st.Renewal != nil && st.Renewal.Result == nil && st.Renewal.State != "installed" && st.Renewal.State != "failed" {
			st.Renewal = nil
			if st.OAuth != nil {
				oauth := *st.OAuth
				oauth.State = "reauthorization_required"
				st.OAuth = &oauth
			}
		}
		lifecycleEvent(&view, "credentials_discarded", at)
		view.OAuthLifecycle.CredentialState = "credential_changed"
		if refresh || deleted {
			view.OAuthLifecycle.LocalState = "disconnected"
		}
		if deleted {
			view.OAuthLifecycle.CredentialState = "reconnect_required"
		}
		st.LeaseID = ""
		st.LeaseUntil = nil
		st.OAuthFlow = nil
		st.State = "scheduled"
		st.NextCheckAt = &at
		view.ObserverStatus = &st
		view.Config.Revision = uuid.NewString()
		setObserverView(&root, role, view)
	}
	if err := m.syncRetirementsLocked(account, app, before, &root, at); err != nil {
		return err
	}
	m.mcpCatalogMonitors[app] = cloneMCPCatalogMonitor(root)
	success = true
	return nil
}
