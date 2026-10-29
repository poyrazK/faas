// adr: 20261024225612 — a deployment keeps one live snapshot per tier per CPU
// platform; a second capture of the same platform still conflicts.
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestSnapshotCPUPlatform(t *testing.T) {
	for _, tc := range []struct{ template, model, want string }{
		{"T2CL", "cascadelake/x", "template:T2CL"},
		{"", "milan/AuthenticAMD-25-1-1/abc", "milan/AuthenticAMD-25-1-1/abc"},
		{"", "", ""},
	} {
		if got := state.SnapshotCPUPlatform(tc.template, tc.model); got != tc.want {
			t.Errorf("SnapshotCPUPlatform(%q, %q) = %q, want %q", tc.template, tc.model, got, tc.want)
		}
	}
}

func TestMemStoreKeepsOneLiveSnapshotPerCPUPlatform(t *testing.T) {
	ctx := context.Background()
	s := state.NewMemStore()
	acct, err := s.CreateAccount(ctx, "platform@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "platform-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{
		ID: "platform-deployment", AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:platform", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name, template, model string, at time.Time) (state.Snapshot, error) {
		return s.CreateSnapshot(ctx, state.Snapshot{
			DeploymentID: dep.ID, FCVersion: "1.17.0", Tier: state.SnapshotTierInit, CreatedAt: at,
			StorageKey:  state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, name),
			CPUTemplate: template, CPUModel: model,
		})
	}
	base := time.Now().Add(-time.Hour)
	intel, err := create("intel", "T2CL", "cascadelake/GenuineIntel-6-85-7/a", base)
	if err != nil {
		t.Fatal(err)
	}
	amd, err := create("amd", "", "milan/AuthenticAMD-25-1-1/b", base.Add(time.Minute))
	if err != nil {
		t.Fatalf("a second CPU platform must get its own live row: %v", err)
	}
	if _, err := create("intel-again", "T2CL", "icelake/GenuineIntel-6-106-6/c", base.Add(2*time.Minute)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("a second T2CL capture must conflict with the first, got %v", err)
	}
	rows, err := s.LiveSnapshotsForTier(ctx, dep.ID, state.SnapshotTierInit)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != amd.ID || rows[1].ID != intel.ID {
		t.Fatalf("live rows = %+v, want amd then intel", rows)
	}
	got, err := state.LiveSnapshotsForTier(ctx, s, dep.ID, state.SnapshotTierWarm)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty tier = %v, %v; want no rows and no error", got, err)
	}
}
