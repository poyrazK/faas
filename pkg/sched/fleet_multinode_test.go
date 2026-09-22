// spec: §6.2
// fleet_multinode_test.go — properties that only appear at three or more
// nodes, exercised through the real admit path.
//
// Why this file exists. Every multi-node behaviour in the tree was pinned
// either by the pure chooser (placement_test.go feeds ChoosePlacement a
// literal node slice) or by a single Engine with one WithOwnerNodeID. The one
// test that built two engines — TestClaimUnplaced_RaceLosesSilently — gave
// them no distinct owners and a single node. So the shape production actually
// runs after ADR-062 (N schedds, each owning a shard of apps, all placing onto
// a shared fleet) had no coverage at all, and neither did any fleet larger
// than two.
//
// That matters because the scaling properties worth protecting are all
// ≥3-node properties:
//
//   - the per-node RAM ceiling is enforced against a fleet, not a box
//     (§6.2-2, ADR-193) — and the enforcement has to survive two schedds
//     admitting at once, which is exactly what no single-engine test can show;
//   - placement has to spread, not pile, once there is somewhere to spread to
//     (two nodes cannot distinguish "spreads" from "alternates").
//
// Both tests below were checked against a deliberately broken build before
// being trusted: neutering betterCandidate (the chooser's comparison) makes
// the spread test pile all three instances on one node, and neutering
// MemStore's per-node reservation makes the ceiling test report two nodes at
// 768 MB against a 512 MB ceiling. A fleet test that cannot be made to fail
// is scenery.
//
// Deliberately NOT covered: app-ownership distribution. A test was written
// and removed, because a probe showed ClaimUnplaced stamps the CLAIMING
// engine's own node — so any "ownership spreads" assertion measures which
// engine the test handed each app to, not a platform property. Ownership
// therefore follows whichever schedd wins the claim race on the broadcast
// app_changed channel, and nothing rebalances it afterwards
// (pkg/sched/rebalancer.go reacts only to active=false). That gap is real and
// still uncovered; it needs a claim-race harness, not a round-robin loop.
//
// MemStore is deliberate. The store-layer concurrency for §6.2-2 is pinned
// against real PostgreSQL in pkg/state (advisory-lock serialization with a
// negative control); what is unpinned is the ENGINE layer above it, and
// MemStore mirrors the same reservation under its own mutex. Keeping these in
// the pure-Go shard means the fleet properties run on every PR rather than
// only where a database is reachable.
package sched

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// fleet is a multi-node, multi-schedd fixture: nodeCount compute nodes and
// one Engine per node, each stamped with that node's id as its owner, all
// sharing one store. That is the post-ADR-062 production shape.
type fleet struct {
	store   *state.MemStore
	ctx     context.Context
	nodes   []state.ComputeNode
	engines []*Engine
}

// newFleet builds a fleet of nodeCount active nodes, each with the same
// admission ceiling so the arithmetic in the assertions stays legible.
//
// MemStore pre-seeds a "default-local" node. It is deactivated here rather
// than reused: leaving it active would silently add an (N+1)th placement
// target with a different ceiling, and every capacity assertion below would
// be measuring a fleet it did not describe.
func newFleet(t *testing.T, nodeCount, ceilingMB int) *fleet {
	t.Helper()
	store := state.NewMemStore()
	ctx := context.Background()

	existing, err := store.ListComputeNodes(ctx, true)
	if err != nil {
		t.Fatalf("ListComputeNodes: %v", err)
	}
	for _, n := range existing {
		if n.Name != "default-local" {
			continue
		}
		if err := store.MarkComputeNodeInactive(ctx, n.ID); err != nil {
			t.Fatalf("deactivate default-local: %v", err)
		}
	}

	f := &fleet{store: store, ctx: ctx}
	for i := 0; i < nodeCount; i++ {
		node, err := store.CreateComputeNode(ctx, state.ComputeNode{
			// Names sort in creation order so a tie-break in the chooser is
			// predictable when several nodes are equally loaded.
			Name:               fmt.Sprintf("fleet-%02d", i),
			TargetURL:          fmt.Sprintf("tcp://10.0.0.%d:7000", i+2),
			VPCPUs:             80,
			MemMB:              ceilingMB * 2,
			MaxConcurrency:     50,
			AdmissionCeilingMB: ceilingMB,
			VCPUBudget:         80,
			Active:             true,
		})
		if err != nil {
			t.Fatalf("CreateComputeNode[%d]: %v", i, err)
		}
		f.nodes = append(f.nodes, node)
	}
	for i := range f.nodes {
		f.engines = append(f.engines,
			newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
				WithOwnerNodeID(f.nodes[i].ID))
	}
	return f
}

// seedOwnedApp creates an app owned by the node at ownerIdx, so the engine
// for that node is the one that admits it.
func (f *fleet) seedOwnedApp(t *testing.T, ownerIdx, ramMB, maxConc int) state.App {
	t.Helper()
	acct, err := f.store.CreateAccount(f.ctx, "u-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := f.store.CreateApp(f.ctx, state.App{
		AccountID:      acct.ID,
		Slug:           "fleet-app-" + uuid.NewString()[:8],
		RAMMB:          ramMB,
		MaxConcurrency: maxConc,
		IdleTimeoutS:   60,
		NodeID:         f.nodes[ownerIdx].ID,
		Status:         state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if _, err := f.store.CreateDeployment(f.ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + uuid.NewString(), Status: state.DeployLive,
	}); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return app
}

// usedByNode reports live RAM per node id, the same sum the placement chooser
// and the ADR-193 reservation both read.
func (f *fleet) usedByNode(t *testing.T) map[string]int64 {
	t.Helper()
	used := make(map[string]int64, len(f.nodes))
	for _, n := range f.nodes {
		mb, err := f.store.ComputeNodeUsedMB(f.ctx, n.ID)
		if err != nil {
			t.Fatalf("ComputeNodeUsedMB(%s): %v", n.Name, err)
		}
		used[n.ID] = mb
	}
	return used
}

// TestFleet_PlacementSpreadsAcrossThreeNodes pins that placement distributes
// rather than piling onto whichever node sorts first.
//
// Two nodes cannot show this: alternating and spreading look identical. With
// three equal nodes and three admissions, the chooser must put exactly one on
// each.
//
// What drives it is betterCandidate, which scores CPU headroom first and
// falls through to RAM. The inputs are redundant — the store's per-node sum
// and the ledger's per-node reservations both feed it — so zeroing either one
// alone does not change the outcome. Neutering betterCandidate itself does:
// all three then land on the node that sorts first, which is how affinity or
// a scoring regression packs one host while idle peers sit next to it.
func TestFleet_PlacementSpreadsAcrossThreeNodes(t *testing.T) {
	f := newFleet(t, 3, 4096)
	app := f.seedOwnedApp(t, 0, 256, 5)

	placed := map[string]int{}
	for i := 0; i < 3; i++ {
		res, err := f.engines[0].AdmitInstance(f.ctx, app.ID, "", "", "")
		if err != nil {
			t.Fatalf("AdmitInstance[%d]: %v", i, err)
		}
		if res.NodeID == "" {
			t.Fatalf("AdmitInstance[%d] returned no node id", i)
		}
		placed[res.NodeID]++
	}

	if len(placed) != 3 {
		t.Errorf("instances landed on %d distinct nodes, want 3 — placement piled instead of spreading (distribution: %v)",
			len(placed), placed)
	}
	for _, n := range f.nodes {
		if placed[n.ID] != 1 {
			t.Errorf("node %s took %d instances, want exactly 1", n.Name, placed[n.ID])
		}
	}
}

// TestFleet_NodeCeilingHoldsAcrossSchedds is the engine-level companion to
// the store-level ADR-193 test.
//
// pkg/state pins that concurrent INSERTs cannot breach a node's ceiling. What
// that cannot show is whether the ENGINE honours the refusal: schedd keeps an
// in-memory NodeLedger whose view is limited to the apps it owns, so two
// schedds admitting at once each believe they have more headroom than the
// fleet actually has. This drives that exact shape — two owners, three nodes,
// ceilings small enough that unrestrained admission would overshoot — and
// asserts the durable outcome.
//
// The assertion is the invariant itself, not a count: however the two engines
// interleaved, no node may exceed its ceiling.
func TestFleet_NodeCeilingHoldsAcrossSchedds(t *testing.T) {
	// Each node fits exactly two 248 MB instances: 2 × (248 + 8) = 512.
	const (
		ceilingMB = 512
		ramMB     = 248
		perEngine = 6
	)
	f := newFleet(t, 3, ceilingMB)
	appA := f.seedOwnedApp(t, 0, ramMB, perEngine)
	appB := f.seedOwnedApp(t, 1, ramMB, perEngine)

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	admit := func(e *Engine, appID string) {
		defer done.Done()
		start.Wait()
		// Errors are expected once the fleet fills — capacity refusal is the
		// correct outcome, not a failure. The invariant is checked after.
		_, _ = e.AdmitInstance(f.ctx, appID, "", "", "")
	}
	for i := 0; i < perEngine; i++ {
		done.Add(2)
		go admit(f.engines[0], appA.ID)
		go admit(f.engines[1], appB.ID)
	}
	start.Done()
	done.Wait()

	used := f.usedByNode(t)
	for _, n := range f.nodes {
		if used[n.ID] > ceilingMB {
			t.Errorf("node %s holds %d MB against a %d MB ceiling — invariant §6.2-2 breached across schedds",
				n.Name, used[n.ID], ceilingMB)
		}
	}

	// Sanity: the run has to have actually admitted something, or the
	// invariant above passes vacuously and the test proves nothing.
	var total int64
	for _, mb := range used {
		total += mb
	}
	if total == 0 {
		t.Fatal("no instance was admitted anywhere; the test exercised nothing")
	}
}

// TestFleet_OwnershipChecksCountDiscardedBroadcasts pins the consumer half of
// the broadcast-amplification measurement.
//
// ADR-062 shards apps across schedds by apps.node_id, but pg_notify carries
// no routing: every app-scoped notification reaches every schedd in the
// fleet, and all but the owner discard it at ownsApp. That discarded work is
// the cost of broadcast, and until it is counted the case for per-owner
// channels rests on arithmetic nobody has checked against a running fleet.
//
// Three nodes, one app owned by node 0. The owner's check must record
// `owned`; the two peers' checks must record `not_owned`. On a fleet of N
// that ratio is the (N-1)/N amplification, which is the number the refactor
// has to beat.
func TestFleet_OwnershipChecksCountDiscardedBroadcasts(t *testing.T) {
	f := newFleet(t, 3, 4096)
	app := f.seedOwnedApp(t, 0, 256, 2)

	owned, err := f.store.AppByID(f.ctx, app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}

	ops := make([]*wire.OpsMetrics, len(f.engines))
	for i := range f.engines {
		ops[i] = wire.NewOpsMetrics("schedd")
		f.engines[i] = f.engines[i].WithOpsMetrics(ops[i])
		// Every schedd sees the notification, so every schedd runs the check.
		f.engines[i].ownsApp(owned)
	}

	wantOwned := []int{1, 0, 0}
	for i := range f.engines {
		body := getMetricsBody(t, ops[i])
		gotOwned := readCounter(t, body, `schedd_app_ownership_checks_total{outcome="owned"}`)
		gotNot := readCounter(t, body, `schedd_app_ownership_checks_total{outcome="not_owned"}`)
		if gotOwned != wantOwned[i] {
			t.Errorf("engine %d owned=%d, want %d", i, gotOwned, wantOwned[i])
		}
		if want := 1 - wantOwned[i]; gotNot != want {
			t.Errorf("engine %d not_owned=%d, want %d", i, gotNot, want)
		}
	}
}

// readCounter pulls an exact labelled counter line out of a /metrics body.
// Returns 0 when absent, which is also what a pre-instantiated closed-set
// counter reports before its first increment.
func readCounter(t *testing.T, body, series string) int {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, series+" ") {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, series)), 64)
		if err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		return int(v)
	}
	return 0
}
