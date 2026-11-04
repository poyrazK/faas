package edgerules

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/state"
)

// listScope is the set of edge-rule lists a request manages (ADR-091
// amendment 2026-11-03, review round 9). A request that names a company app
// (?app=<slug> on /v1/edge-rule-lists, or a dashboard app page) manages that
// organization's lists, authorized by the member's organization role on the
// app; any other request manages the caller's personal lists.
//
// Lists keep their account namespace: a company app's rules resolve lists in
// the namespace of the account that owns the app's resources, and so does
// the gateway. The organization marker decides who manages a list, not where
// a rule finds it.
type listScope struct {
	orgID string // "" = personal
}

func listScopeFor(r *http.Request) listScope {
	if grant, ok := appaccess.From(r); ok && grant.App.OrgID != "" {
		return listScope{orgID: grant.App.OrgID}
	}
	return listScope{}
}

// listVisibility answers whether lists belong to a scope, caching the apps
// it reads for one request.
type listVisibility struct {
	s     *Service
	scope listScope
	refs  map[string][]state.EdgeRuleListRef
	apps  map[string]string // app ID → org ID
}

// visible reports whether l belongs to the scope. A personal request sees
// personal lists. A company app's request sees its organization's lists and
// the personal lists that only its organization's apps use: lists created
// before 2026-11-03 that company rules reference, which would otherwise stay
// with whichever member created them.
func (v *listVisibility) visible(ctx context.Context, l state.EdgeRuleList) (bool, error) {
	if v.scope.orgID == "" {
		return l.OrgID == "", nil
	}
	if l.OrgID != "" {
		return l.OrgID == v.scope.orgID, nil
	}
	refs := v.refs[l.Name]
	if len(refs) == 0 {
		return false, nil
	}
	for _, ref := range refs {
		orgID, ok := v.apps[ref.AppID]
		if !ok {
			app, err := v.s.Host.Store().AppByID(ctx, ref.AppID)
			switch {
			case errors.Is(err, state.ErrNotFound):
				orgID = ""
			case err != nil:
				return false, err
			default:
				orgID = app.OrgID
			}
			if v.apps == nil {
				v.apps = map[string]string{}
			}
			v.apps[ref.AppID] = orgID
		}
		if orgID != v.scope.orgID {
			return false, nil
		}
	}
	return true, nil
}

// scopedEdgeRuleList loads the named list when it belongs to the request's
// scope; a list outside it is reported missing, as another account's is.
func (s *Service) scopedEdgeRuleList(ctx context.Context, store state.EdgeRuleListStore, scope listScope, accountID, name string) (state.EdgeRuleList, []state.EdgeRuleListRef, *api.Problem) {
	lists, err := store.EdgeRuleListsByName(ctx, accountID, []string{name})
	if err != nil {
		return state.EdgeRuleList{}, nil, api.ErrCapacity("could not read edge rule lists")
	}
	if len(lists) == 0 {
		return state.EdgeRuleList{}, nil, api.ErrEdgeRuleListNotFound(name)
	}
	refs, err := store.EdgeRuleListReferences(ctx, accountID)
	if err != nil {
		return state.EdgeRuleList{}, nil, api.ErrCapacity("could not read edge rule list references")
	}
	v := listVisibility{s: s, scope: scope, refs: refs}
	ok, err := v.visible(ctx, lists[0])
	if err != nil {
		return state.EdgeRuleList{}, nil, api.ErrCapacity("could not read the apps that use edge rule lists")
	}
	if !ok {
		return state.EdgeRuleList{}, nil, api.ErrEdgeRuleListNotFound(name)
	}
	return lists[0], refs[name], nil
}
