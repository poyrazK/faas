package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/deploycontroller"
	"github.com/onebox-faas/faas/pkg/releasehistory"
)

func TestHostRuntimeKeepsTheReleaseHistory(t *testing.T) {
	var _ deploycontroller.ActivationRecorder = hostRuntime{}
	if deploycontroller.ActivationDeploy != releasehistory.KindDeploy || deploycontroller.ActivationRollback != releasehistory.KindRollback ||
		deploycontroller.ActivationSucceeded != releasehistory.StatusSucceeded || deploycontroller.ActivationFailed != releasehistory.StatusFailed ||
		deploycontroller.ActivationRolledBack != releasehistory.StatusRolledBack {
		t.Fatal("deploycontroller activation kinds and outcomes differ from the history's")
	}
}

func TestRecordsHistoryOnlyOnControlPlaneHosts(t *testing.T) {
	controlPlane := t.TempDir()
	if err := os.Mkdir(filepath.Join(controlPlane, "systemd"), 0o755); err != nil {
		t.Fatal(err)
	}
	compute := t.TempDir() // a compute release ships no units
	for _, tc := range []struct {
		name    string
		runtime hostRuntime
		root    string
		want    bool
	}{
		{name: "control plane", runtime: defaultHostRuntime(), root: controlPlane, want: true},
		{name: "compute", runtime: defaultHostRuntime(), root: compute},
		{name: "no database", runtime: hostRuntime{}, root: controlPlane},
	} {
		if got := tc.runtime.recordsHistory(deploycontroller.Activation{ReleaseRoot: tc.root}); got != tc.want {
			t.Errorf("%s: recordsHistory = %t, want %t", tc.name, got, tc.want)
		}
	}
}

func TestHistoryActivationNamesHostServicesAndActor(t *testing.T) {
	started := time.Date(2026, 10, 28, 14, 32, 0, 0, time.UTC)
	runtime := hostRuntime{only: map[string]struct{}{"schedd": {}, "apid": {}}}
	a := deploycontroller.Activation{Kind: deploycontroller.ActivationRollback, ReleaseID: "rel-a", CommitSHA: "abc", Previous: "rel-b", StartedAt: started}

	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{env: map[string]string{deployActorEnv: "cd-controlplane run 42", "SUDO_USER": "ops"}, want: "cd-controlplane run 42"},
		{env: map[string]string{"SUDO_USER": "ops", "USER": "root"}, want: "operator:ops"},
		{env: map[string]string{"USER": "root"}, want: "operator:root"},
		{want: "operator:unknown"},
	} {
		for _, name := range []string{deployActorEnv, "SUDO_USER", "USER", "LOGNAME"} {
			t.Setenv(name, tc.env[name])
		}
		got := runtime.historyActivation(a)
		if got.Actor != tc.want {
			t.Errorf("actor with %v = %q, want %q", tc.env, got.Actor, tc.want)
		}
		if got.Host == "" || got.Kind != releasehistory.KindRollback || got.ReleaseID != "rel-a" || got.CommitSHA != "abc" ||
			got.PreviousReleaseID != "rel-b" || !got.StartedAt.Equal(started) {
			t.Fatalf("activation = %+v", got)
		}
		if len(got.Services) != 2 || got.Services[0] != "apid" || got.Services[1] != "schedd" {
			t.Fatalf("services = %v, want [apid schedd]", got.Services)
		}
	}
	if every := (hostRuntime{}).historyActivation(a); every.Services != nil {
		t.Fatalf("services without --only = %v, want nil (every service)", every.Services)
	}
}

// An unreachable Postgres never blocks or fails an activation: the write
// gives up and only warns.
func TestHistoryWriteFailureOnlyWarns(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "systemd"), 0o755); err != nil {
		t.Fatal(err)
	}
	runtime := hostRuntime{databaseURL: "postgres:///faas?host=" + filepath.Join(t.TempDir(), "no-socket") + "&user=faas"}
	a := deploycontroller.Activation{Kind: deploycontroller.ActivationDeploy, ReleaseID: "rel-a", ReleaseRoot: root, StartedAt: time.Now().UTC()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the deploy's own context may already be done
	if id := runtime.StartActivation(ctx, a); id != "" {
		t.Fatalf("StartActivation = %q, want no id", id)
	}
	runtime.FinishActivation(ctx, "", a, deploycontroller.ActivationFailed, "boom")
}
