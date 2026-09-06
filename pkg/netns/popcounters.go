//go:build !metal

package netns

import "context"

// PopCounters is the non-metal stub for the nftables named-counter
// poller. It returns an empty map and a nil error so the cmd/vmmd
// scrape adapter is unit-testable on a vanilla dev box (no
// /dev/kvm, no nftables kernel modules). The metal-side
// implementation lives in pkg/netns/popcounters_metal.go and
// shells out to `nft -j list counters`.
//
// The non-metal build is the DEFAULT for any unit test that
// imports pkg/netns without the //go:build metal tag, including
// the cross-renderer invariant test in
// pkg/netns/denylist_external_test.go. Build-tagged wire keeps
// the test path pure-Go (no nft binary, no os/exec) — the same
// precedent as pkg/netns/{policy,connlimit,allowlist}*_metal_test.go.

// PopCounters returns an empty map — there are no nftables
// counters outside the metal test path. The PR-E poll adapter
// treats the empty result as "no drops surfaced yet" and emits
// nothing, which is the right idle behaviour.
func PopCounters(ctx context.Context) (map[string]uint64, error) {
	_ = ctx
	return map[string]uint64{}, nil
}

// PopCountersInNetns is the non-metal counterpart used by the per-instance
// C1 poller. There are no nftables counters outside the metal path.
func PopCountersInNetns(ctx context.Context, netnsName string) (map[string]uint64, error) {
	_ = ctx
	_ = netnsName
	return map[string]uint64{}, nil
}
