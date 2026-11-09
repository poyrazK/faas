package apidserver

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/accountexport"
	"github.com/onebox-faas/faas/pkg/apid/edgerules"
	"github.com/onebox-faas/faas/pkg/state"
)

// listEdgeRulesForAccountExport exports the account's edge rules on the apps
// the export covers, in the API's rule shape (ADR-091 amendment 2026-10-30).
// The account export used to leave out the edge configuration entirely.
func (s *server) listEdgeRulesForAccountExport(ctx context.Context, acct state.Account) ([]api.EdgeRuleResponse, error) {
	rows, err := s.store.ListEdgeRulesForAccount(ctx, acct.ID)
	if err != nil {
		return nil, err
	}
	scope := accountexport.For(ctx, s.store, acct.ID)
	out := make([]api.EdgeRuleResponse, 0, len(rows))
	for _, row := range rows {
		allowed, err := scope.App(ctx, row.AppID)
		if err != nil {
			return nil, err
		}
		if allowed {
			out = append(out, s.edgeRules.RuleResponse(acct, row))
		}
	}
	return out, nil
}

// listEdgeRuleListsForAccountExport exports the account's edge rule lists
// with their items.
func listEdgeRuleListsForAccountExport(ctx context.Context, st state.Store, accountID string) ([]api.EdgeRuleListResponse, error) {
	store, ok := st.(state.EdgeRuleListStore)
	if !ok {
		return []api.EdgeRuleListResponse{}, nil
	}
	lists, err := store.ListEdgeRuleLists(ctx, accountID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(lists))
	for _, l := range lists {
		// An organization's lists belong to the organization, not to the
		// member whose account holds them; a member who left must not take
		// them along (ADR-091 amendment 2026-11-04, review round 10).
		if l.OrgID == "" {
			names = append(names, l.Name)
		}
	}
	withItems, err := store.EdgeRuleListsByName(ctx, accountID, names)
	if err != nil {
		return nil, fmt.Errorf("read edge rule list items: %w", err)
	}
	out := make([]api.EdgeRuleListResponse, 0, len(withItems))
	for _, l := range withItems {
		out = append(out, edgerules.EdgeRuleListResponse(l, nil, true))
	}
	return out, nil
}

// listCorsPresetsForAccountExport exports the account's CORS presets: the
// account-wide ones and those scoped to an app the export covers (ADR-091
// amendment 2026-11-04). The export carried the rules that name presets but
// not the presets themselves.
func (s *server) listCorsPresetsForAccountExport(ctx context.Context, acct state.Account) ([]api.CorsPresetResponse, error) {
	rows, err := s.store.ListCorsPresetsForAccount(ctx, acct.ID)
	if err != nil {
		return nil, err
	}
	scope := accountexport.For(ctx, s.store, acct.ID)
	kept := make([]state.CorsPreset, 0, len(rows))
	for _, row := range rows {
		if row.AppID != "" {
			allowed, err := scope.App(ctx, row.AppID)
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
		}
		kept = append(kept, row)
	}
	return corsPresetResponsesFromRows(kept), nil
}
