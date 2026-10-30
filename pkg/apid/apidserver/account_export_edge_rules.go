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
		names = append(names, l.Name)
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
