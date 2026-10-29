package api

import (
	"fmt"
	"regexp"
	"strings"
)

var realtimeChannelRuleClaim = regexp.MustCompile(`\{([a-z_][a-z0-9_]{0,63})\}`)

// RealtimeChannelRuleClaims returns the claim names a rule's pattern reads.
func RealtimeChannelRuleClaims(pattern string) []string {
	var out []string
	for _, m := range realtimeChannelRuleClaim.FindAllStringSubmatch(pattern, -1) {
		out = append(out, m[1])
	}
	return out
}

// ValidateRealtimeChannelRules checks an endpoint's channel rules.
func ValidateRealtimeChannelRules(rules []RealtimeChannelRule) error {
	if len(rules) > RealtimeChannelRulesMax {
		return fmt.Errorf("channel_rules: at most %d rules", RealtimeChannelRulesMax)
	}
	for i, rule := range rules {
		p := rule.Pattern
		if p == "" || len(p) > RealtimeChannelRulePatternMax {
			return fmt.Errorf("channel_rules[%d]: pattern must be 1-%d bytes", i, RealtimeChannelRulePatternMax)
		}
		if !rule.Read {
			return fmt.Errorf("channel_rules[%d]: a rule must grant read", i)
		}
		if strings.Contains(strings.TrimSuffix(p, "*"), "*") {
			return fmt.Errorf("channel_rules[%d]: * may only end the pattern", i)
		}
		rest := realtimeChannelRuleClaim.ReplaceAllString(p, "")
		if strings.ContainsAny(rest, "{}") {
			return fmt.Errorf("channel_rules[%d]: placeholders are {claim} with a lowercase claim name", i)
		}
		if p == "*" {
			return fmt.Errorf("channel_rules[%d]: a rule must name part of the channel; use auth none to allow every channel", i)
		}
	}
	return nil
}

// MatchRealtimeChannelRule reports whether rule grants channel to a token
// with claims. A placeholder whose claim is missing or empty never matches.
func MatchRealtimeChannelRule(rule RealtimeChannelRule, channel string, claims map[string]string) bool {
	missing := false
	expanded := realtimeChannelRuleClaim.ReplaceAllStringFunc(rule.Pattern, func(placeholder string) string {
		value := claims[placeholder[1:len(placeholder)-1]]
		if value == "" {
			missing = true
		}
		return value
	})
	if missing {
		return false
	}
	if prefix, wildcard := strings.CutSuffix(expanded, "*"); wildcard && strings.HasSuffix(rule.Pattern, "*") {
		return strings.HasPrefix(channel, prefix)
	}
	return channel == expanded
}
