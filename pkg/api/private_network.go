package api

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	PrivateNetworkAttachmentStatusPending = "pending"
	PrivateNetworkAttachmentStatusReady   = "ready"
	PrivateNetworkAttachmentStatusError   = "error"
	PrivateNetworkAttachmentMaxCIDRs      = 64
	PrivateNetworkPolicyMaxCIDRs          = 64
	PrivateNetworkFirewallMaxRules        = 64
	PrivateNetworkFirewallMaxCIDRsPerRule = 64
	PrivateNetworkFirewallMaxPortsPerRule = 16
	PrivateNetworkStatusReady             = "ready"
	PrivateNetworkStatusError             = "error"
	PrivateNetworkPeeringStatusPending    = "pending"
	PrivateNetworkPeeringStatusReady      = "ready"
	PrivateNetworkPeeringStatusError      = "error"
	PrivateNetworkMinPrefixBits           = 16
	PrivateNetworkMaxPrefixBits           = 28
)

var privateNetworkIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// PrivateNetwork is the customer-visible Gregale-owned network definition.
// Status describes the control-plane definition; an app attachment still
// reports its own pending/ready state while host networking converges.
type PrivateNetwork struct {
	ID            string                       `json:"id"`
	Name          string                       `json:"name"`
	Region        string                       `json:"region"`
	CIDR          string                       `json:"cidr"`
	AllowedCIDRs  []string                     `json:"allowed_cidrs,omitempty"`
	FirewallRules []PrivateNetworkFirewallRule `json:"firewall_rules,omitempty"`
	Status        string                       `json:"status"`
	StatusDetail  string                       `json:"status_detail,omitempty"`
	CreatedAt     *time.Time                   `json:"created_at,omitempty"`
	UpdatedAt     *time.Time                   `json:"updated_at,omitempty"`
}

// PrivateNetworkListResponse wraps the account-scoped network collection.
type PrivateNetworkListResponse struct {
	Networks []PrivateNetwork `json:"networks"`
}

// PrivateNetworkMember is one stable address reservation in a Gregale-owned
// private network. OwnerID is an opaque platform resource identifier.
type PrivateNetworkMember struct {
	ID        string     `json:"id"`
	OwnerType string     `json:"owner_type"`
	OwnerID   string     `json:"owner_id"`
	Address   string     `json:"address"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

// PrivateNetworkMembersResponse is the account-scoped member inventory and
// address capacity for one Gregale-owned private network.
type PrivateNetworkMembersResponse struct {
	NetworkID string                 `json:"network_id"`
	CIDR      string                 `json:"cidr"`
	Capacity  int                    `json:"capacity"`
	Used      int                    `json:"used"`
	Available int                    `json:"available"`
	Members   []PrivateNetworkMember `json:"members"`
}

// PrivateNetworkPeering is a durable, account-scoped request to connect two
// Gregale-owned networks. Pending and error peerings remain fail-closed until
// the node fabric has converged the symmetric route set.
type PrivateNetworkPeering struct {
	ID            string     `json:"id"`
	NetworkID     string     `json:"network_id"`
	PeerNetworkID string     `json:"peer_network_id"`
	Region        string     `json:"region"`
	Status        string     `json:"status"`
	StatusDetail  string     `json:"status_detail,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
}

// PrivateNetworkPeeringListResponse wraps peerings attached to one network.
type PrivateNetworkPeeringListResponse struct {
	Peerings []PrivateNetworkPeering `json:"peerings"`
}

// CreatePrivateNetworkPeeringRequest requests a symmetric peering between the
// network in the URL and another network in the same account and region.
type CreatePrivateNetworkPeeringRequest struct {
	PeerNetworkID string `json:"peer_network_id"`
}

// CreatePrivateNetworkRequest creates a Gregale-owned IPv4 network. The
// region is a Gregale placement label, not a DigitalOcean region identifier.
type CreatePrivateNetworkRequest struct {
	Name          string                       `json:"name"`
	Region        string                       `json:"region"`
	CIDR          string                       `json:"cidr"`
	AllowedCIDRs  []string                     `json:"allowed_cidrs,omitempty"`
	FirewallRules []PrivateNetworkFirewallRule `json:"firewall_rules,omitempty"`
}

// UpdatePrivateNetworkPolicyRequest replaces the network-level CIDR and
// protocol/port policy. Empty lists disable their respective restrictions and
// preserve legacy allow-all behavior.
type UpdatePrivateNetworkPolicyRequest struct {
	AllowedCIDRs  []string                     `json:"allowed_cidrs"`
	FirewallRules []PrivateNetworkFirewallRule `json:"firewall_rules,omitempty"`
}

// PrivateNetworkFirewallRule is an allow rule for traffic on a private
// network. CIDRs are source ranges for ingress and destination ranges for
// egress; an empty CIDR list means the whole network CIDR. TCP/UDP rules must
// carry one or more single ports or inclusive ranges ("443" or "8000-8080").
type PrivateNetworkFirewallRule struct {
	Direction string   `json:"direction"`
	Protocol  string   `json:"protocol"`
	CIDRs     []string `json:"cidrs,omitempty"`
	Ports     []string `json:"ports,omitempty"`
}

// ValidatePrivateNetworkIdentifier validates the stable operator/provider
// identifier carried by an attachment. It deliberately shares the DNS-safe
// lower-case grammar without making the provider's naming rules part of the
// public API.
func ValidatePrivateNetworkIdentifier(value string) error {
	value = strings.TrimSpace(value)
	if !privateNetworkIdentifierPattern.MatchString(value) {
		return fmt.Errorf("must match [a-z][a-z0-9-]{0,62}")
	}
	return nil
}

// ValidatePrivateNetworkCIDRs parses and canonicalizes the requested private
// destination ranges. Attachments are IPv4-only in this first slice; the
// tenant bridge and guest network ranges are rejected to prevent a policy
// request from shadowing Gregale's own routing contract.
func ValidatePrivateNetworkCIDRs(raw []string, max int) ([]netip.Prefix, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("at least one CIDR is required")
	}
	if max <= 0 || max > PrivateNetworkAttachmentMaxCIDRs {
		max = PrivateNetworkAttachmentMaxCIDRs
	}
	if len(raw) > max {
		return nil, fmt.Errorf("contains %d CIDRs; maximum is %d", len(raw), max)
	}
	reserved := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/30"),
		netip.MustParsePrefix("10.100.0.0/16"),
	}
	seen := make([]netip.Prefix, 0, len(raw))
	for _, value := range raw {
		value = strings.TrimSpace(value)
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("%q is not a valid CIDR: %w", value, err)
		}
		prefix = prefix.Masked()
		if !prefix.Addr().Is4() {
			return nil, fmt.Errorf("%q must be an IPv4 CIDR", value)
		}
		if prefix.Bits() == 0 || !prefix.Addr().IsPrivate() {
			return nil, fmt.Errorf("%q must be a non-default RFC1918 private IPv4 CIDR", value)
		}
		for _, blocked := range reserved {
			if prefixesOverlap(prefix, blocked) {
				return nil, fmt.Errorf("%q overlaps Gregale reserved range %s", value, blocked)
			}
		}
		for _, existing := range seen {
			if prefixesOverlap(prefix, existing) {
				return nil, fmt.Errorf("%q overlaps %s", value, existing)
			}
		}
		seen = append(seen, prefix)
	}
	return seen, nil
}

// ValidatePrivateNetworkCIDR validates the address space Gregale owns for a
// network definition. Network definitions are intentionally narrower than
// attachment destination lists: a /16-/28 IPv4 RFC1918 range leaves room for
// the network gateway and deterministic workload address allocation.
func ValidatePrivateNetworkCIDR(value string) (netip.Prefix, error) {
	value = strings.TrimSpace(value)
	prefixes, err := ValidatePrivateNetworkCIDRs([]string{value}, 1)
	if err != nil {
		return netip.Prefix{}, err
	}
	prefix := prefixes[0]
	if bits := prefix.Bits(); bits < PrivateNetworkMinPrefixBits || bits > PrivateNetworkMaxPrefixBits {
		return netip.Prefix{}, fmt.Errorf("prefix length must be between /%d and /%d", PrivateNetworkMinPrefixBits, PrivateNetworkMaxPrefixBits)
	}
	return prefix, nil
}

// ValidatePrivateNetworkPolicyCIDRs canonicalizes an optional allowlist and
// requires every entry to be contained by an attached private destination.
// An empty input disables the policy and preserves legacy allow-all behavior.
func ValidatePrivateNetworkPolicyCIDRs(raw []string, destinations []netip.Prefix) ([]netip.Prefix, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > PrivateNetworkPolicyMaxCIDRs {
		return nil, fmt.Errorf("contains %d CIDRs; maximum is %d", len(raw), PrivateNetworkPolicyMaxCIDRs)
	}
	if len(destinations) == 0 {
		return nil, fmt.Errorf("requires at least one private network destination")
	}
	seen := make([]netip.Prefix, 0, len(raw))
	for _, value := range raw {
		value = strings.TrimSpace(value)
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("%q is not a valid CIDR: %w", value, err)
		}
		prefix = prefix.Masked()
		if !prefix.Addr().Is4() || prefix.Bits() == 0 {
			return nil, fmt.Errorf("%q must be a non-default IPv4 CIDR", value)
		}
		contained := false
		for _, destination := range destinations {
			if destination.Contains(prefix.Addr()) && destination.Bits() <= prefix.Bits() {
				contained = true
				break
			}
		}
		if !contained {
			return nil, fmt.Errorf("%q is outside the attached private network", value)
		}
		for _, existing := range seen {
			if prefixesOverlap(prefix, existing) {
				return nil, fmt.Errorf("%q overlaps %s", value, existing)
			}
		}
		seen = append(seen, prefix)
	}
	return seen, nil
}

// ValidatePrivateNetworkFirewallRules canonicalizes and validates a network
// firewall rule set. Rules are intentionally provider-neutral and IPv4-only
// in this first slice, matching the private-network attachment contract.
func ValidatePrivateNetworkFirewallRules(raw []PrivateNetworkFirewallRule, network netip.Prefix) ([]PrivateNetworkFirewallRule, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > PrivateNetworkFirewallMaxRules {
		return nil, fmt.Errorf("contains %d rules; maximum is %d", len(raw), PrivateNetworkFirewallMaxRules)
	}
	if !network.IsValid() {
		return nil, fmt.Errorf("requires a valid network CIDR")
	}
	out := make([]PrivateNetworkFirewallRule, 0, len(raw))
	for i, rule := range raw {
		direction := strings.ToLower(strings.TrimSpace(rule.Direction))
		if direction != "ingress" && direction != "egress" {
			return nil, fmt.Errorf("rule %d direction must be ingress or egress", i)
		}
		protocol := strings.ToLower(strings.TrimSpace(rule.Protocol))
		if protocol != "tcp" && protocol != "udp" && protocol != "icmp" {
			return nil, fmt.Errorf("rule %d protocol must be tcp, udp, or icmp", i)
		}
		if protocol == "icmp" && len(rule.Ports) > 0 {
			return nil, fmt.Errorf("rule %d icmp cannot specify ports", i)
		}
		if len(rule.CIDRs) > PrivateNetworkFirewallMaxCIDRsPerRule {
			return nil, fmt.Errorf("rule %d contains %d CIDRs; maximum is %d", i, len(rule.CIDRs), PrivateNetworkFirewallMaxCIDRsPerRule)
		}
		ports, err := normalizeFirewallPorts(rule.Ports, protocol, i)
		if err != nil {
			return nil, err
		}
		cidrs := make([]string, 0, len(rule.CIDRs))
		seen := make([]netip.Prefix, 0, len(rule.CIDRs))
		for _, value := range rule.CIDRs {
			value = strings.TrimSpace(value)
			prefix, parseErr := netip.ParsePrefix(value)
			if parseErr != nil {
				return nil, fmt.Errorf("rule %d CIDR %q is invalid: %w", i, value, parseErr)
			}
			prefix = prefix.Masked()
			if !prefix.Addr().Is4() || prefix.Bits() == 0 {
				return nil, fmt.Errorf("rule %d CIDR %q must be a non-default IPv4 CIDR", i, value)
			}
			if !network.Contains(prefix.Addr()) || network.Bits() > prefix.Bits() {
				return nil, fmt.Errorf("rule %d CIDR %q is outside the private network", i, value)
			}
			for _, existing := range seen {
				if prefixesOverlap(prefix, existing) {
					return nil, fmt.Errorf("rule %d CIDR %q overlaps %s", i, value, existing)
				}
			}
			seen = append(seen, prefix)
			cidrs = append(cidrs, prefix.String())
		}
		out = append(out, PrivateNetworkFirewallRule{Direction: direction, Protocol: protocol, CIDRs: cidrs, Ports: ports})
	}
	return out, nil
}

func normalizeFirewallPorts(raw []string, protocol string, rule int) ([]string, error) {
	if protocol == "icmp" {
		return nil, nil
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("rule %d %s requires at least one port or range", rule, protocol)
	}
	if len(raw) > PrivateNetworkFirewallMaxPortsPerRule {
		return nil, fmt.Errorf("rule %d contains %d ports; maximum is %d", rule, len(raw), PrivateNetworkFirewallMaxPortsPerRule)
	}
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		value = strings.TrimSpace(value)
		parts := strings.Split(value, "-")
		if len(parts) > 2 || value == "" {
			return nil, fmt.Errorf("rule %d port %q must be N or N-M", rule, value)
		}
		start, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || start < 1 || start > 65535 {
			return nil, fmt.Errorf("rule %d port %q is outside 1..65535", rule, value)
		}
		end := start
		if len(parts) == 2 {
			end, err = strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil || end < start || end > 65535 {
				return nil, fmt.Errorf("rule %d port %q is not an increasing range within 1..65535", rule, value)
			}
		}
		canonical := strconv.Itoa(start)
		if end != start {
			canonical += "-" + strconv.Itoa(end)
		}
		if _, exists := seen[canonical]; exists {
			return nil, fmt.Errorf("rule %d repeats port %q", rule, canonical)
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	return out, nil
}

func prefixesOverlap(a, b netip.Prefix) bool {
	return a.Contains(b.Addr()) || b.Contains(a.Addr())
}
