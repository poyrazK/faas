package middleware

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/agentapproval"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// AgentConnectGrantReader is the optional store seam behind the Connect
// grant (ADR-20261127160000): the person who connected a hosted MCP agent
// approved its deploys, promotions, rollbacks, restarts and undo at
// sign-in. A store without it grants nothing.
type AgentConnectGrantReader interface {
	APIKeyConnectGrant(ctx context.Context, keyID string) (state.AgentConnectGrant, bool, error)
}

// connectGrantApproves reports whether key's Connect grant approves a
// request under rule that would otherwise ask, and records each request it
// lets through in the audit log of the person who granted it.
func (m *Middleware) connectGrantApproves(ctx context.Context, key state.APIKey, rule agentapproval.Rule, settings approvalSettings, binding state.AgentApprovalBinding) (bool, error) {
	grant, ok, err := m.connectGrant(ctx, key, rule, settings)
	if err != nil || !ok {
		return false, err
	}
	if m.Audit != nil {
		m.Audit.Emit(ctx, "agent_approval.connect_grant", &grant.GrantedBy, map[string]any{
			"key_id":     key.ID,
			"account_id": key.AccountID,
			"org_id":     key.OrgID,
			"category":   rule.Category,
			"method":     binding.Method,
			"path":       binding.Path,
			"granted_by": grant.GrantedBy,
			"granted_at": grant.GrantedAt.UTC().Format(time.RFC3339),
		})
	}
	if m.Log != nil {
		m.Log.Info("agent_approval.connect_grant", "key_id", key.ID, "category", rule.Category, "granted_by", grant.GrantedBy)
	}
	return true, nil
}

// connectGrant returns key's grant when it covers a request under rule: an
// mcp-login key whose sign-in carried the grant, a covered route, and no
// category the key chose or its organization's floor keeps asking for.
func (m *Middleware) connectGrant(ctx context.Context, key state.APIKey, rule agentapproval.Rule, settings approvalSettings) (state.AgentConnectGrant, bool, error) {
	if key.Label != api.MCPLoginKeyLabel || !agentapproval.ConnectApproves(rule, settings.chosen, settings.floor) {
		return state.AgentConnectGrant{}, false, nil
	}
	grants, ok := m.Authn.(AgentConnectGrantReader)
	if !ok {
		return state.AgentConnectGrant{}, false, nil
	}
	return grants.APIKeyConnectGrant(ctx, key.ID)
}
