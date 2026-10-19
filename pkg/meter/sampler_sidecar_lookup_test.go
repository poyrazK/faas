// spec: §4.7
// adr: 070
package meter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// sidecarLookupStore counts DeploymentSidecarRAMs reads and fails the ones
// for deployments in unreadable.
type sidecarLookupStore struct {
	MemStore   *state.MemStore
	reads      map[string]int
	unreadable map[string]bool
}

func (s *sidecarLookupStore) DeploymentSidecarRAMs(ctx context.Context, deploymentID string) ([]int, error) {
	s.reads[deploymentID]++
	if s.unreadable[deploymentID] {
		return nil, errors.New("sidecar column unreadable")
	}
	return s.MemStore.DeploymentSidecarRAMs(ctx, deploymentID)
}

// The sampler walks the app's whole retained wake history every minute.
// Sidecar RAM only prices billable rows, so deployments that left only
// parked history must not cost a read each, nor fail the minute.
func TestSampleAndRollReadsSidecarsOnlyForBillableDeployments(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "sidecars@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "sidecars", RAMMB: 256, Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	fake := &sidecarLookupStore{MemStore: mem, reads: map[string]int{}, unreadable: map[string]bool{}}
	for range 5 {
		old, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := mem.CreateInstance(ctx, app.ID, old.ID, string(state.StateParked), 256, state.DefaultLocalNodeName, ""); err != nil {
			t.Fatal(err)
		}
		fake.unreadable[old.ID] = true
	}
	live, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := mem.CreateInstance(ctx, app.ID, live.ID, string(state.StateRunning), 256, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}

	minute := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	rows, err := NewSampler(statetest.NewMem(fake), nil, func() time.Time { return minute }).SampleAndRoll(ctx)
	if err != nil {
		t.Fatalf("SampleAndRoll: %v", err)
	}
	billed := false
	for _, row := range rows {
		if row.InstanceID == ins.ID && row.MBSeconds > 0 {
			billed = true
		}
	}
	if !billed {
		t.Fatalf("the running instance was not billed: %+v", rows)
	}
	for id, n := range fake.reads {
		if id != live.ID {
			t.Errorf("read sidecars of parked-only deployment %s %d times", id, n)
		}
	}
}
