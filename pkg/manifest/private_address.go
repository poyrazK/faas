package manifest

import (
	"fmt"
	"net/netip"
)

// PrivateAddress is the host's private IPv4 (ADR-20261110120000):
// PrivateIPv4 when set, otherwise Address's host when that is an IP literal,
// otherwise "". The firewall then falls back to the overlay subnet and
// listeners to every address, as before.
func (h Host) PrivateAddress() string {
	if h.PrivateIPv4 != "" {
		return h.PrivateIPv4
	}
	host, _, err := ParseHostPort(h.Address)
	if err != nil {
		return ""
	}
	if addr, err := netip.ParseAddr(host); err == nil && addr.Is4() {
		return host
	}
	return ""
}

// validatePrivateAddresses checks every host's private_ipv4: an IPv4 inside
// the overlay subnet, matching Address when that is an IP, unique, and
// present on every host once overlay.interface marks a bare-metal private
// card.
func validatePrivateAddresses(overlay Overlay, hosts []Host) Errors {
	var errs Errors
	var subnet netip.Prefix
	if overlay.CIDR != "" {
		subnet, _ = netip.ParsePrefix(overlay.CIDR)
	}
	seen := map[string]string{}
	for i, h := range hosts {
		field := fmt.Sprintf("fleet.hosts[%d].private_ipv4", i)
		if h.PrivateIPv4 == "" {
			if overlay.Interface != "" {
				errs = append(errs, Error{field, "is required when overlay.interface names the private card: the firewall admits each host's exact address"})
			}
			continue
		}
		addr, err := netip.ParseAddr(h.PrivateIPv4)
		if err != nil || !addr.Is4() {
			errs = append(errs, Error{field, fmt.Sprintf("%q is not an IPv4 address", h.PrivateIPv4)})
			continue
		}
		if subnet.IsValid() && !subnet.Contains(addr) {
			errs = append(errs, Error{field, fmt.Sprintf("%s is outside overlay.cidr %s", addr, subnet)})
		}
		if host, _, err := ParseHostPort(h.Address); err == nil {
			if a, err := netip.ParseAddr(host); err == nil && a != addr {
				errs = append(errs, Error{field, fmt.Sprintf("%s differs from the host's address %s", addr, a)})
			}
		}
		if other, dup := seen[h.PrivateIPv4]; dup {
			errs = append(errs, Error{field, fmt.Sprintf("%s is also %s's", addr, other)})
		}
		seen[h.PrivateIPv4] = h.Name
	}
	return errs
}
