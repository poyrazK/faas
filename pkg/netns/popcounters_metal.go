//go:build metal

package netns

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// PopCounters reads the nftables named counters per the PR-E
// observability contract and returns a map[name]packets for every
// drop-counter matching the catalog's CounterName prefix scope.
//
// The metal-side implementation executes `nft -j list counters`
// and parses the JSON output. The JSON shape is:
//
//	{"nftables":[{"metainfo":{...}}, {"counter":{...}}, ...]}
//
// where each `counter` block has at least the fields:
//
//	{"name": "drop_v4_10_0_0_0_8", "packets": 17, "bytes": 1234}
//
// We filter to names starting with "drop_v4_" or "drop_v6_" (the
// CounterName family prefix emitted by DropCounterName) so the
// result is bounded by the catalog size — nftables may have other
// named counters (faas_cap, etc.) that are not relevant to the
// egress-deny panel.
//
// nft rejects -j on very old releases (pre-0.9.5); the metal
// test pipeline has nft ≥ 1.0 (the Lima guest runs nft 1.0.x). A
// version skew on the EX44 is out of scope for this PR — the same
// precedent as connlimit_metal_test.go::nftVersionOK.
//
// The ctx is threaded so a context-cancelled poll can abort the
// exec rather than block the poll loop. The PR-E poll adapter
// invokes this every 15s; a slow nft (e.g. nftd recompiling) would
// otherwise pile up goroutines.
func PopCounters(ctx context.Context) (map[string]uint64, error) {
	return popCountersCommand(ctx, "nft", "-j", "list", "counters")
}

// PopCountersInNetns reads the named counters from one live instance's
// namespace. Counter names are local to each nft table, so this is the only
// way to retain the app/instance association needed by C1.
func PopCountersInNetns(ctx context.Context, netnsName string) (map[string]uint64, error) {
	if netnsName == "" {
		return nil, fmt.Errorf("nft list counters: empty netns")
	}
	return popCountersCommand(ctx, "ip", "netns", "exec", netnsName, "nft", "-j", "list", "counters")
}

func popCountersCommand(ctx context.Context, argv ...string) (map[string]uint64, error) {
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err != nil {
		return nil, fmt.Errorf("nft list counters: %w", err)
	}
	// nft -j emits a top-level {"nftables":[...]} envelope; each
	// element is either a metainfo block, a counter block, or a
	// chain / table block. We skip everything except the counter
	// blocks that match the egress-deny prefix.
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
		if c.Counter.Name == "" {
			continue
		}
		if !strings.HasPrefix(c.Counter.Name, "drop_v4_") &&
			!strings.HasPrefix(c.Counter.Name, "drop_v6_") &&
			!strings.HasPrefix(c.Counter.Name, "deny_") {
			continue
		}
		m[c.Counter.Name] = c.Counter.Packets
	}
	return m, nil
}
