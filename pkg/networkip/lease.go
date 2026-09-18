// Package networkip contains provider-neutral public address lease rules.
//
// The lease is deliberately separate from any cloud API or host dataplane.
// It is the control-plane contract needed to reserve an address, assign it
// to one workload, and later move that assignment during a failover.
package networkip

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// Status is the durable lifecycle of a reserved public address.
type Status string

const (
	StatusAvailable Status = "available"
	StatusPending   Status = "pending"
	StatusAssigned  Status = "assigned"
	StatusError     Status = "error"
)

// Node is the scheduler-facing candidate used by SelectFailoverNode.
type Node struct {
	ID     string
	Region string
	Active bool
}

// ValidateAddress accepts public unicast IPv4 and IPv6 addresses only. The
// provider must route the address to Gregale; private, loopback, link-local,
// multicast, and unspecified addresses cannot be a reserved public IP.
func ValidateAddress(address netip.Addr) error {
	if !address.IsValid() || (!address.Is4() && !address.Is6()) {
		return fmt.Errorf("address must be an IPv4 or IPv6 address")
	}
	if address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() ||
		address.IsMulticast() || address.IsUnspecified() || address.IsInterfaceLocalMulticast() {
		return fmt.Errorf("address must be a public unicast address")
	}
	return nil
}

// ValidateTransition guards the compare-and-swap state machine used by both
// the in-memory and Postgres stores. A provider connector may retry a
// pending/assigned transition, but it cannot silently move an address from a
// different owner's assignment.
func ValidateTransition(from, to Status) error {
	if from == to {
		return nil
	}
	switch from {
	case StatusAvailable:
		if to == StatusPending || to == StatusError {
			return nil
		}
	case StatusPending:
		if to == StatusAssigned || to == StatusAvailable || to == StatusError {
			return nil
		}
	case StatusAssigned:
		if to == StatusPending || to == StatusAvailable || to == StatusError {
			return nil
		}
	case StatusError:
		if to == StatusAvailable || to == StatusPending {
			return nil
		}
	}
	return fmt.Errorf("invalid reserved IP transition %q -> %q", from, to)
}

// SelectFailoverNode deterministically selects the first active candidate in
// the address region. The current node is skipped so a failover cannot
// accidentally return the failed owner. Empty/duplicate IDs are ignored.
func SelectFailoverNode(current, region string, candidates []Node) (string, bool) {
	region = strings.TrimSpace(region)
	seen := make(map[string]struct{}, len(candidates))
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		id := strings.TrimSpace(candidate.ID)
		if id == "" || id == current || !candidate.Active {
			continue
		}
		if region != "" && candidate.Region != region {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return "", false
	}
	return ids[0], true
}
