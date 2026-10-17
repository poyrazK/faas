package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
	"github.com/onebox-faas/faas/pkg/releasebundle"
)

// Per-unit releases (docs/adr/1650-per-unit-control-plane-releases.md):
// `deployctl deploy --only` restarts and gates only the services whose
// binaries the release replaced.

func manifestWithServices(services ...string) releasebundle.Manifest {
	manifest := releasebundle.Manifest{}
	for _, service := range services {
		manifest.Files = append(manifest.Files, releasebundle.File{Path: "systemd/faas-" + service + ".service"})
	}
	return manifest
}

func TestRestartServicesHonorsOnly(t *testing.T) {
	order, err := daemonunitspec.RestartOrder()
	if err != nil {
		t.Fatal(err)
	}
	manifest := manifestWithServices("apid", "schedd", "gatewayd-public", "meterd", "githubd", "outboundd")
	for _, tc := range []struct {
		name string
		only map[string]struct{}
		want []string
		err  string
	}{
		{name: "unset restarts the release", want: []string{"apid", "schedd", "gatewayd-public", "meterd", "githubd", "outboundd"}},
		{name: "subset keeps restart order", only: set("outboundd", "apid"), want: []string{"apid", "outboundd"}},
		{name: "service outside the release", only: set("apid", "vmmd"), err: "does not run: vmmd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := hostRuntime{serviceOrder: order, only: tc.only}
			got, err := r.restartServices(manifest)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("restartServices error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("restartServices: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("restartServices = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHostRestartWithOnlyLeavesOtherDaemonsRunning(t *testing.T) {
	orig := runCommand
	t.Cleanup(func() { runCommand = orig })
	var restarted, probed []string
	runCommand = func(_ context.Context, name string, args ...string) error {
		if name == "systemctl" && len(args) >= 2 && args[0] == "restart" {
			restarted = append(restarted, args[1])
		}
		return nil
	}
	binaryDir := t.TempDir()
	for _, service := range []string{"apid", "meterd", "schedd"} {
		if err := os.WriteFile(filepath.Join(binaryDir, service), []byte("candidate"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := hostRuntime{
		binaryDir:    binaryDir,
		serviceOrder: []string{"apid", "schedd", "meterd"},
		readyTimeout: 100 * time.Millisecond,
		only:         set("meterd"),
		waitReadyOverride: func(_ context.Context, service string) error {
			probed = append(probed, service)
			return nil
		},
	}
	if err := r.Restart(context.Background(), manifestWithServices("apid", "schedd", "meterd")); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if want := []string{"faas-meterd.service"}; !reflect.DeepEqual(restarted, want) {
		t.Fatalf("restarted %v, want %v", restarted, want)
	}
	if want := []string{"meterd"}; !reflect.DeepEqual(probed, want) {
		t.Fatalf("readiness probed %v, want %v", probed, want)
	}
}

func TestActivateWithOnlyRetiresOnlyRestartedCanaries(t *testing.T) {
	root := t.TempDir()
	releaseRoot := filepath.Join(root, "release")
	unitDir := filepath.Join(root, "installed")
	for _, service := range []string{"apid", "meterd"} {
		writeFile(t, filepath.Join(releaseRoot, "systemd", "faas-"+service+".service"), "[Unit]\n")
		writeFile(t, filepath.Join(unitDir, "faas-"+service+".service.d", "zz-canary.conf"),
			"[Service]\nExecStart=\nExecStart=/opt/faas/canaries/x/bin/"+service+"\n")
	}
	orig := runCommand
	t.Cleanup(func() { runCommand = orig })
	runCommand = func(context.Context, string, ...string) error { return nil }

	r := hostRuntime{unitDir: unitDir, only: set("apid")}
	if err := r.Activate(context.Background(), releaseRoot); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(unitDir, "faas-apid.service.d", "zz-canary.conf")); !os.IsNotExist(err) {
		t.Fatalf("restarted apid kept its canary override: %v", err)
	}
	if _, err := os.Stat(filepath.Join(unitDir, "faas-meterd.service.d", "zz-canary.conf")); err != nil {
		t.Fatalf("meterd is not restarted, so its canary override must stay: %v", err)
	}
}

func TestParseOnly(t *testing.T) {
	if got, err := parseOnly("", false); err != nil || got != nil {
		t.Fatalf("absent --only = %v, %v; want nil (every service)", got, err)
	}
	if got, err := parseOnly("", true); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty --only = %v, %v; want an empty set (no service)", got, err)
	}
	got, err := parseOnly("apid,meterd", true)
	if err != nil || !reflect.DeepEqual(got, set("apid", "meterd")) {
		t.Fatalf("parseOnly = %v, %v", got, err)
	}
	for _, bad := range []string{"apid,", "nope", "apid,,meterd"} {
		if _, err := parseOnly(bad, true); err == nil {
			t.Errorf("parseOnly(%q) accepted an unknown service", bad)
		}
	}
}

func TestEmptyOnlyRestartsNothing(t *testing.T) {
	order, err := daemonunitspec.RestartOrder()
	if err != nil {
		t.Fatal(err)
	}
	r := hostRuntime{serviceOrder: order, only: set()}
	got, err := r.restartServices(manifestWithServices("apid", "meterd"))
	if err != nil || len(got) != 0 {
		t.Fatalf("restartServices = %v, %v; want none", got, err)
	}
}

func set(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[name] = struct{}{}
	}
	return out
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
