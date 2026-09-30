// adr: 233, 375
package state

import (
	"github.com/google/uuid"
	"strconv"
)

// ProjectEnvironmentPolicyRules applies the app filter and the optional
// headers/CORS replacement used by named-environment runtime compilation.
// A present empty policy suppresses headers/CORS; nil keeps their fallback.
func ProjectEnvironmentPolicyRules(host string, environment ProjectEnvironment, app App, global []EdgeRule, policy *ProjectEnvironmentEdgePolicy) []EdgeRule {
	out := make([]EdgeRule, 0, len(global))
	for _, rule := range global {
		if rule.AppID == app.ID && (policy == nil || rule.Kind != EdgeRuleKindHeaders && rule.Kind != EdgeRuleKindCORSA) {
			out = append(out, rule)
		}
	}
	if policy == nil {
		return out
	}
	for index, rule := range policy.Rules {
		if !rule.Enabled {
			continue
		}
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(environment.ID+"/"+app.ID+"/"+strconv.Itoa(index)))
		out = append(out, EdgeRule{ID: id.String(), AccountID: app.AccountID, AppID: app.ID, MatchHost: host,
			MatchPath: rule.MatchPath, MatchMethods: rule.MatchMethods, MatchHeaders: rule.MatchHeaders,
			Priority: rule.Priority, Enabled: true, Kind: rule.Kind, Action: rule.Action,
			CreatedAt: policy.CreatedAt, UpdatedAt: policy.UpdatedAt})
	}
	return out
}
