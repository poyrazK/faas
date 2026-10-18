package main

// adr: 20261018133100

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// configHost fakes systemd for restart-changed: each unit's state and the
// files it reads, with modification times relative to its start.
type configHost struct {
	t       *testing.T
	dir     string
	started time.Time
	units   map[string]map[string]string // service -> systemctl show properties
	calls   []string
	failOn  string
}

func newConfigHost(t *testing.T) *configHost {
	return &configHost{t: t, dir: t.TempDir(), started: time.Now().Add(-time.Hour), units: map[string]map[string]string{}}
}

// file writes path under the host dir, older or newer than the units' start.
func (h *configHost) file(rel string, newer bool) string {
	h.t.Helper()
	path := filepath.Join(h.dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
			h.t.Fatal(err)
		}
	}
	h.touch(rel, newer)
	return path
}

func (h *configHost) touch(rel string, newer bool) {
	h.t.Helper()
	when := h.started.Add(-time.Hour)
	if newer {
		when = h.started.Add(time.Minute)
	}
	if err := os.Chtimes(filepath.Join(h.dir, rel), when, when); err != nil {
		h.t.Fatal(err)
	}
}

// unit registers an active service whose unit, drop-in directory, drop-in,
// environment file and --config file are all older than its start.
func (h *configHost) unit(service string) {
	fragment := h.file("systemd/faas-"+service+".service", false)
	dropIn := h.file("systemd/faas-"+service+".service.d/50-env.conf", false)
	h.touch("systemd/faas-"+service+".service.d", false)
	env := h.file("etc/"+service+".env", false)
	config := h.file("etc/"+service+".toml", false)
	h.units[service] = map[string]string{
		"ActiveState":          "active",
		"MainPID":              "100",
		"NeedDaemonReload":     "no",
		"ExecReload":           "",
		"StatusText":           "ready",
		"ActiveEnterTimestamp": fmt.Sprintf("@%d", h.started.Unix()),
		"FragmentPath":         fragment,
		"DropInPaths":          dropIn,
		"EnvironmentFiles":     env + " (ignore_errors=yes)",
		"ExecStart":            fmt.Sprintf("{ path=/opt/faas/current/bin/%s ; argv[]=/opt/faas/current/bin/%s --config %s ; ignore_errors=no ; }", service, service, config),
	}
}

func (h *configHost) install() hostRuntime {
	h.t.Helper()
	origOutput, origRun := commandOutput, runCommand
	h.t.Cleanup(func() { commandOutput, runCommand = origOutput, origRun })
	commandOutput = func(_ context.Context, name string, args ...string) (string, error) {
		if name != "systemctl" || args[0] != "show" {
			return "", fmt.Errorf("unexpected %s %v", name, args)
		}
		service := strings.TrimSuffix(strings.TrimPrefix(args[1], "faas-"), ".service")
		props, ok := h.units[service]
		if !ok {
			return "", fmt.Errorf("no unit %s", args[1])
		}
		var lines []string
		for i := 2; i < len(args); i++ {
			if args[i] == "-p" {
				lines = append(lines, args[i+1]+"="+props[args[i+1]])
			}
		}
		return strings.Join(lines, "\n"), nil
	}
	runCommand = func(_ context.Context, name string, args ...string) error {
		call := name + " " + strings.Join(args, " ")
		switch {
		case name == "chown" || name == "chmod":
			return nil
		case name == "systemctl" && (args[0] == "restart" || args[0] == "reload" || args[0] == "reset-failed"):
			if args[0] != "reset-failed" {
				h.calls = append(h.calls, args[0]+" "+args[1])
			}
			if h.failOn != "" && args[1] == "faas-"+h.failOn+".service" && args[0] == "restart" {
				return errors.New("exit status 1")
			}
			return nil
		}
		return fmt.Errorf("unexpected %s", call)
	}
	bin := filepath.Join(h.dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		h.t.Fatal(err)
	}
	for service := range h.units {
		if err := os.WriteFile(filepath.Join(bin, service), []byte("#!/bin/true\n"), 0o755); err != nil {
			h.t.Fatal(err)
		}
	}
	return hostRuntime{binaryDir: bin, waitReadyOverride: func(context.Context, string) error { return nil }}
}

func TestRestartChangedRestartsOnlyDaemonsWhoseStartInputsChanged(t *testing.T) {
	cases := []struct {
		name       string
		change     func(h *configHost)
		restarted  []string
		configOnly bool
	}{
		{name: "nothing changed", change: func(*configHost) {}},
		{name: "a drop-in changed", change: func(h *configHost) { h.touch("systemd/faas-meterd.service.d/50-env.conf", true) },
			restarted: []string{"meterd"}},
		{name: "a drop-in was removed", change: func(h *configHost) {
			if err := os.Remove(filepath.Join(h.dir, "systemd/faas-meterd.service.d/50-env.conf")); err != nil {
				t.Fatal(err)
			}
			h.units["meterd"]["DropInPaths"] = ""
			h.touch("systemd/faas-meterd.service.d", true)
		}, restarted: []string{"meterd"}},
		{name: "an environment file changed", change: func(h *configHost) { h.touch("etc/githubd.env", true) },
			restarted: []string{"githubd"}},
		{name: "the unit changed", change: func(h *configHost) { h.touch("systemd/faas-githubd.service", true) },
			restarted: []string{"githubd"}},
		{name: "a daemon-reload is pending", change: func(h *configHost) { h.units["meterd"]["NeedDaemonReload"] = "yes" },
			restarted: []string{"meterd"}},
		{name: "only the --config file changed", change: func(h *configHost) { h.touch("etc/meterd.toml", true) },
			restarted: []string{"meterd"}, configOnly: true},
		{name: "a stopped daemon stays stopped", change: func(h *configHost) {
			h.touch("etc/meterd.env", true)
			h.units["meterd"]["ActiveState"] = "inactive"
		}},
		{name: "every changed daemon, in restart order", change: func(h *configHost) {
			h.touch("etc/githubd.env", true)
			h.touch("etc/meterd.env", true)
		}, restarted: []string{"meterd", "githubd"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newConfigHost(t)
			h.unit("meterd")
			h.unit("githubd")
			tc.change(h)
			r := h.install()
			got, err := r.restartChanged(context.Background(), []string{"meterd", "githubd"}, false)
			if err != nil {
				t.Fatal(err)
			}
			var names, calls []string
			for _, d := range got {
				names = append(names, d.Service)
				if d.ConfigOnly != tc.configOnly {
					t.Errorf("%s config_only = %v, want %v (%s)", d.Service, d.ConfigOnly, tc.configOnly, d.Reason)
				}
				// meterd and githubd do not hand off, so every change restarts.
				calls = append(calls, "restart faas-"+d.Service+".service")
			}
			if !reflect.DeepEqual(names, tc.restarted) {
				t.Fatalf("restarted %v, want %v", names, tc.restarted)
			}
			if !reflect.DeepEqual(h.calls, calls) {
				t.Fatalf("systemctl calls %v, want %v", h.calls, calls)
			}
		})
	}
}

func TestRestartChangedDryRunAndFailureStopTheSequence(t *testing.T) {
	h := newConfigHost(t)
	h.unit("meterd")
	h.unit("githubd")
	h.touch("etc/githubd.env", true)
	h.touch("etc/meterd.env", true)
	r := h.install()
	got, err := r.restartChanged(context.Background(), []string{"meterd", "githubd"}, true)
	if err != nil || len(got) != 2 || len(h.calls) != 0 {
		t.Fatalf("dry run = %v, %v, calls %v; want two decisions and no restart", got, err, h.calls)
	}
	h.failOn = "meterd"
	got, err = r.restartChanged(context.Background(), []string{"meterd", "githubd"}, false)
	if err == nil || !strings.Contains(err.Error(), "restart meterd") {
		t.Fatalf("err = %v, want the meterd restart failure", err)
	}
	if len(got) != 1 || !reflect.DeepEqual(h.calls, []string{"restart faas-meterd.service"}) {
		t.Fatalf("after a failure nothing else may restart: decisions %v, calls %v", got, h.calls)
	}
}

func TestConfigFilesReadsExecStartArgv(t *testing.T) {
	cases := map[string][]string{
		"{ path=/opt/faas/current/bin/apid ; argv[]=/opt/faas/current/bin/apid --config /etc/faas/apid.toml ; ignore_errors=no ; }": {"/etc/faas/apid.toml"},
		"{ path=/x ; argv[]=/x -config=/etc/faas/x.toml ; }":                                                                        {"/etc/faas/x.toml"},
		"{ path=/x ; argv[]=/x --config=/a.toml --other y ; }":                                                                      {"/a.toml"},
		"{ path=/opt/faas/current/bin/bridged ; argv[]=/opt/faas/current/bin/bridged ; }":                                           nil,
		"": nil,
	}
	for in, want := range cases {
		if got := configFiles(in); !reflect.DeepEqual(got, want) {
			t.Errorf("configFiles(%q) = %v, want %v", in, got, want)
		}
	}
}
