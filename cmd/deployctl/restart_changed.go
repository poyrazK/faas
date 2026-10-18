package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/deploycontroller"
	"github.com/onebox-faas/faas/pkg/releasebundle"
)

// restart-changed (docs/adr/20261018133100-host-configuration-lane.md)
// applies a host-configuration change to the daemons it touched. The lane
// converges the changed control-plane roles with their FaaS daemon
// handlers deferred; this then restarts, one at a time in restart order and
// each to readiness, only the active daemons whose start inputs changed
// since they started: the unit, its drop-ins and drop-in directory, its
// environment files and credentials (what a hand-off would not pick up),
// and the --config file it reads. A daemon whose only change is its
// --config file hands off (ADR-1580); the others restart through their
// socket units. A stopped daemon stays stopped, as the roles' try-restart
// handlers leave it.

var restartChangedShowProperties = append(append([]string(nil), handoffShowProperties...), "ExecStart")

// restartDecision is one daemon's line in the report. A daemon whose only
// changed input is its --config file may hand off: the successor reads the
// file again, while it inherits the environment, credentials and sockets.
type restartDecision struct {
	Service    string `json:"service"`
	Reason     string `json:"reason"`
	ConfigOnly bool   `json:"config_only"`
}

func runRestartChanged(args []string) error {
	fs := flag.NewFlagSet("restart-changed", flag.ContinueOnError)
	only := fs.String("only", "", "comma-separated services to consider (default: every service of the active release)")
	dryRun := fs.Bool("dry-run", false, "report what would restart, restart nothing")
	jsonOut := fs.Bool("json", false, "print the report as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: deployctl restart-changed [--only a,b] [--dry-run] [--json]")
	}
	runtime := defaultHostRuntime()
	services, err := parseOnly(*only, flagSet(fs, "only"))
	if err != nil {
		return err
	}
	runtime.only = services
	lock, err := deploycontroller.Lock("/run/lock/faas-deploy.lock")
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	current, err := filepath.EvalSymlinks("/opt/faas/current")
	if err != nil {
		return fmt.Errorf("resolve active release: %w", err)
	}
	manifest, err := releasebundle.Read(current)
	if err != nil {
		return fmt.Errorf("read active release: %w", err)
	}
	order, err := runtime.restartServices(manifest)
	if err != nil {
		return err
	}
	restarted, err := runtime.restartChanged(context.Background(), order, *dryRun)
	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"restarted": restarted, "dry_run": *dryRun})
	} else {
		for _, d := range restarted {
			fmt.Printf("%s: %s\n", d.Service, d.Reason)
		}
	}
	return err
}

// restartChanged restarts each active daemon of order whose start inputs
// changed since it started, in order, stopping at the first failure. It
// returns the daemons it restarted (or would, with dryRun).
func (r hostRuntime) restartChanged(ctx context.Context, order []string, dryRun bool) ([]restartDecision, error) {
	var changed []restartDecision
	for _, service := range order {
		state, err := showUnit(ctx, service, restartChangedShowProperties...)
		if err != nil {
			return nil, fmt.Errorf("read %s unit state: %w", service, err)
		}
		if state.get("ActiveState") != "active" {
			continue
		}
		reason, configOnly, err := startInputsChanged(service, state)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			changed = append(changed, restartDecision{Service: service, Reason: reason, ConfigOnly: configOnly})
		}
	}
	if dryRun {
		return changed, nil
	}
	for i, d := range changed {
		fmt.Fprintf(os.Stderr, "deployctl: %s: %s\n", d.Service, d.Reason)
		if err := r.restartOne(ctx, d.Service, d.ConfigOnly); err != nil {
			return changed[:i+1], fmt.Errorf("restart %s: %w", d.Service, err)
		}
	}
	return changed, nil
}

// startInputsChanged says why a running daemon must restart to apply host
// configuration, or "" when nothing it read at start changed. configOnly
// reports that the only change is its --config file.
func startInputsChanged(service string, state unitState) (reason string, configOnly bool, err error) {
	if state.get("NeedDaemonReload") != "no" {
		return "unit file changed since the last daemon-reload", false, nil
	}
	started, err := parseUnixTimestamp(state.get("ActiveEnterTimestamp"))
	if err != nil {
		return "", false, fmt.Errorf("%s start time: %w", service, err)
	}
	paths := handoffInputs(service, state)
	if fragment := state.get("FragmentPath"); fragment != "" {
		// A removed drop-in leaves no file to stat, but moves its directory.
		paths = append(paths, fragment+".d")
	}
	systemd := len(paths)
	paths = append(paths, configFiles(state.get("ExecStart"))...)
	for i, path := range paths {
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", false, fmt.Errorf("stat %s: %w", path, err)
		}
		// Whole seconds: a file written in the start's second counts as new.
		if info.ModTime().Unix() >= started.Unix() {
			return path + " changed since the unit started", i >= systemd, nil
		}
	}
	return "", false, nil
}

// configFiles are the --config paths of a unit's ExecStart, as
// `systemctl show -p ExecStart` prints it:
// { path=/opt/faas/current/bin/apid ; argv[]=/opt/faas/current/bin/apid --config /etc/faas/apid.toml ; ... }
func configFiles(execStart string) []string {
	var out []string
	for _, part := range strings.Split(execStart, ";") {
		argv, ok := strings.CutPrefix(strings.TrimSpace(part), "argv[]=")
		if !ok {
			continue
		}
		fields := strings.Fields(argv)
		for i, field := range fields {
			switch {
			case (field == "--config" || field == "-config") && i+1 < len(fields):
				out = append(out, fields[i+1])
			case strings.HasPrefix(field, "--config="), strings.HasPrefix(field, "-config="):
				out = append(out, field[strings.Index(field, "=")+1:])
			}
		}
	}
	return out
}
