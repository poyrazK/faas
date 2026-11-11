package apidserver

import (
	"context"
	"net/http"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// keyApprovalCategories validates the approval categories requested for a
// new key (ADR-20261104140058). It reports set=false when none were
// requested: the key then needs approval for every category.
func keyApprovalCategories(scopes []string, requested *[]string) (categories []string, set bool, prob *api.Problem) {
	if requested == nil {
		return nil, false, nil
	}
	if !slices.Contains(scopes, api.ScopeAgent) {
		return nil, false, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Approvals need an agent key",
			"approval_categories apply only to keys with the agent scope")
	}
	categories, err := api.NormalizeAgentApprovalCategories(*requested)
	if err != nil {
		return nil, false, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Unknown approval category", err.Error())
	}
	return categories, true, nil
}

// setKeyApprovalCategories records a new key's categories. When it cannot,
// it revokes the key: a key with other settings than the caller asked for
// must not stay usable.
func (s *server) setKeyApprovalCategories(ctx context.Context, acct state.Account, key state.APIKey, categories []string, set bool) *api.Problem {
	if !set {
		return nil
	}
	store, _ := s.store.(state.AgentApprovalStore)
	if store != nil && store.SetAPIKeyApprovalCategories(ctx, key.ID, categories) == nil {
		return nil
	}
	s.revokeMintedKey(ctx, acct, key)
	return api.ErrCapacity("could not record the key's approval settings; it was revoked, try again")
}

// revokeMintedKey removes a key whose settings could not be recorded.
func (s *server) revokeMintedKey(ctx context.Context, acct state.Account, key state.APIKey) {
	if key.OrgID != "" {
		_, _ = s.store.RevokeOrgAPIKey(ctx, key.OrgID, key.ID)
		return
	}
	_ = s.store.DeleteAPIKey(ctx, acct.ID, key.ID)
}
