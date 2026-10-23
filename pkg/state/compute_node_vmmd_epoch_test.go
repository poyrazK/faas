package state

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

type vmmdEpochTestStore interface {
	ComputeNodeVMMDEpochStore
	CreateComputeNode(context.Context, ComputeNode) (ComputeNode, error)
}

// vmmdEpochLifecycle checks the claim protocol against any store.
func vmmdEpochLifecycle(t *testing.T, s vmmdEpochTestStore) {
	t.Helper()
	ctx := t.Context()
	n, err := s.CreateComputeNode(ctx, ComputeNode{Name: "vmmd-epoch-" + uuid.NewString(), TargetURL: "unix:///tmp/vmmd-epoch", VPCPUs: 2, MemMB: 1024, MaxConcurrency: 5, AdmissionCeilingMB: 1024, VCPUBudget: 2, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if e, err := s.ComputeNodeVMMDEpoch(ctx, n.ID); err != nil || e != (VMMDEpoch{}) {
		t.Fatalf("fresh node epoch = %+v, %v", e, err)
	}
	first, second := uuid.NewString(), uuid.NewString()
	if got, err := s.ClaimComputeNodeVMMDEpoch(ctx, n.ID, 0, first); err != nil || got != 1 {
		t.Fatalf("first claim = %d, %v", got, err)
	}
	if got, err := s.ClaimComputeNodeVMMDEpoch(ctx, n.ID, 1, second); err != nil || got != 2 {
		t.Fatalf("second claim = %d, %v", got, err)
	}
	if e, err := s.ComputeNodeVMMDEpoch(ctx, n.ID); err != nil || e != (VMMDEpoch{Epoch: 2, Incarnation: second, Predecessor: first}) {
		t.Fatalf("epoch after two claims = %+v, %v", e, err)
	}
	// A claim from a stale read never advances the node.
	if _, err := s.ClaimComputeNodeVMMDEpoch(ctx, n.ID, 1, uuid.NewString()); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale claim: %v", err)
	}
	// Racing claims from the same read: exactly one wins.
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ClaimComputeNodeVMMDEpoch(ctx, n.ID, 2, uuid.NewString()); err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrConflict) {
				t.Errorf("racing claim: %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("racing claims won %d times, want once", wins.Load())
	}
	for name, try := range map[string]func() error{
		"unknown node": func() error {
			_, err := s.ClaimComputeNodeVMMDEpoch(ctx, uuid.NewString(), 0, uuid.NewString())
			return err
		},
		"nil incarnation":   func() error { _, err := s.ClaimComputeNodeVMMDEpoch(ctx, n.ID, 3, uuid.Nil.String()); return err },
		"bad incarnation":   func() error { _, err := s.ClaimComputeNodeVMMDEpoch(ctx, n.ID, 3, "vmmd"); return err },
		"unknown node read": func() error { _, err := s.ComputeNodeVMMDEpoch(ctx, uuid.NewString()); return err },
	} {
		if err := try(); err == nil || errors.Is(err, ErrConflict) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestMemVMMDEpoch(t *testing.T) { vmmdEpochLifecycle(t, NewMemStore()) }

type adoptionReportInstanceTestStore = contractTestStore

// adoptionReportInstancesLifecycle checks the rows a vmmd adoption report
// settles: RUNNING and FROZEN on the node, nothing else.
func adoptionReportInstancesLifecycle(t *testing.T, s adoptionReportInstanceTestStore) {
	t.Helper()
	ctx := t.Context()
	owner, err := s.CreateAccountWithPersonalOrg(ctx, CreateAccountWithPersonalOrgParams{Email: "adoption-" + uuid.NewString() + "@example.com", Plan: api.PlanScale})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, Slug: "adoption-app", RAMMB: 128, MaxConcurrency: 20})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployLive, ImageDigest: "sha256:adoption-report"})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.CreateComputeNode(ctx, ComputeNode{Name: "adoption-" + uuid.NewString(), TargetURL: "unix:///tmp/adoption-vmmd", VPCPUs: 4, MemMB: 4096, MaxConcurrency: 20, AdmissionCeilingMB: 4096, VCPUBudget: 4, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateComputeNode(ctx, ComputeNode{Name: "adoption-other-" + uuid.NewString(), TargetURL: "unix:///tmp/adoption-other", VPCPUs: 4, MemMB: 4096, MaxConcurrency: 20, AdmissionCeilingMB: 4096, VCPUBudget: 4, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(st State, nodeID string) Instance {
		ins, err := s.CreateInstance(ctx, app.ID, dep.ID, string(StateRunning), 128, nodeID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		if st != StateRunning {
			if err := s.UpdateInstanceStateIf(ctx, ins.ID, string(StateRunning), string(st)); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.InstanceByID(ctx, ins.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	running, frozen := mk(StateRunning, node.ID), mk(StateFrozen, node.ID)
	mk(StateDraining, node.ID)
	mk(StateRunning, other.ID)
	got, err := s.ListAdoptionReportInstancesOnNode(ctx, node.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Instance{running.ID: running, frozen.ID: frozen}
	if len(got) != len(want) {
		t.Fatalf("listed %d rows, want %d: %+v", len(got), len(want), got)
	}
	for _, g := range got {
		w, ok := want[g.ID]
		if !ok || g.AppID != w.AppID || g.DeploymentID != w.DeploymentID || g.State != w.State || g.NodeID != node.ID ||
			g.WakeID != w.WakeID || !g.StartedAt.Equal(w.StartedAt) || g.Mode != w.Mode {
			t.Fatalf("row %+v, want %+v", g, w)
		}
	}
	if got, err := s.ListAdoptionReportInstancesOnNode(ctx, node.ID, 1); err != nil || len(got) != 1 {
		t.Fatalf("limit 1 = %d rows, %v", len(got), err)
	}
	if _, err := s.ListAdoptionReportInstancesOnNode(ctx, node.ID, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("zero limit: %v", err)
	}
}

func TestMemAdoptionReportInstances(t *testing.T) { adoptionReportInstancesLifecycle(t, NewMemStore()) }

type vmmdRestartNoticeTestStore interface {
	VMMDRestartNoticeStore
	CreateComputeNode(context.Context, ComputeNode) (ComputeNode, error)
}

func vmmdRestartNoticeLifecycle(t *testing.T, s vmmdRestartNoticeTestStore) {
	t.Helper()
	ctx := t.Context()
	n, err := s.CreateComputeNode(ctx, ComputeNode{Name: "vmmd-notice-" + uuid.NewString(), TargetURL: "unix:///tmp/vmmd-notice", VPCPUs: 2, MemMB: 1024, MaxConcurrency: 5, AdmissionCeilingMB: 1024, VCPUBudget: 2, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	active := func() (time.Time, bool) {
		t.Helper()
		m, err := s.ListComputeNodeVMMDRestartNotices(ctx)
		if err != nil {
			t.Fatal(err)
		}
		until, ok := m[n.ID]
		return until, ok
	}
	if _, ok := active(); ok {
		t.Fatal("a fresh node has a restart notice")
	}
	if err := s.SetComputeNodeVMMDRestartNotice(ctx, n.ID, 60); err != nil {
		t.Fatal(err)
	}
	if until, ok := active(); !ok || time.Until(until) < 50*time.Second || time.Until(until) > 70*time.Second {
		t.Fatalf("notice = %v, %v; want about 60 s from now", until, ok)
	}
	if err := s.ClearComputeNodeVMMDRestartNotice(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := active(); ok {
		t.Fatal("cleared notice still active")
	}
	if err := s.ClearComputeNodeVMMDRestartNotice(ctx, n.ID); err != nil {
		t.Fatalf("clearing twice: %v", err)
	}
	for name, err := range map[string]error{
		"unknown node": s.SetComputeNodeVMMDRestartNotice(ctx, uuid.NewString(), 60),
		"zero seconds": s.SetComputeNodeVMMDRestartNotice(ctx, n.ID, 0),
		"over an hour": s.SetComputeNodeVMMDRestartNotice(ctx, n.ID, 3601),
		"malformed id": s.SetComputeNodeVMMDRestartNotice(ctx, "node", 60),
	} {
		if err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestMemVMMDRestartNotice(t *testing.T) { vmmdRestartNoticeLifecycle(t, NewMemStore()) }
