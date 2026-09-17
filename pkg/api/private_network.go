package api

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

const (
	PrivateNetworkAttachmentStatusPending = "pending"
	PrivateNetworkAttachmentStatusReady   = "ready"
	PrivateNetworkAttachmentStatusError   = "error"
	PrivateNetworkAttachmentMaxCIDRs      = 64
)

var privateNetworkIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

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

func prefixesOverlap(a, b netip.Prefix) bool {
	return a.Contains(b.Addr()) || b.Contains(a.Addr())
}
