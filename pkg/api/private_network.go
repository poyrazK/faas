package api

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

const (
	PrivateNetworkAttachmentStatusPending = "pending"
	PrivateNetworkAttachmentStatusReady   = "ready"
	PrivateNetworkAttachmentStatusError   = "error"
	PrivateNetworkAttachmentMaxCIDRs      = 64
	PrivateNetworkPolicyMaxCIDRs          = 64
	PrivateNetworkStatusReady             = "ready"
	PrivateNetworkStatusError             = "error"
	PrivateNetworkMinPrefixBits           = 16
	PrivateNetworkMaxPrefixBits           = 28
)

var privateNetworkIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// PrivateNetwork is the customer-visible Gregale-owned network definition.
// Status describes the control-plane definition; an app attachment still
// reports its own pending/ready state while host networking converges.
type PrivateNetwork struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Region       string     `json:"region"`
	CIDR         string     `json:"cidr"`
	Status       string     `json:"status"`
	StatusDetail string     `json:"status_detail,omitempty"`
	CreatedAt    *time.Time `json:"created_at,omitempty"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
}

// PrivateNetworkListResponse wraps the account-scoped network collection.
type PrivateNetworkListResponse struct {
	Networks []PrivateNetwork `json:"networks"`
}

// CreatePrivateNetworkRequest creates a Gregale-owned IPv4 network. The
// region is a Gregale placement label, not a DigitalOcean region identifier.
type CreatePrivateNetworkRequest struct {
	Name   string `json:"name"`
	Region string `json:"region"`
	CIDR   string `json:"cidr"`
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

func prefixesOverlap(a, b netip.Prefix) bool {
	return a.Contains(b.Addr()) || b.Contains(a.Addr())
}
