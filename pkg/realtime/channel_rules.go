package realtime

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// Channel rules (ADR-20261019102657) grant channels from a verified token's
// claims, so a matching channel needs no authorize callback and does not wake
// the app. A rule trusts the token until it expires; channels that need faster
// revocation keep the callback. Activity scopes always use the callback.

// channelRuleClaims lists the claims the rules read, so verification returns
// them. The subject is always available.
func channelRuleClaims(rules []api.RealtimeChannelRule) []string {
	var names []string
	for _, rule := range rules {
		for _, name := range api.RealtimeChannelRuleClaims(rule.Pattern) {
			if name != "sub" && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names
}

// channelRulesAllow reports whether one of the endpoint's rules grants
// channel to identity.
func channelRulesAllow(endpoint *Endpoint, identity JWTIdentity, channel string) bool {
	if endpoint == nil || len(endpoint.ChannelRules) == 0 || identity.Principal == "" {
		return false
	}
	if _, scope, err := api.ParseRealtimeActivityScope(channel); err != nil || scope != "" {
		return false
	}
	claims := make(map[string]string, len(identity.Claims)+1)
	for name, value := range identity.Claims {
		claims[name] = value
	}
	claims["sub"] = identity.Principal
	for _, rule := range endpoint.ChannelRules {
		if rule.Read && api.MatchRealtimeChannelRule(rule, channel, claims) {
			return true
		}
	}
	return false
}
