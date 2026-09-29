package netns

import (
	"encoding/json"
	"fmt"
	"strings"
)

// trackedCounterName reports whether a named nft counter feeds the egress
// pollers: the per-CIDR drop_v4_/drop_v6_ counters, the deny_* aggregates,
// and the ADR-361 faas_egress_* policy and fan-out counters.
func trackedCounterName(name string) bool {
	for _, prefix := range []string{"drop_v4_", "drop_v6_", "deny_", "faas_egress_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// parseNftCounters turns `nft -j list counters` output into
// map[name]packets. The ip and ip6 faas tables declare some counters under
// the same name (deny_allowlist, the ADR-361 counters); their packets are
// summed, so a per-instance total covers both families instead of whichever
// table nft listed last.
func parseNftCounters(out []byte) (map[string]uint64, error) {
	// nft -j emits a top-level {"nftables":[...]} envelope; each
	// element is either a metainfo block, a counter block, or a
	// chain / table block. We skip everything except tracked counters.
	var doc struct {
		Nftables []json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("parse nft -j counters: %w", err)
	}
	m := make(map[string]uint64)
	for _, raw := range doc.Nftables {
		var c struct {
			Counter struct {
				Name    string `json:"name"`
				Packets uint64 `json:"packets"`
			} `json:"counter"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			// Skip non-counter blocks (metainfo, table, chain, etc.).
			// A malformed counter block is also skipped — the rest
			// of the parse still surfaces valid entries.
			continue
		}
		if c.Counter.Name == "" || !trackedCounterName(c.Counter.Name) {
			continue
		}
		m[c.Counter.Name] += c.Counter.Packets
	}
	return m, nil
}
