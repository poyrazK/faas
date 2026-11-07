package apidserver

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agentconnections"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// agentConnectionDeps wires the connected-agents handlers
// (ADR-20261030133644) to apid's key revocation and audit.
func (s *server) agentConnectionDeps() agentconnections.Deps {
	return agentconnections.Deps{
		Store:    s.store,
		Log:      s.log,
		Prefixes: s.listedKeyPrefixes,
		Revoke: func(r *http.Request, acct state.Account, keyID string) error {
			_, err := s.revokeAPIKeyWithReason(r, acct, keyID, "agent_disconnected")
			return err
		},
		Audit: func(ctx context.Context, kind, accountID string, data map[string]any) {
			s.audit.Emit(ctx, kind, &accountID, data)
		},
		ReviewURL: s.agentApprovalReviewURL,
	}
}

// orgAgentDeps wires an organization's agent handlers (ADR-20261101225122)
// to its key revocation and activity.
func (s *server) orgAgentDeps() agentconnections.OrgDeps {
	return agentconnections.OrgDeps{
		Deps:       s.agentConnectionDeps(),
		AuthzAudit: s.authzAuditForCurrentRequest,
		RevokeOrg:  s.revokeOrgAgentKey,
		RecordPolicy: func(r *http.Request, acct state.Account, policy state.OrgAgentPolicy, revoked int) {
			data := map[string]any{"agents_allowed": policy.AgentsAllowed, "allowed_scopes": policy.AllowedScopes, "approval_categories": policy.ApprovalCategories, "revoked": revoked}
			s.audit.Emit(r.Context(), "org.agent_policy.updated", &acct.ID, data)
			id, _ := uuid.Parse(policy.OrgID)
			actorType, actorLabel, actorAccountID := activityActor(r, acct)
			s.recordOrgActivity(r.Context(), state.OrgActivity{
				OrgID: id, Kind: "org.agent_policy.updated", ActorType: actorType, ActorAccountID: actorAccountID, ActorLabel: actorLabel,
				ResourceType: "agent_policy", ResourceID: policy.OrgID, ResourceLabel: "Agent policy",
				SourceType: "org.agent_policy.updated", SourceID: "org.agent_policy.updated:" + uuid.NewString(), Data: activityData(data),
			})
		},
	}
}

// orgAgentKeyProblem refuses a shared organization's agent key that the
// organization's agent policy does not allow (ADR-20261101225122).
func (s *server) orgAgentKeyProblem(ctx context.Context, orgID, accountID string, scopes []string) *api.Problem {
	if !slices.Contains(scopes, api.ScopeAgent) {
		return nil
	}
	if org, err := s.store.OrgByID(ctx, orgID); err == nil && org.Personal {
		return nil
	}
	policy, err := agentconnections.PolicyFor(ctx, s.store, orgID, accountID)
	switch {
	case errors.Is(err, agentconnections.ErrAgentsNotAllowed), errors.Is(err, agentconnections.ErrNotAgentMember):
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Agents not allowed", err.Error())
	case err != nil:
		return api.ErrCapacity("could not check the organization's agent policy")
	case agentconnections.ExceedsPolicy(policy, scopes):
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Scope not allowed for agents", "the organization's agent policy allows agents only these scopes: "+strings.Join(policy.AllowedScopes, ", "))
	}
	return nil
}

// revokeOrgAgentKey revokes an agent's organization key as revokeOrgAPIKey
// does, naming why.
func (s *server) revokeOrgAgentKey(r *http.Request, acct state.Account, orgID, keyID, reason string) error {
	activity := newOrgAPIKeyActivity(r, acct, orgID, "api_key.revoked", map[string]any{"reason": reason})
	updated, err := s.revokeOrgAPIKeyWithActivity(r.Context(), orgID, keyID, activity)
	if err != nil {
		return err
	}
	_ = s.notif.Notify(r.Context(), db.NotifyKeyChanged, `{"kind":"revoked","org":"`+orgID+`"}`)
	s.audit.Emit(r.Context(), "api_key.revoked", &acct.ID, map[string]any{
		"key_id": updated.ID, "scopes": updated.Scopes, "reason": reason, "org_id": updated.OrgID,
	})
	return nil
}
