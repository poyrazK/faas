package appstandards

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
)

func narrower(field Field, candidate, bound json.RawMessage) bool {
	switch field {
	case RequireSigned:
		return !boolValue(bound) || boolValue(candidate)
	case SecurityPolicy:
		return policyRank(policyValue(candidate)) >= policyRank(policyValue(bound))
	case EgressCIDRs:
		values, allowed := stringsValue(candidate), stringsValue(bound)
		// The existing network contract interprets an empty CIDR list as
		// unrestricted, not deny-all. Never call clearing the list narrowing.
		if len(allowed) == 0 {
			return true
		}
		if len(values) == 0 {
			return false
		}
		for _, value := range values {
			if !prefixContained(value, allowed) {
				return false
			}
		}
		return true
	case EgressExtraPorts:
		for _, value := range portsValue(candidate) {
			if !slices.Contains(portsValue(bound), value) {
				return false
			}
		}
		return true
	case TrustedPublishers:
		return stringsContainAll(stringsValue(bound), stringsValue(candidate))
	default:
		return false
	}
}

func intersect(field Field, a, b json.RawMessage) (json.RawMessage, error) {
	switch field {
	case RequireSigned:
		return json.Marshal(boolValue(a) || boolValue(b))
	case SecurityPolicy:
		if policyRank(policyValue(a)) >= policyRank(policyValue(b)) {
			return a, nil
		}
		return b, nil
	case TrustedPublishers:
		values := []string{}
		for _, value := range stringsValue(a) {
			if slices.Contains(stringsValue(b), value) {
				values = append(values, value)
			}
		}
		return json.Marshal(values)
	case EgressExtraPorts:
		values := []int{}
		for _, value := range portsValue(a) {
			if slices.Contains(portsValue(b), value) {
				values = append(values, value)
			}
		}
		return json.Marshal(values)
	case EgressCIDRs:
		return intersectCIDRs(stringsValue(a), stringsValue(b))
	default:
		return nil, fmt.Errorf("%s does not support narrowing", field)
	}
}

func intersectCIDRs(a, b []string) (json.RawMessage, error) {
	if len(a) == 0 {
		return json.Marshal(b)
	}
	if len(b) == 0 {
		return json.Marshal(a)
	}
	values := []string{}
	for _, value := range a {
		if prefixContained(value, b) {
			values = append(values, value)
		}
	}
	for _, value := range b {
		if prefixContained(value, a) {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("inherited outbound CIDRs have no representable overlap")
	}
	slices.Sort(values)
	return json.Marshal(slices.Compact(values))
}

func prefixContained(value string, allowed []string) bool {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return false
	}
	for _, item := range allowed {
		bound, err := netip.ParsePrefix(item)
		if err == nil && bound.Addr().BitLen() == prefix.Addr().BitLen() && bound.Bits() <= prefix.Bits() && bound.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}
