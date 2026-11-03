package state_test

// adr: 20261103131546

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type instanceHistoryTestStore interface {
	state.TerminalInstanceBatchDeleter
	state.InstancesByIDsReader
	state.AppInstancesInStatesReader
	CreateInstance(ctx context.Context, appID, deploymentID, state string, ramMB int, nodeID, wakeID string) (state.Instance, error)
	UpdateInstanceStateIf(ctx context.Context, id, expectedState, nextState string) error
	InstanceByID(ctx context.Context, id string) (state.Instance, error)
}

func TestMemInstanceHistoryReads(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	acct, err := m.CreateAccount(ctx, "history-"+uuid.NewString()[:8]+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "history-" + uuid.NewString()[:8], RAMMB: 128, MaxConcurrency: 4})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := m.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:history", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	testInstanceHistoryReads(t, m, app.ID, dep.ID, "")
}

// testInstanceHistoryReads checks the narrow instance reads and the batched
// terminal-row retention delete against one app's rows.
func testInstanceHistoryReads(t *testing.T, s instanceHistoryTestStore, appID, deploymentID, nodeID string) {
	t.Helper()
	ctx := context.Background()
	create := func(final state.State) string {
		t.Helper()
		ins, err := s.CreateInstance(ctx, appID, deploymentID, string(state.StateRunning), 128, nodeID, uuid.NewString())
		if err != nil {
			t.Fatalf("CreateInstance: %v", err)
		}
		if final != state.StateRunning {
			if err := s.UpdateInstanceStateIf(ctx, ins.ID, string(state.StateRunning), string(final)); err != nil {
				t.Fatalf("running -> %s: %v", final, err)
			}
		}
		return ins.ID
	}
	running := create(state.StateRunning)
	failedA := create(state.StateFailed)
	failedB := create(state.StateFailed)
	stopped := create(state.StateStopped)

	live, err := s.ListAppInstancesInStates(ctx, appID, []string{string(state.StateRunning)})
	if err != nil || len(live) != 1 || live[0].ID != running {
		t.Fatalf("ListAppInstancesInStates(running) = %+v (err %v), want only %s", live, err, running)
	}
	byID, err := s.InstancesByIDs(ctx, []string{running, failedA, uuid.NewString()})
	if err != nil || len(byID) != 2 || byID[running].State != string(state.StateRunning) || byID[failedA].State != string(state.StateFailed) {
		t.Fatalf("InstancesByIDs = %+v (err %v), want the running and failed rows only", byID, err)
	}

	terminal := []state.State{state.StateStopped, state.StateFailed}
	if n, err := s.DeleteTerminalInstancesOlderThan(ctx, terminal, time.Now().Add(-time.Hour), 10); err != nil || n != 0 {
		t.Fatalf("delete before the rows expired = %d (err %v), want 0", n, err)
	}
	later := time.Now().Add(time.Hour)
	if n, err := s.DeleteTerminalInstancesOlderThan(ctx, terminal, later, 2); err != nil || n != 2 {
		t.Fatalf("first batch deleted %d (err %v), want the limit of 2", n, err)
	}
	if n, err := s.DeleteTerminalInstancesOlderThan(ctx, terminal, later, 10); err != nil || n != 1 {
		t.Fatalf("second batch deleted %d (err %v), want the last terminal row", n, err)
	}
	for _, id := range []string{failedA, failedB, stopped} {
		if _, err := s.InstanceByID(ctx, id); err == nil {
			t.Fatalf("terminal row %s survived retention", id)
		}
	}
	if _, err := s.InstanceByID(ctx, running); err != nil {
		t.Fatalf("retention removed the running row: %v", err)
	}
}
