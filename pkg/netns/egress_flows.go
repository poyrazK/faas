package netns

import (
	"encoding/json"
	"fmt"
	"net/netip"
)

// EgressFlow is one destination address and port a guest opened a new flow
// to within EgressFlowTimeout (ADR-371). Protocol is empty for egress_flows
// entries, which are TCP only; egress_blocked entries carry "tcp" or "udp".
type EgressFlow struct {
	Addr     netip.Addr
	Protocol string
	Port     uint16
}

// parseEgressBlockedSet decodes `nft -j list set <family> faas egress_blocked`,
// whose key is address . protocol . port.
func parseEgressBlockedSet(out []byte) ([]EgressFlow, error) {
	return parseEgressSet(out, "egress_blocked")
}

// parseEgressFlowSet decodes `nft -j list set <family> faas egress_flows`.
// Elements of a set with timeouts are wrapped as {"elem": {"val": ...}};
// the value of a concatenated key is {"concat": [addr, port]}.
func parseEgressFlowSet(out []byte) ([]EgressFlow, error) {
	return parseEgressSet(out, "egress_flows")
}

func parseEgressSet(out []byte, name string) ([]EgressFlow, error) {
	var doc struct {
		Nftables []struct {
			Set *struct {
				Elem []json.RawMessage `json:"elem"`
			} `json:"set"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("parse nft -j %s: %w", name, err)
	}
	var flows []EgressFlow
	for _, block := range doc.Nftables {
		if block.Set == nil {
			continue
		}
		for _, raw := range block.Set.Elem {
			var wrapped struct {
				Elem *struct {
					Val json.RawMessage `json:"val"`
				} `json:"elem"`
			}
			val := raw
			if json.Unmarshal(raw, &wrapped) == nil && wrapped.Elem != nil {
				val = wrapped.Elem.Val
			}
			var concat struct {
				Concat []json.RawMessage `json:"concat"`
			}
			if json.Unmarshal(val, &concat) != nil || len(concat.Concat) < 2 || len(concat.Concat) > 3 {
				continue
			}
			var addrStr string
			var port int
			if json.Unmarshal(concat.Concat[0], &addrStr) != nil || json.Unmarshal(concat.Concat[len(concat.Concat)-1], &port) != nil {
				continue
			}
			addr, err := netip.ParseAddr(addrStr)
			if err != nil || port < 0 || port > 65535 {
				continue
			}
			flow := EgressFlow{Addr: addr, Port: uint16(port)}
			if len(concat.Concat) == 3 {
				if flow.Protocol = parseL4Proto(concat.Concat[1]); flow.Protocol == "" {
					continue
				}
			}
			flows = append(flows, flow)
		}
	}
	return flows, nil
}

// parseL4Proto maps an nft inet_proto value, printed by name ("tcp") or by
// number (6), to "tcp" or "udp". Anything else is "".
func parseL4Proto(raw json.RawMessage) string {
	var name string
	if json.Unmarshal(raw, &name) == nil {
		switch name {
		case "tcp", "6":
			return "tcp"
		case "udp", "17":
			return "udp"
		}
		return ""
	}
	var num int
	if json.Unmarshal(raw, &num) == nil {
		switch num {
		case 6:
			return "tcp"
		case 17:
			return "udp"
		}
	}
	return ""
}
