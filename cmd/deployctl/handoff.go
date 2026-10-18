package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/daemonunitspec"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Two-generation hand-off (docs/adr/1736-zero-downtime-daemon-restarts.md).
// A daemon whose unit reloads by hand-off is reloaded instead of restarted
// when nothing it read at start has changed: the successor inherits the old
// process's environment, credentials and sockets, so only a new binary can
// take effect through a reload. Anything else falls back to a restart.

// handoffPollInterval is how often a reload's progress is read.
var handoffPollInterval = 250 * time.Millisecond

// handoffWaitSlack covers the successor's exec and systemd's adoption on top
// of the readiness bound the daemon itself enforces.
const handoffWaitSlack = 30 * time.Second

// unitState is the subset of `systemctl show` a hand-off decision reads.
type unitState struct {
	props map[string][]string
}

func (s unitState) get(key string) string {
	if v := s.props[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

var handoffShowProperties = []string{
	"ActiveState", "MainPID", "NeedDaemonReload", "ExecReload", "StatusText",
	"ActiveEnterTimestamp", "FragmentPath", "DropInPaths", "EnvironmentFiles",
}

func showUnit(ctx context.Context, service string, props ...string) (unitState, error) {
	args := []string{"show", serviceFilePrefix + service + serviceFileSuffix, "--timestamp=unix"}
	for _, p := range props {
		args = append(args, "-p", p)
	}
	out, err := commandOutput(ctx, "systemctl", args...)
	if err != nil {
		return unitState{}, err
	}
	state := unitState{props: make(map[string][]string)}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			state.props[key] = append(state.props[key], value)
		}
	}
	return state, nil
}

// handoffBlocker says why service must be restarted rather than reloaded,
// or "" when a hand-off may run.
func (r hostRuntime) handoffBlocker(ctx context.Context, service string, state unitState) string {
	if !daemonunitspec.HandsOffOnReload(service) {
		return "unit does not hand off on reload"
	}
	if state.get("ActiveState") != "active" {
		return "unit is " + state.get("ActiveState")
	}
	pid := state.get("MainPID")
	if pid == "" || pid == "0" {
		return "unit has no main process"
	}
	if state.get("NeedDaemonReload") != "no" {
		return "unit file changed since the last daemon-reload"
	}
	if !strings.Contains(state.get("ExecReload"), "-USR2") {
		return "loaded unit has no hand-off ExecReload"
	}
	started, err := parseUnixTimestamp(state.get("ActiveEnterTimestamp"))
	if err != nil {
		return "unit start time unknown: " + err.Error()
	}
	for _, path := range handoffInputs(service, state) {
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Sprintf("stat %s: %v", path, err)
		}
		// Whole seconds: a file written in the start's second counts as new.
		if info.ModTime().Unix() >= started.Unix() {
			return path + " changed since the unit started"
		}
	}
	if err := runCommand(ctx, filepath.Join(procRoot, pid, "exe"), "-handoff-capable"); err != nil {
		return "running binary cannot hand off"
	}
	if err := runCommand(ctx, filepath.Join(r.binaryDir, service), "-handoff-capable"); err != nil {
		return "new binary cannot hand off"
	}
	return ""
}

// handoffInputs are the files systemd reads when it starts service: the unit,
// its drop-ins, environment files and credential sources.
func handoffInputs(service string, state unitState) []string {
	var paths []string
	if p := state.get("FragmentPath"); p != "" {
		paths = append(paths, p)
	}
	for _, v := range state.props["DropInPaths"] {
		paths = append(paths, strings.Fields(v)...)
	}
	for _, v := range state.props["EnvironmentFiles"] {
		// "/etc/faas/x.env (ignore_errors=yes)"
		if f := strings.Fields(v); len(f) > 0 {
			paths = append(paths, f[0])
		}
	}
	if u, err := daemonunitspec.UnitByName(service); err == nil {
		for _, c := range u.LoadCredential {
			paths = append(paths, c.Path)
		}
	}
	return paths
}

func parseUnixTimestamp(v string) (time.Time, error) {
	secs, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(v), "@"), 10, 64)
	if err != nil || secs <= 0 {
		return time.Time{}, fmt.Errorf("timestamp %q", v)
	}
	return time.Unix(secs, 0), nil
}

// reloadByHandoff reloads service and waits until its successor is the
// unit's main process. A false result with a nil error means the hand-off
// did not happen and the old generation still serves; the caller restarts.
func (r hostRuntime) reloadByHandoff(ctx context.Context, service string) (bool, string, error) {
	if !daemonunitspec.HandsOffOnReload(service) {
		return false, "", nil
	}
	state, err := showUnit(ctx, service, handoffShowProperties...)
	if err != nil {
		return false, "read unit state: " + err.Error(), nil
	}
	if reason := r.handoffBlocker(ctx, service, state); reason != "" {
		return false, reason, nil
	}
	oldPID, oldStatus := state.get("MainPID"), state.get("StatusText")
	if err := runCommand(ctx, "systemctl", "reload", serviceFilePrefix+service+serviceFileSuffix); err != nil {
		return false, "", err
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(api.HandoffReadyTimeoutSeconds)*time.Second+handoffWaitSlack)
	defer cancel()
	for {
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return false, "", ctx.Err()
			}
			return false, "successor did not take over in time", nil
		case <-time.After(handoffPollInterval):
		}
		now, err := showUnit(waitCtx, service, "ActiveState", "MainPID", "StatusText")
		if err != nil {
			continue
		}
		if pid := now.get("MainPID"); pid != oldPID && pid != "" && pid != "0" && now.get("ActiveState") == "active" {
			return true, "", nil
		}
		if status := now.get("StatusText"); status != oldStatus && strings.HasPrefix(status, wire.HandoffStatusFailed) {
			return false, status, nil
		}
	}
}
