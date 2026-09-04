// commands_compute_nodes.go — gregalectl compute-nodes subcommand
// dispatcher (PR #929 review-fix).
//
// Used by the image-rollout orchestrator at cmd/deployctl/upgrade.go:
// the upgrade-node flow calls `gregalectl compute-nodes drain --node X`,
// `gregalectl compute-nodes drain-status --node X`, and
// `gregalectl compute-nodes activate --node X`. Without this dispatcher
// the upgrade orchestrator was dead on arrival — the CLI fell into the
// default case (cmd/gregalectl/main.go:155-157) and exited 1.
//
// Wire shape: every subcommand takes --node=<fqdn>; the state package
// owns the canonical SQL UPDATE (pkg/state.MarkComputeNodeInactive for
// drain, pkg/state.SetComputeNodeActive(ctx, id, true) for activate).
// drain-status queries pkg/state.ListInstancesOnNodeID and counts rows
// in {WAKING, COLD_BOOTING, RUNNING}; > 0 means the upgrade orchestrator
// blocks until the operator runs force-drain.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// dispatchComputeNodes is wired into cmd/gregalectl/main.go:switch
// alongside the other dispatch* consts.
const dispatchComputeNodes = "compute-nodes"

// cmdComputeNodesDispatch fans to add / drain / drain-status /
// activate / force-drain / list / show. Matches the (args []string) int
// signature every other dispatch* arm uses (see commands_release.go:cmdReleaseDispatch).
//
// `add` is the operator-side pre-registration path: it POSTs a row to
// compute_nodes before vmmd has booted on the new box. The runbook's
// `target_url` discipline (multi-host-rollout.md §3.5) makes this
// order load-bearing — vmmd's self-registration UPSERT preserves the
// operator's POSTed target_url via UpsertComputeNodeFromVmmd's
// COALESCE, so the operator must land the row first.
//
// `list` / `show` are the read-only introspection pair (Cluster C of
// the gregalectl mega-PR). Mirrors the `compute-nodes` admin API
// surface (cmd/apid/compute_nodes.go) but goes through state.Store
// directly so the operator can sanity-check the cluster without
// standing up apid + auth.
func cmdComputeNodesDispatch(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes: missing subcommand; want add|list|show|drain|drain-status|activate|force-drain")
		return 2
	}
	switch args[0] {
	case "add":
		return cmdComputeNodesAdd(args[1:])
	case "list":
		return cmdComputeNodesList(args[1:])
	case "show":
		return cmdComputeNodesShow(args[1:])
	case "drain":
		return cmdComputeNodesDrain(args[1:])
	case "drain-status":
		return cmdComputeNodesDrainStatus(args[1:])
	case "activate":
		return cmdComputeNodesActivate(args[1:])
	case "force-drain":
		return cmdComputeNodesForceDrain(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl compute-nodes: unknown subcommand %q\n", args[0])
		return 2
	}
}

// computeNodesStoreOpener is the seam tests use to swap in a
// MemStore without going through FAAS_PG_DSN. Production wires this
// to openComputeNodesStore; tests wire to a MemStore-returning
// helper via setComputeNodesStoreOpener.
//
// The opener returns a `close func()` instead of leaking the
// *pgxpool.Pool back out — production wires close to pool.Close;
// tests wire close to a no-op so the typed-nil pool never reaches
// pgx internals. Keeps the seam minimal and test-friendly.
var computeNodesStoreOpener = openComputeNodesStore

// setComputeNodesStoreOpener is the test-only swap helper. NOT for
// production use — the var is unexported so the only callers are
// in the same package's _test.go files.
func setComputeNodesStoreOpener(fn func() (state.Store, func(), error)) {
	computeNodesStoreOpener = fn
}

// openComputeNodesStore wires a state.Store from FAAS_PG_DSN via the
// existing openPgPoolFromEnv helper (commands_release.go:344). The
// returned close func releases the pool when the caller defers it.
func openComputeNodesStore() (state.Store, func(), error) {
	pool, err := openPgPoolFromEnv(context.Background())
	if err != nil {
		return nil, func() {}, fmt.Errorf("gregalectl compute-nodes: %w", err)
	}
	return state.NewPgStore(pool), pool.Close, nil
}

// pgxpool reference kept so the build compiles when this file is the
// only one that ever imported it — protects against future edits
// deleting the import in a refactor.
var _ = (*pgxpool.Pool)(nil)

// cmdComputeNodesDrain resolves the operator-facing node name to its row ID,
// then runs `UPDATE compute_nodes SET active=false` via
// state.Store.MarkComputeNodeInactive.
func cmdComputeNodesDrain(args []string) int {
	fs := flag.NewFlagSet("drain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	node := fs.String("node", "", "fqdn of the node to drain")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *node == "" {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes drain: --node required")
		return 2
	}
	st, closeFn, err := computeNodesStoreOpener()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeFn()

	ctx := context.Background()
	computeNode, err := st.ComputeNodeByName(ctx, *node)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes drain:", err)
		return 1
	}
	if err := st.MarkComputeNodeInactive(ctx, computeNode.ID); err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes drain:", err)
		return 1
	}
	_, _ = fmt.Fprintf(os.Stdout, "drained %s\n", *node)
	return 0
}

// cmdComputeNodesDrainStatus reports whether live instances remain on
// the node. Exit 0 if drain is safe (no live instances); exit 1 if
// instances still pinned (upgrade orchestrator surfaces this as
// "instances still on node" and the operator runs force-drain).
//
// Per the state-machine RAM invariant, an instance is "live" iff its
// state counts resident RAM (WAKING, COLD_BOOTING, RUNNING,
// SNAPSHOTTING, or MIGRATING). The state package exposes
// ListInstancesOnNodeID; we filter to that live subset here.
func cmdComputeNodesDrainStatus(args []string) int {
	fs := flag.NewFlagSet("drain-status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	node := fs.String("node", "", "fqdn of the node to check")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *node == "" {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes drain-status: --node required")
		return 2
	}
	st, closeFn, err := computeNodesStoreOpener()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeFn()

	ctx := context.Background()
	computeNode, err := st.ComputeNodeByName(ctx, *node)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes drain-status:", err)
		return 1
	}
	insts, err := st.ListInstancesOnNodeID(ctx, computeNode.ID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes drain-status:", err)
		return 1
	}
	live := 0
	for _, inst := range insts {
		if state.IsLive(strings.ToLower(inst.State)) {
			live++
		}
	}
	if live > 0 {
		_, _ = fmt.Fprintf(os.Stdout, "instances still on %s: %d\n", *node, live)
		return 1
	}
	_, _ = fmt.Fprintf(os.Stdout, "drain-safe: %s has 0 live instances\n", *node)
	return 0
}

// cmdComputeNodesActivate runs the inverse — flips active=true. Only
// invoked by the upgrade orchestrator AFTER every Lifecycle.Probe on
// every Registry entry reports ready (cmd/deployctl/upgrade.go:waitForReady).
func cmdComputeNodesActivate(args []string) int {
	fs := flag.NewFlagSet("activate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	node := fs.String("node", "", "fqdn of the node to activate")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *node == "" {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes activate: --node required")
		return 2
	}
	st, closeFn, err := computeNodesStoreOpener()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeFn()

	ctx := context.Background()
	computeNode, err := st.ComputeNodeByName(ctx, *node)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes activate:", err)
		return 1
	}
	if err := st.SetComputeNodeActive(ctx, computeNode.ID, true); err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes activate:", err)
		return 1
	}
	_, _ = fmt.Fprintf(os.Stdout, "activated %s\n", *node)
	return 0
}

// cmdComputeNodesForceDrain is the operator's escape hatch when an
// upgrade can't move because live instances are pinned. NOT called by
// the upgrade orchestrator (the operator runs this manually after
// acknowledging the loud warning). Same SQL as drain but explicitly
// named so the operator's intent is auditable.
func cmdComputeNodesForceDrain(args []string) int {
	fs := flag.NewFlagSet("force-drain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	node := fs.String("node", "", "fqdn of the node to force-drain")
	ack := fs.Bool("yes", false, "acknowledge that live instances may be cold-evicted")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *node == "" {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes force-drain: --node required")
		return 2
	}
	if !*ack {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes force-drain: --yes required (live instances may be cold-evicted)")
		return 2
	}
	st, closeFn, err := computeNodesStoreOpener()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeFn()

	ctx := context.Background()
	computeNode, err := st.ComputeNodeByName(ctx, *node)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes force-drain:", err)
		return 1
	}
	if err := st.MarkComputeNodeInactive(ctx, computeNode.ID); err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes force-drain:", err)
		return 1
	}
	_, _ = fmt.Fprintf(os.Stdout, "force-drained %s\n", *node)
	return 0
}

// cmdComputeNodesList is the read-only introspection entry. Walks
// every row in compute_nodes (or just the active ones with
// --active-only) and emits a one-line summary per node. --json
// emits a structured report for CI gates that need to assert on
// fleet shape (e.g. "exactly 3 active nodes" in a smoke test).
//
// The exit code is 0 when the query succeeds, regardless of row
// count (an empty fleet is not an error condition — it just means
// no nodes have been registered yet). The read path uses the same
// openComputeNodesStore seam the write paths use, so tests can
// swap in a MemStore via setComputeNodesStoreOpener.
func cmdComputeNodesList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	activeOnly := fs.Bool("active-only", false, "filter to active=true rows (default: every row regardless of drain state)")
	jsonOut := fs.Bool("json", false, "emit structured JSON to stdout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes list: unexpected positional args")
		return 2
	}
	st, closeFn, err := computeNodesStoreOpener()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeFn()

	nodes, err := st.ListComputeNodes(context.Background(), !*activeOnly)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl compute-nodes list: %v\n", err)
		return 1
	}
	if *jsonOut {
		return emitComputeNodesListJSON(osStdout, nodes)
	}
	reportComputeNodesList(osStdout, nodes, *activeOnly)
	return 0
}

// reportComputeNodesList prints one line per node in a fixed-width
// table. Empty fleet prints a single "(no compute nodes)" line so
// the operator can tell at a glance that the query succeeded but
// no rows match (rather than a blank screen that looks like the
// command hung).
func reportComputeNodesList(w io.Writer, nodes []state.ComputeNode, activeOnly bool) {
	if len(nodes) == 0 {
		_, _ = fmt.Fprintf(w, "(no compute nodes%s)\n", activeOnlyString(activeOnly))
		return
	}
	_, _ = fmt.Fprintf(w, "%-32s  %-15s  %-7s  %-7s  %-7s  %-7s  %-36s  %s\n", "NAME", "ROLE", "VPCPUS", "MEM_MB", "MAXCON", "ACTIVE", "TARGET_URL", "GATEWAY_TARGET_URL")
	for _, n := range nodes {
		role := ""
		if n.Role != nil {
			role = *n.Role
		}
		_, _ = fmt.Fprintf(w, "%-32s  %-15s  %-7d  %-7d  %-7d  %-7t  %-36s  %s\n",
			n.Name, role, n.VPCPUs, n.MemMB, n.MaxConcurrency, n.Active, n.TargetURL, computeNodeGatewayTargetValue(n.GatewayTargetURL))
	}
}

func activeOnlyString(activeOnly bool) string {
	if activeOnly {
		return " (active-only)"
	}
	return ""
}

// computeNodesListJSON is the wire shape for `compute-nodes list --json`.
// Field set pinned by json_parity_test so CI gates can rely on the
// schema. count + nodes mirror the on-disk row count + the rows
// themselves so a gate can assert "count == 3" without walking the
// array.
type computeNodesListJSON struct {
	Count int                `json:"count"`
	Nodes []computeNodeBrief `json:"nodes"`
}

// computeNodeBrief is the per-node JSON shape. We deliberately
// drop the heavy pointer fields (HostCertificate, CertFingerprint,
// ReleaseID, ManifestHash, Generation) that the cmd-line show
// path exposes; the list is meant for fleet-level assertions
// ("which boxes are registered, what's their role / capacity /
// dial target") not for per-node audit. The show subcommand is
// the place that surfaces the heavy fields.
type computeNodeBrief struct {
	Name               string  `json:"name"`
	ID                 string  `json:"id"`
	Role               *string `json:"role,omitempty"`
	VPCPUs             int     `json:"vpcpus"`
	MemMB              int     `json:"mem_mb"`
	MaxConcurrency     int     `json:"max_concurrency"`
	AdmissionCeilingMB int     `json:"admission_ceiling_mb"`
	Active             bool    `json:"active"`
	TargetURL          string  `json:"target_url"`
	GatewayTargetURL   string  `json:"gateway_target_url,omitempty"`
}

func emitComputeNodesListJSON(w io.Writer, nodes []state.ComputeNode) int {
	out := computeNodesListJSON{
		Count: len(nodes),
		Nodes: make([]computeNodeBrief, 0, len(nodes)),
	}
	for _, n := range nodes {
		out.Nodes = append(out.Nodes, computeNodeBrief{
			Name: n.Name, ID: n.ID, Role: n.Role,
			VPCPUs: n.VPCPUs, MemMB: n.MemMB,
			MaxConcurrency: n.MaxConcurrency, AdmissionCeilingMB: n.AdmissionCeilingMB,
			Active: n.Active, TargetURL: n.TargetURL,
			GatewayTargetURL: computeNodeGatewayTargetValue(n.GatewayTargetURL),
		})
	}
	body, err := json.Marshal(out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl compute-nodes list: marshal json: %v\n", err)
		return 1
	}
	if _, err := w.Write(body); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl compute-nodes list: write json: %v\n", err)
		return 1
	}
	_, _ = w.Write([]byte("\n"))
	return 0
}

// cmdComputeNodesShow is the per-node introspection leaf. Mirrors
// the cmd-line dump from the `add` JSON shape but for an
// already-registered row, plus live_instance_count (the count of
// instances in {WAKING, COLD_BOOTING, RUNNING} on the node).
// --json emits the full ComputeNode + the live count.
//
// Missing node is exit 3 (the "row not found" convention that
// state.Store.ComputeNodeByName returns ErrNotFound for, distinct
// from the "DB unreachable" exit 1).
func cmdComputeNodesShow(args []string) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	node := fs.String("node", "", "fqdn / short-hostname of the node to show (required)")
	jsonOut := fs.Bool("json", false, "emit structured JSON to stdout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *node == "" {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes show: --node required")
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes show: unexpected positional args")
		return 2
	}
	st, closeFn, err := computeNodesStoreOpener()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeFn()

	ctx := context.Background()
	row, err := st.ComputeNodeByName(ctx, *node)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			fmt.Fprintf(os.Stderr, "gregalectl compute-nodes show: no compute_node with name=%q\n", *node)
			return 3
		}
		fmt.Fprintf(os.Stderr, "gregalectl compute-nodes show: %v\n", err)
		return 1
	}
	insts, err := st.ListInstancesOnNodeID(ctx, row.ID)
	if err != nil {
		// Live-instance count is informational (drives the
		// drain-status UX); a query failure must NOT hide the
		// row data. Emit a WARN to stderr and continue with
		// live=0 so the operator still sees the node.
		fmt.Fprintf(os.Stderr, "warn: ListInstancesOnNodeID(%q): %v (live_instance_count reported as 0)\n", row.ID, err)
		insts = nil
	}
	live := 0
	for _, inst := range insts {
		if state.IsLive(strings.ToLower(inst.State)) {
			live++
		}
	}
	if *jsonOut {
		return emitComputeNodeShowJSON(osStdout, row, live)
	}
	reportComputeNodeShow(os.Stdout, row, live)
	return 0
}

// reportComputeNodeShow dumps the row as a multi-line key=value
// listing. We use the same output shape as `add` (so an operator
// who just registered a box can grep both outputs into the same
// record), plus the live_instance_count footer.
func reportComputeNodeShow(w io.Writer, n state.ComputeNode, live int) {
	role := ""
	if n.Role != nil {
		role = *n.Role
	}
	_, _ = fmt.Fprintf(w, "name=%s\n", n.Name)
	_, _ = fmt.Fprintf(w, "id=%s\n", n.ID)
	_, _ = fmt.Fprintf(w, "role=%s\n", role)
	_, _ = fmt.Fprintf(w, "target_url=%s\n", n.TargetURL)
	_, _ = fmt.Fprintf(w, "gateway_target_url=%s\n", computeNodeGatewayTargetValue(n.GatewayTargetURL))
	_, _ = fmt.Fprintf(w, "vpcpus=%d\n", n.VPCPUs)
	_, _ = fmt.Fprintf(w, "mem_mb=%d\n", n.MemMB)
	_, _ = fmt.Fprintf(w, "max_concurrency=%d\n", n.MaxConcurrency)
	_, _ = fmt.Fprintf(w, "admission_ceiling_mb=%d\n", n.AdmissionCeilingMB)
	_, _ = fmt.Fprintf(w, "active=%t\n", n.Active)
	if n.Region != nil {
		_, _ = fmt.Fprintf(w, "region=%s\n", *n.Region)
	}
	if n.Zone != nil {
		_, _ = fmt.Fprintf(w, "zone=%s\n", *n.Zone)
	}
	if n.ReleaseID != nil {
		_, _ = fmt.Fprintf(w, "release_id=%s\n", *n.ReleaseID)
	}
	if n.ManifestHash != nil {
		_, _ = fmt.Fprintf(w, "manifest_hash=%s\n", *n.ManifestHash)
	}
	if n.CertFingerprint != nil {
		_, _ = fmt.Fprintf(w, "cert_fingerprint=%s\n", *n.CertFingerprint)
	}
	if n.Generation != nil {
		_, _ = fmt.Fprintf(w, "generation=%d\n", *n.Generation)
	}
	_, _ = fmt.Fprintf(w, "live_instance_count=%d\n", live)
}

// computeNodeShowJSON is the wire shape for `compute-nodes show --json`.
// Fields mirror the read-only set on the row + live_instance_count.
type computeNodeShowJSON struct {
	Name               string  `json:"name"`
	ID                 string  `json:"id"`
	Role               *string `json:"role,omitempty"`
	TargetURL          string  `json:"target_url"`
	GatewayTargetURL   string  `json:"gateway_target_url,omitempty"`
	VPCPUs             int     `json:"vpcpus"`
	MemMB              int     `json:"mem_mb"`
	MaxConcurrency     int     `json:"max_concurrency"`
	AdmissionCeilingMB int     `json:"admission_ceiling_mb"`
	Active             bool    `json:"active"`
	Region             *string `json:"region,omitempty"`
	Zone               *string `json:"zone,omitempty"`
	ReleaseID          *string `json:"release_id,omitempty"`
	ManifestHash       *string `json:"manifest_hash,omitempty"`
	CertFingerprint    *string `json:"cert_fingerprint,omitempty"`
	Generation         *int    `json:"generation,omitempty"`
	LiveInstanceCount  int     `json:"live_instance_count"`
}

func emitComputeNodeShowJSON(w io.Writer, n state.ComputeNode, live int) int {
	body, err := json.Marshal(computeNodeShowJSON{
		Name: n.Name, ID: n.ID, Role: n.Role,
		TargetURL: n.TargetURL, GatewayTargetURL: computeNodeGatewayTargetValue(n.GatewayTargetURL), VPCPUs: n.VPCPUs, MemMB: n.MemMB,
		MaxConcurrency: n.MaxConcurrency, AdmissionCeilingMB: n.AdmissionCeilingMB,
		Active: n.Active, Region: n.Region, Zone: n.Zone,
		ReleaseID: n.ReleaseID, ManifestHash: n.ManifestHash,
		CertFingerprint: n.CertFingerprint, Generation: n.Generation,
		LiveInstanceCount: live,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl compute-nodes show: marshal json: %v\n", err)
		return 1
	}
	if _, err := w.Write(body); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl compute-nodes show: write json: %v\n", err)
		return 1
	}
	_, _ = w.Write([]byte("\n"))
	return 0
}

// cmdComputeNodesAdd is the operator-side pre-registration entry
// point for adding a compute node to the fleet. Mirrors the admin
// POST handler at cmd/apid/compute_nodes.go:createOrUpdateComputeNode
// (the apid handler is unchanged; this is a CLI wrapper that goes
// straight to the state.Store via openComputeNodesStore).
//
// The `--from-file` flag is the bridge that PR-B's
// `gregalectl deploy add-node` uses to invoke this subcommand with
// a payload it builds in-memory. When `--from-file` is set, the
// remaining flags are ignored; the JSON body must match
// computeNodePayload's shape exactly.
//
// Field semantics mirror the apid handler's 400 surface so the
// operator and the daemon agree on what "valid" means: zero-valued
// capacity fields are a config bug, not a meaningful "I want a
// node with zero RAM" state. target_url is validated against the
// per-host dial-target discipline (rejecting loopback / 0.0.0.0 /
// empty / missing scheme).
func cmdComputeNodesAdd(args []string) int {
	return cmdComputeNodesAddTo(args, os.Stdout)
}

// cmdComputeNodesAddTo is the seam that takes an explicit
// stdout writer. PR-A's CLI calls through cmdComputeNodesAdd
// (which writes to os.Stdout); PR-B's deploy add-node calls
// cmdComputeNodesAddTo with io.Discard so the inner OK line /
// JSON blob doesn't pollute the outer report's stdout.
//
// Same flag set, same validation, same exit codes — only the
// stdout destination differs.
func cmdComputeNodesAddTo(args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	name := fs.String("name", "", "fqdn / short-hostname of the new node (required)")
	targetURL := fs.String("target-url", "", "routable dial target for vmmd (tcp://vmmd-N.faas:50051 or unix://...)")
	gatewayTargetURL := fs.String("gateway-target-url", "", "private HTTP target for gatewayd-internal (tcp://host:port)")
	vpcpus := fs.Int("vpcpus", 0, "vCPU count reported to schedd")
	memMB := fs.Int("mem-mb", 0, "RAM MB reported to schedd")
	maxConc := fs.Int("max-concurrency", 0, "max concurrent live instances")
	admCeil := fs.Int("admission-ceiling-mb", 0, "tenant RAM admission ceiling (85% of mem-mb for production nodes)")
	fromFile := fs.String("from-file", "", "read a computeNodePayload-shaped JSON file instead of the per-field flags (PR-B bridge)")
	deferActivation := fs.Bool("defer-activation", false, "insert/update the row drained so a deployment can activate it after readiness checks")
	jsonOut := fs.Bool("json", false, "emit structured JSON to stdout")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	var payload computeNodePayload
	if *fromFile != "" {
		body, err := os.ReadFile(*fromFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gregalectl compute-nodes add: read --from-file %s: %v\n", *fromFile, err)
			return 1
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			fmt.Fprintf(os.Stderr, "gregalectl compute-nodes add: parse --from-file %s: %v\n", *fromFile, err)
			return 3
		}
	} else {
		payload = computeNodePayload{
			Name:               *name,
			TargetURL:          *targetURL,
			GatewayTargetURL:   *gatewayTargetURL,
			VPCPUs:             *vpcpus,
			MemMB:              *memMB,
			MaxConcurrency:     *maxConc,
			AdmissionCeilingMB: *admCeil,
		}
	}

	// Validation mirrors the apid 400 surface
	// (cmd/apid/compute_nodes.go:createOrUpdateComputeNode). When
	// --from-file is used the operator may not know the per-field
	// shape; emitting the same messages keeps the runbook's
	// "curl returned 400 because X" line true whether the operator
	// ran curl or this CLI.
	if payload.Name == "" {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes add: --name required")
		return 2
	}
	if !validComputeNodeName(payload.Name) {
		fmt.Fprintf(os.Stderr, "gregalectl compute-nodes add: --name %q is not a valid fqdn (must match ^[a-z0-9][a-z0-9.\\-]{0,62}[a-z0-9]$)\n", payload.Name)
		return 2
	}
	if payload.TargetURL == "" {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes add: --target-url required (unix:///... or tcp://...)")
		return 2
	}
	if err := validDialTargetURL(payload.TargetURL); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl compute-nodes add: --target-url invalid: %v\n", err)
		return 2
	}
	if strings.TrimSpace(payload.GatewayTargetURL) != "" {
		if err := validGatewayTargetURL(payload.GatewayTargetURL); err != nil {
			fmt.Fprintf(os.Stderr, "gregalectl compute-nodes add: --gateway-target-url invalid: %v\n", err)
			return 2
		}
	}
	if payload.VPCPUs <= 0 || payload.MemMB <= 0 || payload.MaxConcurrency <= 0 || payload.AdmissionCeilingMB <= 0 {
		fmt.Fprintln(os.Stderr, "gregalectl compute-nodes add: vpcpus, mem-mb, max-concurrency, admission-ceiling-mb must all be > 0")
		return 2
	}

	st, closeFn, err := computeNodesStoreOpener()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeFn()

	// Build the row. The CLI only knows 6 fields; the SQL
	// ON CONFLICT clause (pgstore.go:8873-8889) writes
	// release_id / manifest_hash / host_certificate /
	// cert_fingerprint / role from `excluded.*` — so passing
	// nil for those fields would NULL them out on a re-add and
	// break the installed node's PKI + release metadata.
	//
	// Read the existing row first; copy the operator-side
	// pointer fields (Role, ReleaseID, ManifestHash,
	// HostCertificate, CertFingerprint) so the UPSERT
	// preserves them. The cold-insert branch (no existing row)
	// leaves them nil — same as the pre-PR-A apid behavior.
	node := state.ComputeNode{
		Name:      payload.Name,
		TargetURL: payload.TargetURL,
		VPCPUs:    payload.VPCPUs,
		// The database enforces a positive per-node vCPU budget. The
		// operator payload historically carried physical vCPUs only, so
		// derive the default using the same 8x overcommit policy as the
		// scheduler instead of sending zero and violating the schema.
		VCPUBudget:         payload.VPCPUs * api.CPUOvercommit,
		MemMB:              payload.MemMB,
		MaxConcurrency:     payload.MaxConcurrency,
		AdmissionCeilingMB: payload.AdmissionCeilingMB,
	}
	if value := strings.TrimSpace(payload.GatewayTargetURL); value != "" {
		node.GatewayTargetURL = &value
	}
	if existing, lookupErr := st.ComputeNodeByName(context.Background(), payload.Name); lookupErr == nil {
		node.Role = existing.Role
		node.ReleaseID = existing.ReleaseID
		node.ManifestHash = existing.ManifestHash
		node.HostCertificate = existing.HostCertificate
		node.CertFingerprint = existing.CertFingerprint
		if node.GatewayTargetURL == nil {
			node.GatewayTargetURL = existing.GatewayTargetURL
		}
	} else if !errors.Is(lookupErr, state.ErrNotFound) {
		fmt.Fprintf(os.Stderr, "gregalectl compute-nodes add: lookup existing: %v\n", lookupErr)
		return 1
	}
	row, err := st.UpsertComputeNodeFromOperator(context.Background(), node)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl compute-nodes add: upsert: %v\n", err)
		return 1
	}
	if *deferActivation {
		if err := st.SetComputeNodeActive(context.Background(), row.ID, false); err != nil {
			fmt.Fprintf(os.Stderr, "gregalectl compute-nodes add: defer activation: %v\n", err)
			return 1
		}
		row.Active = false
	}

	// `compute_node_changed` pg_notify trigger (migration 00026) fires
	// on the UPSERT; the operator's manual curl + jq workflow and
	// this CLI share the same fan-out, so the runbook's "wait for
	// pg_notify" paragraph stays accurate. We emit a single line on
	// stderr naming the trigger channel so operators reading the
	// log can correlate.
	if _, err := fmt.Fprintln(os.Stderr, "compute_node_changed pg_notify fired (channel: compute_node_changed; subscribers: schedd, gatewayd-internal)"); err != nil {
		return 1
	}
	if *jsonOut {
		return emitComputeNodeAddedJSON(stdout, row)
	}
	_, _ = fmt.Fprintf(stdout, "OK name=%s id=%s target_url=%s gateway_target_url=%s vpcpus=%d mem_mb=%d max_concurrency=%d admission_ceiling_mb=%d\n",
		row.Name, row.ID, row.TargetURL, computeNodeGatewayTargetValue(row.GatewayTargetURL), row.VPCPUs, row.MemMB, row.MaxConcurrency, row.AdmissionCeilingMB)
	return 0
}

// computeNodePayload is the JSON shape consumed by --from-file.
// Mirrors cmd/apid/compute_nodes.go:computeNodePayload exactly so
// PR-B's deploy add-node can write the same payload to a scratch
// file and pass it through.
type computeNodePayload struct {
	Name               string `json:"name"`
	TargetURL          string `json:"target_url"`
	GatewayTargetURL   string `json:"gateway_target_url,omitempty"`
	VPCPUs             int    `json:"vpcpus"`
	MemMB              int    `json:"mem_mb"`
	MaxConcurrency     int    `json:"max_concurrency"`
	AdmissionCeilingMB int    `json:"admission_ceiling_mb"`
}

// emitComputeNodeAddedJSON writes the upserted row as structured
// JSON to w. Kept as a free function so tests can call it with a
// bytes.Buffer without touching os.Stdout directly.
func emitComputeNodeAddedJSON(w io.Writer, row state.ComputeNode) int {
	body, err := json.Marshal(struct {
		Name               string `json:"name"`
		ID                 string `json:"id"`
		TargetURL          string `json:"target_url"`
		GatewayTargetURL   string `json:"gateway_target_url,omitempty"`
		VPCPUs             int    `json:"vpcpus"`
		MemMB              int    `json:"mem_mb"`
		MaxConcurrency     int    `json:"max_concurrency"`
		AdmissionCeilingMB int    `json:"admission_ceiling_mb"`
		Active             bool   `json:"active"`
	}{
		Name: row.Name, ID: row.ID, TargetURL: row.TargetURL,
		GatewayTargetURL: computeNodeGatewayTargetValue(row.GatewayTargetURL),
		VPCPUs:           row.VPCPUs, MemMB: row.MemMB,
		MaxConcurrency: row.MaxConcurrency, AdmissionCeilingMB: row.AdmissionCeilingMB,
		Active: row.Active,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl compute-nodes add: marshal json: %v\n", err)
		return 1
	}
	if _, err := w.Write(body); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl compute-nodes add: write json: %v\n", err)
		return 1
	}
	_, _ = w.Write([]byte("\n"))
	return 0
}

func computeNodeGatewayTargetValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// validGatewayTargetURL accepts only the private TCP listener exposed by a
// compute node's gatewayd-internal. Keeping this separate from target_url is
// important: target_url is vmmd's gRPC endpoint and must never be used for
// HTTP application routing.
func validGatewayTargetURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("parse target: %w", err)
	}
	if u.Scheme != "tcp" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("must be tcp://host:port")
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("must be tcp://host:port")
	}
	if isNonRoutableHost(host) {
		return fmt.Errorf("host %q is not routable from the control plane", host)
	}
	return nil
}

// validComputeNodeName is the local mirror of the apid handler's
// name check. Kept minimal: short fqdn or short-hostname, lowercase,
// no leading/trailing dashes, dot-allowed.
func validComputeNodeName(name string) bool {
	if len(name) == 0 || len(name) > 63 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '-':
			if i == 0 || i == len(name)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// validDialTargetURL rejects loopback / unspecified / missing-scheme
// targets. Cross-box dial discipline (multi-host-rollout.md §3.5):
// `0.0.0.0` resolves to the local host's own vmmd, not the second
// box; `127.0.0.1` is loopback-only. The remaining schemes
// (tcp://, unix://) match pkg/wire/grpc.go's URL parser.
//
// The IPv4 loopback checks cover `0.0.0.0`, `127.0.0.1` and the
// `127.0.0.0/8` range. The IPv6 checks cover `::`, `::1`, the
// `fc00::/7` ULA range (routable on the box but never from a peer),
// and `fe80::/10` link-local. Without them, an operator pasting
// `tcp://[::1]:50051` or `tcp://[::]:50051` slips past the v4
// check and the peer box's dial loops back to itself.
func validDialTargetURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("empty target_url")
	}
	switch {
	case strings.HasPrefix(raw, "tcp://"):
		host := strings.TrimPrefix(raw, "tcp://")
		if host == "" {
			return fmt.Errorf("tcp:// with empty host")
		}
		// Strip an optional :port — the bracketed-IPv6 form
		// (`[::1]:50051`) needs the bracket stripped before the
		// host check; the bare form (`[::1]`) needs the same.
		hostOnly, _ := splitHostPort(host)
		if isNonRoutableHost(hostOnly) {
			return fmt.Errorf("tcp://%s is non-routable from a peer box; use the box's overlay or routable IP", hostOnly)
		}
		if !strings.Contains(host, ":") {
			return fmt.Errorf("tcp:// host missing port")
		}
		return nil
	case strings.HasPrefix(raw, "unix://"):
		if raw == "unix://" {
			return fmt.Errorf("unix:// with empty path")
		}
		return nil
	case strings.HasPrefix(raw, "dns://"):
		if raw == "dns://" {
			return fmt.Errorf("dns:// with empty hostname")
		}
		return nil
	default:
		return fmt.Errorf("scheme must be tcp://, unix://, or dns:// (got %q)", raw)
	}
}

// splitHostPort splits `tcp://host:port` into host + port for
// IPv4 and bracketed-IPv6 forms. For unbracketed IPv6 (`::1`) the
// port is empty (the caller has already failed the `strings.Contains(host, ":")`
// check). Returns the trimmed host part and the trimmed port
// (empty if absent).
func splitHostPort(host string) (string, string) {
	if strings.HasPrefix(host, "[") {
		// bracketed IPv6: [host]:port — close bracket required
		end := strings.Index(host, "]")
		if end < 0 {
			return strings.TrimPrefix(host, "["), ""
		}
		h := host[1:end]
		rest := strings.TrimPrefix(host[end+1:], ":")
		return h, rest
	}
	// IPv4 / hostname: last `:` is the port separator.
	idx := strings.LastIndex(host, ":")
	if idx < 0 {
		return host, ""
	}
	return host[:idx], host[idx+1:]
}

// isNonRoutableHost returns true for IPs that resolve to the
// local box (loopback / unspecified / link-local / ULA). The
// runbook's §3.5 cross-box dial discipline rejects these — a
// peer box cannot reach `vmmd` on the operator's loopback.
func isNonRoutableHost(host string) bool {
	if host == "" {
		return true
	}
	// IPv4 literal
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate()
	}
	// Handle the bracketed-literal case the parser above missed
	// (e.g. host arrives as `[::1]` due to upstream format drift).
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate()
	}
	// Hostname — hostname:port with no literal IP is operator's
	// own FQDN. Don't reject; the resolver can return a public IP.
	// (The actual dial failure will surface in the gRPC layer.)
	return false
}
