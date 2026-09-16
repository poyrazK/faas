package e2etest

// Untagged on purpose, like twonode_sql.go: twonode.go is `//go:build e2e ||
// metal`, so anything defined there is invisible to ordinary CI. The skip
// decision below is the part worth testing, so it lives here where a plain
// `go test ./...` compiles and exercises it.

// TwoNodeSkipReason returns why a two-node drill cannot run, or "" when it can.
//
// Local mode seeds two compute_nodes rows and starts no daemons — TwoNodeHarness
// says so itself: "this harness does not spawn daemon subprocesses". The
// lifecycle flip to 'unavailable' is performed by a running schedd's stale-peer
// observer (pkg/sched.Heartbeat.observeStalePeerNodes), so in local mode the
// drills backdated a heartbeat and then waited out their full 90s budget for a
// transition no process on the box could make, reporting
//
//	twonode: node node-b-<id> did not reach lifecycle=unavailable within 1m30s
//
// as though the recovery arbiter were broken. It is not; there was nobody to
// run it. Three drills did this on every e2e-native run, 93s each.
//
// These belong to scripts/ci/run-native-m9-acceptance.sh, which requires
// FAAS_TWO_NODE_NODE_A/B plus SSH targets and addresses for two real compute
// nodes and drives faas-schedd on both. e2e-native.yml runs on a single node
// and never sets remote mode.
func TwoNodeSkipReason(remote bool) string {
	if remote {
		return ""
	}
	return "requires two real compute nodes: set FAAS_TWO_NODE_REMOTE=1 with " +
		"FAAS_TWO_NODE_NODE_A/B (see scripts/ci/run-native-m9-acceptance.sh). " +
		"Local mode starts no schedd, so nothing can flip a node's lifecycle."
}
