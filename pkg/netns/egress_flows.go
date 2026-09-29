package netns

import (
	"encoding/json"
	"fmt"
	"net/netip"
)

// EgressFlow is one destination address and TCP port a guest opened a new
// flow to within EgressFlowTimeout (ADR-369).
type EgressFlow struct {
	Addr netip.Addr
	Port uint16
}

// parseEgressFlowSet decodes `nft -j list set <family> faas egress_flows`.
// Elements of a set with timeouts are wrapped as {"elem": {"val": ...}};
// the value of a concatenated key is {"concat": [addr, port]}.
func parseEgressFlowSet(out []byte) ([]EgressFlow, error) {
	var doc struct {
		Nftables []struct {
			Set *struct {
				Elem []json.RawMessage `json:"elem"`
			} `json:"set"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("parse nft -j egress_flows: %w", err)
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
			if json.Unmarshal(val, &concat) != nil || len(concat.Concat) != 2 {
				continue
			}
			var addrStr string
			var port int
			if json.Unmarshal(concat.Concat[0], &addrStr) != nil || json.Unmarshal(concat.Concat[1], &port) != nil {
				continue
			}
			addr, err := netip.ParseAddr(addrStr)
			if err != nil || port < 0 || port > 65535 {
				continue
			}
			flows = append(flows, EgressFlow{Addr: addr, Port: uint16(port)})
		}
	}
	return flows, nil
}
