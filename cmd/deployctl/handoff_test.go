package main

// adr: 20261018074719

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// fakeUnit stands in for systemd and the two binaries a hand-off decision
// probes.
type fakeUnit struct {
	mu            sync.Mutex
	pid           string
	status        string
	started       time.Time
	envFile       string
	oldCapable    bool
	newCapable    bool
	takeOverAfter int // shows after the reload before MainPID changes; -1 never
	failStatus    string
	reloads       int
	shows         int
}

func (f *fakeUnit) install(t *testing.T) {
	t.Helper()
	origOutput, origRun, origPoll := commandOutput, runCommand, handoffPollInterval
	t.Cleanup(func() { commandOutput, runCommand, handoffPollInterval = origOutput, origRun, origPoll })
	handoffPollInterval = time.Millisecond
	commandOutput = func(_ context.Context, name string, args ...string) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if name != "systemctl" || args[0] != "show" {
			return "", fmt.Errorf("unexpected %s %v", name, args)
		}
		if f.reloads > 0 {
			f.shows++
			if f.takeOverAfter >= 0 && f.shows > f.takeOverAfter {
				f.pid, f.status = "200", "handed off to 200"
			} else if f.failStatus != "" && f.shows > 1 {
				f.status = f.failStatus
			}
		}
		return strings.Join([]string{
			"ActiveState=active",
			"MainPID=" + f.pid,
			"NeedDaemonReload=no",
			"ExecReload={ path=/bin/kill ; argv[]=/bin/kill -USR2 $MAINPID ; }",
			"StatusText=" + f.status,
			fmt.Sprintf("ActiveEnterTimestamp=@%d", f.started.Unix()),
			"FragmentPath=",
			"DropInPaths=",
			"EnvironmentFiles=" + f.envFile + " (ignore_errors=yes)",
		}, "\n"), nil
	}
	runCommand = func(_ context.Context, name string, args ...string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case len(args) == 1 && args[0] == "-handoff-capable":
			capable := f.newCapable
			if strings.HasPrefix(name, procRoot) {
				capable = f.oldCapable
			}
			if !capable {
				return errors.New("exit status 2")
			}
			return nil
		case name == "systemctl" && args[0] == "reload":
			f.reloads++
			return nil
		}
		return fmt.Errorf("unexpected %s %v", name, args)
	}
}

func newFakeUnit(t *testing.T) *fakeUnit {
	t.Helper()
	env := filepath.Join(t.TempDir(), "gatewayd-internal.env")
	if err := os.WriteFile(env, []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(env, old, old); err != nil {
		t.Fatal(err)
	}
	return &fakeUnit{pid: "100", status: "ready", started: time.Now().Add(-time.Minute), envFile: env, oldCapable: true, newCapable: true}
}

func TestReloadByHandoffWhenNothingTheUnitReadChanged(t *testing.T) {
	f := newFakeUnit(t)
	f.takeOverAfter = 3
	f.install(t)
	r := hostRuntime{binaryDir: t.TempDir()}
	handedOff, reason, err := r.reloadByHandoff(context.Background(), "gatewayd-internal")
	if err != nil || !handedOff {
		t.Fatalf("reloadByHandoff = (%v, %q, %v), want a hand-off", handedOff, reason, err)
	}
	if f.reloads != 1 {
		t.Fatalf("reloads = %d, want 1", f.reloads)
	}
}

func TestReloadByHandoffFallsBackToRestart(t *testing.T) {
	cases := map[string]struct {
		tweak  func(t *testing.T, f *fakeUnit)
		reason string
		reload bool
	}{
		"environment file changed": {
			tweak: func(t *testing.T, f *fakeUnit) {
				now := time.Now()
				if err := os.Chtimes(f.envFile, now, now); err != nil {
					t.Fatal(err)
				}
			},
			reason: "changed since the unit started",
		},
		"running release predates hand-off": {
			tweak:  func(_ *testing.T, f *fakeUnit) { f.oldCapable = false },
			reason: "running binary cannot hand off",
		},
		"new release cannot hand off": {
			tweak:  func(_ *testing.T, f *fakeUnit) { f.newCapable = false },
			reason: "new binary cannot hand off",
		},
		"daemon refused the hand-off": {
			tweak: func(_ *testing.T, f *fakeUnit) {
				f.takeOverAfter = -1
				f.failStatus = wire.HandoffStatusFailed + " 2026-10-18T00:00:00Z: cgroup uses 300 of 512 bytes"
			},
			reason: wire.HandoffStatusFailed,
			reload: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeUnit(t)
			f.takeOverAfter = 3
			tc.tweak(t, f)
			f.install(t)
			r := hostRuntime{binaryDir: t.TempDir()}
			handedOff, reason, err := r.reloadByHandoff(context.Background(), "gatewayd-internal")
			if err != nil || handedOff {
				t.Fatalf("reloadByHandoff = (%v, %q, %v), want a restart", handedOff, reason, err)
			}
			if !strings.Contains(reason, tc.reason) {
				t.Fatalf("reason %q, want it to mention %q", reason, tc.reason)
			}
			if got := f.reloads > 0; got != tc.reload {
				t.Fatalf("reloaded = %v, want %v", got, tc.reload)
			}
		})
	}
}

func TestReloadByHandoffSkipsDaemonsThatDoNotHandOff(t *testing.T) {
	f := newFakeUnit(t)
	f.install(t)
	handedOff, reason, err := hostRuntime{}.reloadByHandoff(context.Background(), "schedd")
	if err != nil || handedOff || reason != "" || f.reloads != 0 {
		t.Fatalf("reloadByHandoff(schedd) = (%v, %q, %v), reloads %d; want a silent restart", handedOff, reason, err, f.reloads)
	}
}
