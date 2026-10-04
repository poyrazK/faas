// adr: 531
package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
)

func (h *Handler) pinResolvedOwnerPolicies(w http.ResponseWriter, r *http.Request, app App) bool {
	loader, required := h.edgeRules.(EdgeOwnerPolicySnapshotter)
	if !required || !loader.RequiresOwnerPolicySnapshot() {
		return false
	}
	prior, _ := r.Context().Value(pinnedEdgePoliciesKey{}).(map[string]*edgePolicySnapshot)
	if app.PublicPolicySource == nil || app.PublicPolicySource.Revision == "" || len(prior) == 0 || len(app.PublicCompiledPolicies) != len(prior) {
		h.writeTrafficPolicyUnavailable(w, r)
		return true
	}
	ctx := r.Context()
	for host := range prior {
		entry := app.PublicCompiledPolicies[host]
		if entry == nil || entry.Host != host || entry.PublicSourceRevision == "" {
			h.writeTrafficPolicyUnavailable(w, r)
			return true
		}
		var err error
		ctx, err = WithPinnedHostPolicy(ctx, host, entry)
		if err != nil {
			h.writeTrafficPolicyUnavailable(w, r)
			return true
		}
	}
	*r = *r.WithContext(ctx)
	return false
}

func PublicRouteGraphRevision(entry *HostEntry, owner string) (string, error) {
	if entry == nil {
		return "", errors.New("public route graph is unavailable")
	}
	routes := make([]EdgeRuleResolved, 0, len(entry.Route))
	for _, rule := range entry.Route {
		if owner == "" || rule.AccountID == owner {
			routes = append(routes, rule)
		}
	}
	encoded, err := json.Marshal(struct {
		Host   string
		Routes []EdgeRuleResolved
	}{entry.Host, routes})
	if err != nil {
		return "", err
	}
	return policyDigest(encoded), nil
}
