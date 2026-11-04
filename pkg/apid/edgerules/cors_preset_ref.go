package edgerules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// corsPresetRef is the preset a kind=cors action names, or nil.
//
// ADR-091 amendment 2026-11-04 (review round 10): ActionFromBody used to drop
// cors_preset_id, so a rule written through the API or the dashboard was
// stored as an empty CORS rule, and edge_rules.cors_preset_id (the column the
// preset's fencing, authorization and ON DELETE SET NULL work from) was only
// ever set by the manifest path.
func corsPresetRef(kind string, raw json.RawMessage) *string {
	if kind != string(state.EdgeRuleKindCORSA) || len(raw) == 0 {
		return nil
	}
	var a api.EdgeRuleCORSAction
	if json.Unmarshal(raw, &a) != nil || a.CorsPresetID == nil || *a.CorsPresetID == "" {
		return nil
	}
	id := *a.CorsPresetID
	return &id
}

// companyOrgID is the organization a company app belongs to, or "" for an
// app of a personal workspace (or one with no organization).
func (s *Service) companyOrgID(ctx context.Context, app state.App) (string, error) {
	if app.OrgID == "" {
		return "", nil
	}
	org, err := s.Host.Store().OrgByID(ctx, app.OrgID)
	if err != nil {
		return "", err
	}
	if org.Personal {
		return "", nil
	}
	return org.ID, nil
}

// CorsPresetRefProblem refuses a cors rule naming a preset its app cannot
// use: one the account does not have, one scoped to another app, or, for a
// company app, an account-wide preset of the member whose account owns the
// app's resources. A company app's rules use the app's own presets, which its
// organization manages (?app=<slug> on /v1/cors-presets); the creator's
// account-wide presets stay private to the creator, who could otherwise
// change a company app's CORS after leaving. Every refusal reads the same,
// so it reveals nothing about which preset IDs exist.
func (s *Service) CorsPresetRefProblem(ctx context.Context, accountID string, app state.App, presetID *string) *api.Problem {
	if presetID == nil {
		return nil
	}
	refused := api.ErrValidation(fmt.Sprintf("cors_preset_id %q names no CORS preset this app can use; create one for the app with POST /v1/cors-presets", *presetID))
	preset, err := s.Host.Store().GetCorsPresetByID(ctx, accountID, *presetID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		return refused
	case err != nil:
		return api.ErrCapacity("could not read the CORS preset")
	case preset.AppID != "":
		if preset.AppID != app.ID {
			return refused
		}
		return nil
	}
	orgID, err := s.companyOrgID(ctx, app)
	if err != nil {
		return api.ErrCapacity("could not read the app's organization")
	}
	if orgID != "" {
		return refused
	}
	return nil
}

// updateReferencesProblem checks the lists and CORS preset a rule update
// names against the rule's app, as a create does.
func (s *Service) updateReferencesProblem(ctx context.Context, acct state.Account, row state.EdgeRule, req api.UpdateEdgeRuleRequest) *api.Problem {
	var presetID *string
	if req.Action != nil {
		presetID = corsPresetRef(string(row.Kind), *req.Action)
	}
	if api.EdgeRuleMatchListRefs(req.Match) == nil && presetID == nil {
		return nil
	}
	app, err := s.Host.Store().AppByID(ctx, row.AppID)
	if err != nil {
		return api.ErrCapacity("could not read the rule's app")
	}
	if prob := s.validateEdgeRuleMatchLists(ctx, acct.ID, app, req.Match); prob != nil {
		return prob
	}
	return s.CorsPresetRefProblem(ctx, acct.ID, app, presetID)
}
