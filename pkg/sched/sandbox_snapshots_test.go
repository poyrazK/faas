// adr: 20261027090013 §12 — schedd captures a session shape's warm snapshot
// after its first clean cold boot on a node, under that session's
// reservation; later sessions of the shape restore it, one that cold-boots
// instead drops it, a template rebuilt since its capture cold-boots and
// recaptures into the same slot, no other account restores a template's
// snapshot, and a deleted template's snapshot is dropped and its objects
// deleted on the node.
package sched_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type capture struct {
	spec                       sched.SandboxSpec
	node, namespace, captureID string
}

// snapRouter ends each session at once. A session with a snapshot restores
// unless failRestore is set; captures return a snapshot named by its keys.
type snapRouter struct {
	*agentRouter
	mu          sync.Mutex
	failRestore bool
	specs       []sched.SandboxSpec
	captured    []capture
	resident    []bool
	deleted     []string
}

func (r *snapRouter) DeleteSandboxSnapshot(_ context.Context, nodeID string, snap *sched.AgentRunSnapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted = append(r.deleted, nodeID+" "+snap.StorageKey+" "+snap.VMStateStorageKey)
	return nil
}

func (r *snapRouter) RunSandbox(ctx context.Context, _ string, spec sched.SandboxSpec, hooks sched.SandboxHooks, _ <-chan sched.SandboxControl) error {
	r.mu.Lock()
	r.specs = append(r.specs, spec)
	method := "WAKE_COLD_BOOT"
	if spec.Snapshot != nil && !r.failRestore {
		method = "WAKE_RESTORE"
	}
	r.mu.Unlock()
	return hooks.Booted(ctx, method, 5)
}

func (r *snapRouter) CaptureSandboxSnapshot(_ context.Context, nodeID string, spec sched.SandboxSpec, namespace, captureID string) (*sched.AgentRunSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.captured = append(r.captured, capture{spec: spec, node: nodeID, namespace: namespace, captureID: captureID})
	r.resident = append(r.resident, r.ledger.ResidentFor(spec.Instance))
	key := "snap/" + namespace + "/captures/" + captureID + "/v2/mem"
	return &sched.AgentRunSnapshot{StorageKey: key, VMStateStorageKey: key + "-state", FCVersion: "1.7.0", MemBytes: 1 << 30}, nil
}

func (r *snapRouter) lastSpec() sched.SandboxSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.specs[len(r.specs)-1]
}

func (r *snapRouter) captures() []capture {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]capture(nil), r.captured...)
}

func snapshotFixture(t *testing.T, on bool) (*sched.AgentRunner, *snapRouter, *sched.NodeLedger) {
	t.Helper()
	runner, router, ledger, _ := snapshotFixtureWithStore(t, on)
	return runner, router, ledger
}

func snapshotFixtureWithStore(t *testing.T, on bool) (*sched.AgentRunner, *snapRouter, *sched.NodeLedger, *state.MemStore) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	if _, err := store.UpsertComputeNode(ctx, state.ComputeNode{Name: "agents-1", TargetURL: "tcp://agents-1:50051",
		VPCPUs: 20, MemMB: 64000, MaxConcurrency: 200, AdmissionCeilingMB: 47600, VCPUBudget: 40, Active: true}); err != nil {
		t.Fatal(err)
	}
	ledger := sched.NewNodeLedger()
	engine, err := sched.NewEngine(ctx, store, ledger, nil, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	router := &snapRouter{agentRouter: &agentRouter{ledger: ledger}}
	runner, err := sched.NewAgentRunner(engine, router, sched.AgentRunConfig{BaseKey: "base/agent-base.ext4", LayerKey: "layer/agent.ext4",
		SandboxLayerKey: "base/sandbox-layer-v1-amd64.ext4", Nodes: []string{"agents-1"}, SandboxSnapshots: on})
	if err != nil {
		t.Fatal(err)
	}
	return runner, router, ledger, store
}

const (
	build1 = "apps/sbt-0123456789ab/11111111-1111-4111-8111-111111111111.ext4"
	build2 = "apps/sbt-0123456789ab/22222222-2222-4222-8222-222222222222.ext4"
)

func TestSandboxWarmSnapshots(t *testing.T) {
	runner, router, ledger := snapshotFixture(t, true)
	ctx := context.Background()
	n := 0
	session := func(account, image string) sched.SandboxSpec {
		t.Helper()
		n++
		a := sandboxAdmission()
		a.SandboxID = "11111111-2222-3333-4444-00000000000" + string(rune('0'+n))
		a.AccountID, a.ImageKey = account, image
		if image != "" {
			a.StartCmd, a.ReadyCmd, a.ReadyTimeout = "serve", "probe", 5*time.Second
		}
		if err := runner.RunSandbox(ctx, a, nil, sched.SandboxHooks{}, make(chan sched.SandboxControl)); err != nil {
			t.Fatal(err)
		}
		return router.lastSpec()
	}
	captured := func(want int) []capture {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for len(router.captures()) < want && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		got := router.captures()
		if len(got) != want {
			t.Fatalf("%d captures, want %d", len(got), want)
		}
		// The capture's reservation is released once it ends.
		for ledger.ResidentFor(got[want-1].spec.Instance) && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		return got
	}

	if s := session("acct", ""); s.Snapshot != nil {
		t.Fatal("the first session restored a snapshot")
	}
	preset := captured(1)[0]
	if preset.namespace != "sandbox-preset" || preset.spec.StartCmd != "" || preset.spec.LayerKey != "base/sandbox-layer-v1-amd64.ext4" || !router.resident[0] {
		t.Fatalf("preset capture %+v resident %v", preset, router.resident)
	}
	if s := session("other", ""); s.Snapshot == nil {
		t.Fatal("another account's preset session did not restore the shared preset snapshot")
	}
	router.mu.Lock()
	router.failRestore = true
	router.mu.Unlock()
	failed := session("acct", "").Snapshot
	router.mu.Lock()
	router.failRestore = false
	router.mu.Unlock()
	// The failed session cold-booted, so it recaptures in the background
	// (under the slot's fixed keys); the next session may restore that new
	// capture, never the one that failed.
	if s := session("acct", ""); failed == nil || s.Snapshot == failed {
		t.Fatal("a snapshot that failed to restore was kept")
	}
	captured(2)

	session("acct", build1)
	tpl := captured(3)[2]
	if tpl.namespace != "sandbox-template-acct" || tpl.spec.ImageKey != build1 || tpl.spec.StartCmd != "" {
		t.Fatalf("template capture %+v", tpl)
	}
	if s := session("acct", build1); s.Snapshot == nil || s.StartCmd != "serve" {
		t.Fatalf("template session %+v", s)
	}
	if s := session("other", build1); s.Snapshot != nil {
		t.Fatal("another account restored the template's snapshot")
	}
	captured(4)
	if s := session("acct", build2); s.Snapshot != nil {
		t.Fatal("a rebuilt template restored its old build's snapshot")
	}
	if got := captured(5)[4]; got.captureID != tpl.captureID || got.spec.ImageKey != build2 {
		t.Fatalf("rebuild capture %+v, want the slot %s", got, tpl.captureID)
	}
	if s := session("acct", build2); s.Snapshot == nil {
		t.Fatal("the rebuilt template's new snapshot was not used")
	}
}

func TestSandboxWarmSnapshotsOff(t *testing.T) {
	runner, router, _ := snapshotFixture(t, false)
	for i := 0; i < 2; i++ {
		a := sandboxAdmission()
		if err := runner.RunSandbox(context.Background(), a, nil, sched.SandboxHooks{}, make(chan sched.SandboxControl)); err != nil {
			t.Fatal(err)
		}
		if router.lastSpec().Snapshot != nil {
			t.Fatal("a session restored with snapshots off")
		}
	}
	time.Sleep(50 * time.Millisecond)
	if len(router.captures()) != 0 {
		t.Fatal("captured with snapshots off")
	}
}

func TestSandboxTemplateSnapshotSweep(t *testing.T) {
	runner, router, ledger, store := snapshotFixtureWithStore(t, true)
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "sweep@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "sbt-0123456789ab", RAMMB: 256, SandboxTemplate: true})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	session := func(image string) sched.SandboxSpec {
		t.Helper()
		n++
		a := sandboxAdmission()
		a.SandboxID = "22222222-3333-4444-5555-00000000000" + string(rune('0'+n))
		a.AccountID, a.ImageKey = account.ID, image
		if err := runner.RunSandbox(ctx, a, nil, sched.SandboxHooks{}, make(chan sched.SandboxControl)); err != nil {
			t.Fatal(err)
		}
		return router.lastSpec()
	}
	captured := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for len(router.captures()) < want && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		got := router.captures()
		if len(got) != want {
			t.Fatalf("%d captures, want %d", len(got), want)
		}
		for ledger.ResidentFor(got[want-1].spec.Instance) && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
	}
	session("")
	captured(1)
	session(build1)
	captured(2)
	tplNode := router.captures()[1].node

	if dropped := runner.SweepSandboxTemplateSnapshots(ctx); dropped != 0 || len(router.deleted) != 0 {
		t.Fatalf("a live template's snapshot was swept: %d dropped, %v deleted", dropped, router.deleted)
	}
	if s := session(build1); s.Snapshot == nil {
		t.Fatal("the live template's session did not restore")
	}
	if _, err := store.ScheduleAppDeletion(ctx, app.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if dropped := runner.SweepSandboxTemplateSnapshots(ctx); dropped != 1 {
		t.Fatalf("%d slots dropped, want the deleted template's", dropped)
	}
	key := "snap/sandbox-template-" + account.ID + "/captures/"
	if len(router.deleted) != 1 {
		t.Fatalf("deleted %v, want the template's objects", router.deleted)
	}
	node, keys, _ := strings.Cut(router.deleted[0], " ")
	if node != tplNode || !strings.HasPrefix(keys, key) || !strings.HasSuffix(keys, "/v2/mem-state") {
		t.Fatalf("deleted %q, want the template's objects on its node %s", router.deleted[0], tplNode)
	}
	if s := session(""); s.Snapshot == nil {
		t.Fatal("the sweep dropped the preset snapshot")
	}
	if dropped := runner.SweepSandboxTemplateSnapshots(ctx); dropped != 0 || len(router.deleted) != 1 {
		t.Fatalf("a second sweep dropped %d, deleted %v", dropped, router.deleted)
	}
}
