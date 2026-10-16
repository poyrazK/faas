package state

import (
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// ApplyEnvironmentCachePolicy replaces only cache rules. Revision-scoped IDs
// partition response-cache entries after a policy reload, even when a route's
// path and priority stay the same. Missing policies keep legacy inheritance.
func ApplyEnvironmentCachePolicy(rules []EdgeRule, policy api.EnvironmentCachePolicy, host string) ([]EdgeRule, error) {
	if policy.Inherited {
		if len(policy.Rules) != 0 || policy.Revision < 0 || policy.Revision > 0 && (policy.UpdatedAt == nil || policy.AppID == "" || policy.EnvironmentID == "") {
			return nil, ErrInvalidArgument
		}
		if policy.Revision == 0 {
			return rules, nil
		}
		out := append([]EdgeRule(nil), rules...)
		for i := range out {
			if out[i].Kind == EdgeRuleKindCache {
				out[i].ID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("cache-inheritance/"+policy.EnvironmentID+"/"+policy.AppID+"/"+strconv.FormatInt(policy.Revision, 10)+"/"+out[i].ID)).String()
			}
		}
		return out, nil
	}
	if policy.Revision < 1 || policy.EnvironmentID == "" || policy.AppID == "" || policy.UpdatedAt == nil {
		return nil, ErrInvalidArgument
	}
	normalized, err := api.NormalizeEnvironmentCacheRules(policy.Rules)
	if err != nil {
		return nil, ErrInvalidArgument
	}
	out := make([]EdgeRule, 0, len(rules)+len(normalized))
	for _, r := range rules {
		if r.Kind != EdgeRuleKindCache {
			out = append(out, r)
		}
	}
	for i, r := range normalized {
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("cache-policy/"+policy.EnvironmentID+"/"+policy.AppID+"/"+strconv.FormatInt(policy.Revision, 10)+"/"+strconv.Itoa(i)))
		out = append(out, EdgeRule{ID: id.String(), AppID: policy.AppID, MatchHost: host, ManifestKey: "environment_cache_policy", MatchPath: r.Path, Priority: r.Priority, Enabled: true, Kind: EdgeRuleKindCache, Mode: EdgeRuleModeEnforce, Action: EdgeRuleAction{Kind: EdgeRuleKindCache, Cache: &EdgeRuleCacheAction{MaxAgeSeconds: r.Cache.MaxAgeSeconds, StaleWhileRevalidateSeconds: r.Cache.StaleWhileRevalidateSeconds, StaleIfErrorSeconds: r.Cache.StaleIfErrorSeconds, VaryOn: r.Cache.VaryOn, Methods: r.Cache.Methods}}, CreatedAt: *policy.UpdatedAt, UpdatedAt: *policy.UpdatedAt})
	}
	return out, nil
}
