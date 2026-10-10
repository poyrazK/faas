package api

// ADR-963: reusable edge-rule lists. Account-scoped named sets of IPs,
// countries, hosts or strings that match conditions reference with the
// in_list op. Items are validated and canonicalized here so apid, the
// gateway and the trace simulator agree on what a list contains.

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Edge-rule list kinds.
const (
	EdgeRuleListKindIP      = "ip"
	EdgeRuleListKindCountry = "country"
	EdgeRuleListKindHost    = "host"
	EdgeRuleListKindString  = "string"
	// EdgeRuleListKindASN holds autonomous system numbers (ADR-966).
	EdgeRuleListKindASN = "asn"
)

// EdgeRuleListMaxDescriptionBytes bounds a list's free-text description.
const EdgeRuleListMaxDescriptionBytes = 500

var edgeRuleListNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidateEdgeRuleListName checks a list name: 1–64 bytes of lowercase
// letters, digits, '_' and '-', starting with a letter or digit.
func ValidateEdgeRuleListName(name string) error {
	if !edgeRuleListNameRe.MatchString(name) {
		return fmt.Errorf("list name %q must be 1-64 characters of a-z, 0-9, '_' and '-', starting with a letter or digit", name)
	}
	return nil
}

// ValidEdgeRuleListKind reports whether kind is a known list kind.
func ValidEdgeRuleListKind(kind string) bool {
	switch kind {
	case EdgeRuleListKindIP, EdgeRuleListKindCountry, EdgeRuleListKindHost, EdgeRuleListKindString, EdgeRuleListKindASN:
		return true
	}
	return false
}

// NormalizeEdgeRuleListItems validates items for kind and returns them
// canonicalized, deduplicated and sorted: IPs and CIDRs in net package
// form (CIDRs masked), countries uppercased, hosts lowercased.
func NormalizeEdgeRuleListItems(kind string, items []string) ([]string, error) {
	if !ValidEdgeRuleListKind(kind) {
		return nil, fmt.Errorf("unknown list kind %q (ip, country, host, string, asn)", kind)
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for i, item := range items {
		v, err := normalizeEdgeRuleListItem(kind, item)
		if err != nil {
			return nil, fmt.Errorf("items[%d]: %w", i, err)
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out, nil
}

func normalizeEdgeRuleListItem(kind, item string) (string, error) {
	item = strings.TrimSpace(item)
	if item == "" {
		return "", fmt.Errorf("empty item")
	}
	if len(item) > EdgeRuleMatchMaxValueBytes {
		return "", fmt.Errorf("item longer than %d bytes", EdgeRuleMatchMaxValueBytes)
	}
	switch kind {
	case EdgeRuleListKindIP:
		if strings.Contains(item, "/") {
			_, ipnet, err := net.ParseCIDR(item)
			if err != nil {
				return "", fmt.Errorf("invalid CIDR %q", item)
			}
			return ipnet.String(), nil
		}
		ip := net.ParseIP(item)
		if ip == nil {
			return "", fmt.Errorf("invalid IP %q", item)
		}
		return ip.String(), nil
	case EdgeRuleListKindCountry:
		if err := validateGeoCountryCode(item); err != nil {
			return "", err
		}
		return strings.ToUpper(item), nil
	case EdgeRuleListKindHost:
		return normalizeEdgeRuleListHost(item)
	case EdgeRuleListKindASN:
		return canonicalASN(item)
	}
	return item, nil
}

// normalizeEdgeRuleListHost accepts an exact host or a "*.suffix" pattern,
// which matches every host under suffix but not suffix itself.
func normalizeEdgeRuleListHost(item string) (string, error) {
	host := strings.ToLower(item)
	name := strings.TrimPrefix(host, "*.")
	if len(name) > 253 {
		return "", fmt.Errorf("host %q longer than 253 bytes", item)
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("invalid host %q", item)
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return "", fmt.Errorf("invalid host %q (use an exact host or *.suffix)", item)
			}
		}
	}
	return host, nil
}

// EdgeRuleList is a compiled list. It is immutable and safe for concurrent
// lookups.
type EdgeRuleList struct {
	Kind     string
	exact    map[string]struct{} // lowercased for country and host
	nets     []*net.IPNet
	suffixes map[string]struct{} // ".example.com" for "*.example.com"
}

// EdgeRuleLists resolves list names for one account.
type EdgeRuleLists map[string]*EdgeRuleList

// CompileEdgeRuleList validates and compiles items for kind.
func CompileEdgeRuleList(kind string, items []string) (*EdgeRuleList, error) {
	items, err := NormalizeEdgeRuleListItems(kind, items)
	if err != nil {
		return nil, err
	}
	l := &EdgeRuleList{Kind: kind, exact: make(map[string]struct{}, len(items))}
	for _, item := range items {
		switch {
		case kind == EdgeRuleListKindIP && strings.Contains(item, "/"):
			_, ipnet, _ := net.ParseCIDR(item)
			l.nets = append(l.nets, ipnet)
		case kind == EdgeRuleListKindHost && strings.HasPrefix(item, "*."):
			if l.suffixes == nil {
				l.suffixes = map[string]struct{}{}
			}
			l.suffixes[item[1:]] = struct{}{}
		case kind == EdgeRuleListKindCountry:
			l.exact[strings.ToLower(item)] = struct{}{}
		default:
			l.exact[item] = struct{}{}
		}
	}
	return l, nil
}

// edgeRuleListFits reports whether a list kind can be used on a field.
func edgeRuleListFits(kind string, field matchFieldKind) bool {
	switch kind {
	case EdgeRuleListKindIP:
		return field == fieldClientIP
	case EdgeRuleListKindCountry:
		return field == fieldCountry
	case EdgeRuleListKindHost:
		return field == fieldHost
	case EdgeRuleListKindString:
		return field == fieldPath || field == fieldHeader || field == fieldCookie || field == fieldQuery
	case EdgeRuleListKindASN:
		return field == fieldASN
	}
	return false
}

func (l *EdgeRuleList) containsIP(ip net.IP) bool {
	if _, ok := l.exact[ip.String()]; ok {
		return true
	}
	for _, ipnet := range l.nets {
		if ipnet.Contains(ip) {
			return true
		}
	}
	return false
}

// contains looks up an observed value; host and country values arrive
// lowercased.
func (l *EdgeRuleList) contains(v string) bool {
	if _, ok := l.exact[v]; ok {
		return true
	}
	for i := 1; i < len(v) && l.suffixes != nil; i++ {
		if v[i] == '.' {
			if _, ok := l.suffixes[v[i:]]; ok {
				return true
			}
		}
	}
	return false
}

// EdgeRuleMatchListRefs returns the distinct list names expr references,
// sorted.
func EdgeRuleMatchListRefs(expr *EdgeRuleMatchExpr) []string {
	if expr == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var walk func(e *EdgeRuleMatchExpr)
	walk = func(e *EdgeRuleMatchExpr) {
		if e.List != "" {
			seen[e.List] = struct{}{}
		}
		for i := range e.All {
			walk(&e.All[i])
		}
		for i := range e.Any {
			walk(&e.Any[i])
		}
		if e.Not != nil {
			walk(e.Not)
		}
	}
	walk(expr)
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// canonicalASN accepts "13335" or "AS13335" and returns the decimal form.
func canonicalASN(v string) (string, error) {
	digits := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(v), "AS"), "as")
	n, err := strconv.ParseUint(digits, 10, 32)
	if err != nil || n == 0 {
		return "", fmt.Errorf("invalid ASN %q (use 13335 or AS13335)", v)
	}
	return strconv.FormatUint(n, 10), nil
}
