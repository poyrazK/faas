package main

// adr: 20261024085817

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The rc.290 rollout's drain wait printed only "instances still on
// fsn-3.faas: 1"; the operator had to find the blocking app by hand. The
// break-glass drain-status the rollout polls now names every live instance.
func TestDrainStatusNamesTheBlockingInstances(t *testing.T) {
	cases := []struct {
		name      string
		states    []state.State
		wantCode  int
		wantLines []string
		absent    []string
	}{
		{name: "no live instance waits for the maintenance hold", states: []state.State{state.StateParked},
			wantCode: 1, wantLines: []string{"waiting for maintenance hold"}, absent: []string{"instances still on"}},
		{name: "one busy instance is named", states: []state.State{state.StateRunning, state.StateParked},
			wantCode: 1, wantLines: []string{"instances still on default-local: 1", "app=h12-py", "state=running"}},
		{name: "frozen and warm instances block too", states: []state.State{state.StateFrozen, state.StateWarm},
			wantCode: 1, wantLines: []string{"instances still on default-local: 2", "state=frozen", "state=warm"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := state.NewMemStore()
			setComputeNodesStoreOpener(func() (state.Store, func(), error) { return st, func() {}, nil })
			ctx := t.Context()
			node, err := st.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			acct, err := st.CreateAccount(ctx, "drain@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := st.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "h12-py", Type: state.AppTypeFunction, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := st.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:drain", Status: state.DeployLive})
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, s := range tc.states {
				ins, err := st.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(s), app.RAMMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
				if err != nil {
					t.Fatal(err)
				}
				if state.IsLive(string(s)) {
					ids = append(ids, ins.ID)
				}
			}
			buf, restore := captureOsStdoutComputeNodes(t)
			code := cmdComputeNodesDrainStatus([]string{"--node", state.DefaultLocalNodeName, "--break-glass-db"})
			restore()
			out := string(buf.data)
			if code != tc.wantCode {
				t.Fatalf("drain-status = %d, want %d; output:\n%s", code, tc.wantCode, out)
			}
			for _, want := range append(tc.wantLines, ids...) {
				if !strings.Contains(out, want) {
					t.Errorf("drain-status output missing %q:\n%s", want, out)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(out, absent) {
					t.Errorf("drain-status output has %q:\n%s", absent, out)
				}
			}
		})
	}
}

func TestDrainStatusCapsTheBlockerList(t *testing.T) {
	st := state.NewMemStore()
	setComputeNodesStoreOpener(func() (state.Store, func(), error) { return st, func() {}, nil })
	ctx := t.Context()
	node, err := st.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	acct, err := st.CreateAccount(ctx, "cap@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	total := drainBlockerLimit + 3
	for i := range total {
		app, err := st.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: fmt.Sprintf("app-%02d", i), Type: state.AppTypeFunction, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := st.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:cap", Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateRunning), app.RAMMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal)); err != nil {
			t.Fatal(err)
		}
	}
	buf, restore := captureOsStdoutComputeNodes(t)
	code := cmdComputeNodesDrainStatus([]string{"--node", state.DefaultLocalNodeName, "--break-glass-db"})
	restore()
	out := string(buf.data)
	if code != 1 || strings.Count(out, "  app=") != drainBlockerLimit || !strings.Contains(out, "... and 3 more") {
		t.Fatalf("drain-status = %d; want %d named instances and a remainder line:\n%s", code, drainBlockerLimit, out)
	}
}
