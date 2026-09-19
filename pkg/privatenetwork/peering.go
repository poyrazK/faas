package privatenetwork

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// PeeringSpec describes a customer-authorized link between two Gregale-owned
// networks. Peering is deliberately provider-neutral: the control plane owns
// the relationship and a later applier translates the desired routes into the
// node-local fabric.
type PeeringSpec struct {
	ID        string
	AccountID string
	Region    string
	Left      FabricSpec
	Right     FabricSpec
}

// PeeringRoute is one directional route admitted by a peering relationship.
// The destination is the complete remote network CIDR; callers must still
// apply the network's firewall policy before advertising the route as ready.
type PeeringRoute struct {
	PeeringID          string
	FromNetworkID      string
	DestinationNetwork string
	DestinationCIDR    netip.Prefix
}

// PeeringPlan is the deterministic desired state for one peering. It contains
// both directions so an applier can replace stale routes atomically instead
// of accumulating one-way rules across retries.
type PeeringPlan struct {
	Spec   PeeringSpec
	Routes []PeeringRoute
}

// ValidatePeeringCIDRs rejects overlapping address spaces. A routed peering
// cannot disambiguate overlapping destinations, and accepting it would make
// traffic selection depend on kernel prefix ordering.
func ValidatePeeringCIDRs(left, right netip.Prefix) error {
	if !left.IsValid() || !left.Addr().Is4() {
		return errors.New("privatenetwork: peering left CIDR must be a valid IPv4 prefix")
	}
	if !right.IsValid() || !right.Addr().Is4() {
		return errors.New("privatenetwork: peering right CIDR must be a valid IPv4 prefix")
	}
	if left.Overlaps(right) {
		return fmt.Errorf("privatenetwork: peering CIDRs overlap (%s and %s)", left, right)
	}
	return nil
}

// BuildPeeringPlan validates a peering and returns a canonical two-way route
// plan. It intentionally does not execute iproute2 commands: the same plan is
// consumed by local, overlay, and future provider adapters.
func BuildPeeringPlan(spec PeeringSpec) (PeeringPlan, error) {
	spec.ID = strings.TrimSpace(spec.ID)
	spec.AccountID = strings.TrimSpace(spec.AccountID)
	spec.Region = strings.TrimSpace(spec.Region)
	if err := validatePeeringIdentifier(spec.ID, "peering_id"); err != nil {
		return PeeringPlan{}, err
	}
	if spec.AccountID == "" {
		return PeeringPlan{}, errors.New("privatenetwork: peering account_id is required")
	}
	if err := validatePeeringIdentifier(spec.Region, "region"); err != nil {
		return PeeringPlan{}, err
	}
	if spec.Left.AccountID != spec.AccountID || spec.Right.AccountID != spec.AccountID {
		return PeeringPlan{}, errors.New("privatenetwork: peering networks must belong to the same account")
	}
	if spec.Left.Region != spec.Region || spec.Right.Region != spec.Region {
		return PeeringPlan{}, errors.New("privatenetwork: peering networks must be in the same region")
	}
	if spec.Left.NetworkID == "" || spec.Right.NetworkID == "" {
		return PeeringPlan{}, errors.New("privatenetwork: peering network IDs are required")
	}
	if spec.Left.NetworkID == spec.Right.NetworkID {
		return PeeringPlan{}, errors.New("privatenetwork: a network cannot peer with itself")
	}
	if err := ValidatePeeringCIDRs(spec.Left.CIDR, spec.Right.CIDR); err != nil {
		return PeeringPlan{}, err
	}

	// Canonicalize endpoint ordering so the same pair has the same plan even
	// when callers submit the networks in reverse order.
	left, right := spec.Left, spec.Right
	if right.NetworkID < left.NetworkID {
		left, right = right, left
	}
	spec.Left, spec.Right = left, right
	return PeeringPlan{
		Spec: spec,
		Routes: []PeeringRoute{
			{PeeringID: spec.ID, FromNetworkID: left.NetworkID, DestinationNetwork: right.NetworkID, DestinationCIDR: right.CIDR},
			{PeeringID: spec.ID, FromNetworkID: right.NetworkID, DestinationNetwork: left.NetworkID, DestinationCIDR: left.CIDR},
		},
	}, nil
}

// BuildPeeringRoutes validates and combines a set of peerings into the
// deterministic desired route set for one account and region. Duplicate
// relationships and conflicting destinations are rejected before any host
// mutation is attempted.
func BuildPeeringRoutes(peerings []PeeringSpec) ([]PeeringRoute, error) {
	routes := make([]PeeringRoute, 0, len(peerings)*2)
	seenIDs := make(map[string]struct{}, len(peerings))
	seenRoutes := make(map[string]struct{}, len(peerings)*2)
	destinations := make(map[string][]PeeringRoute)
	for _, spec := range peerings {
		plan, err := BuildPeeringPlan(spec)
		if err != nil {
			return nil, err
		}
		if _, exists := seenIDs[plan.Spec.ID]; exists {
			return nil, fmt.Errorf("privatenetwork: duplicate peering ID %q", plan.Spec.ID)
		}
		seenIDs[plan.Spec.ID] = struct{}{}
		for _, route := range plan.Routes {
			key := route.FromNetworkID + "\x00" + route.DestinationNetwork
			if _, exists := seenRoutes[key]; exists {
				return nil, fmt.Errorf("privatenetwork: duplicate route %s -> %s", route.FromNetworkID, route.DestinationNetwork)
			}
			for _, prior := range destinations[route.FromNetworkID] {
				if prior.DestinationCIDR.Overlaps(route.DestinationCIDR) {
					return nil, fmt.Errorf("privatenetwork: overlapping destinations from %s (%s and %s)", route.FromNetworkID, prior.DestinationNetwork, route.DestinationNetwork)
				}
			}
			seenRoutes[key] = struct{}{}
			destinations[route.FromNetworkID] = append(destinations[route.FromNetworkID], route)
			routes = append(routes, route)
		}
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].FromNetworkID == routes[j].FromNetworkID {
			return routes[i].DestinationNetwork < routes[j].DestinationNetwork
		}
		return routes[i].FromNetworkID < routes[j].FromNetworkID
	})
	return routes, nil
}

func validatePeeringIdentifier(value, field string) error {
	if err := apiIdentifier(value); err != nil {
		return fmt.Errorf("privatenetwork: peering %s %w", field, err)
	}
	return nil
}

// apiIdentifier is kept local to this file to avoid making the planner depend
// on HTTP/API packages. FabricSpec validation already applies the same grammar
// to network and region identifiers.
func apiIdentifier(value string) error {
	if value == "" {
		return errors.New("is required")
	}
	if len(value) > 63 || value[0] < 'a' || value[0] > 'z' {
		return errors.New("must match [a-z][a-z0-9-]{0,62}")
	}
	for _, r := range value[1:] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return errors.New("must match [a-z][a-z0-9-]{0,62}")
		}
	}
	return nil
}
