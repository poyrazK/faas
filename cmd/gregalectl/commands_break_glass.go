package main

// ADR 20261022140000: break-glass controls for a security incident. They
// write PostgreSQL directly from a control-plane node, so they work when
// apid is down or cannot be trusted.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

const dispatchBreakGlass = "break-glass"

const breakGlassActor = "gregalectl:break-glass"

func cmdBreakGlass(args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stderr, "usage: gregalectl break-glass <pause|resume|status|revoke-credentials> [flags]", dispatchBreakGlass)
		return 0
	}
	var err error
	switch args[0] {
	case "pause":
		err = runBreakGlassPause(args[1:], true)
	case "resume":
		err = runBreakGlassPause(args[1:], false)
	case "status":
		err = runBreakGlassStatus(args[1:])
	case "revoke-credentials":
		err = runBreakGlassRevoke(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl break-glass: unknown subcommand %q\n", args[0])
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl break-glass %s: %v\n", args[0], err)
		return 3
	}
	return 0
}

func breakGlassReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) < 3 || len(reason) > 400 {
		return "", errors.New("--reason must be 3..400 characters; it is recorded in the runtime configuration history and the audit log")
	}
	return reason, nil
}

// runBreakGlassPause turns the platform release pause on or off. apid and
// builderd follow the runtime configuration row within seconds.
func runBreakGlassPause(args []string, on bool) error {
	name := map[bool]string{true: "pause", false: "resume"}[on]
	fs := flag.NewFlagSet("break-glass "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	reason := fs.String("reason", "", "why (recorded)")
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	why, err := breakGlassReason(*reason)
	if err != nil {
		return err
	}
	pool, err := openFleetSealPool(*dsn, *dbEnv)
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := state.NewPgStore(pool)
	value, _ := json.Marshal(on)
	row, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: runtimeconfig.KeyPlatformReleasePause, Scope: state.RuntimeConfigScopeGlobal,
		DesiredValue: value, ApplyMode: state.RuntimeConfigApplyHot, Reason: "break-glass " + name + ": " + why,
	})
	if err != nil {
		return fmt.Errorf("write %s: %w", runtimeconfig.KeyPlatformReleasePause, err)
	}
	data, _ := json.Marshal(map[string]any{"paused": on, "reason": why, "version": row.Version})
	if err := store.AppendEvent(ctx, breakGlassActor, "break_glass.release_pause_set", nil, data); err != nil {
		return fmt.Errorf("record audit event: %w", err)
	}
	if *jsonOut || jsonOutput {
		return json.NewEncoder(osStdout).Encode(map[string]any{"paused": on, "version": row.Version})
	}
	if on {
		PrintOK(osStdout, "Releases paused platform-wide (version %d): new deployments refused, queued builds held, rollbacks allowed\n", row.Version)
	} else {
		PrintOK(osStdout, "Release pause lifted (version %d)\n", row.Version)
	}
	return nil
}

func runBreakGlassStatus(args []string) error {
	fs := flag.NewFlagSet("break-glass status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pool, err := openFleetSealPool(*dsn, *dbEnv)
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := state.NewPgStore(pool)
	status := map[string]any{"paused": false}
	row, err := store.GetRuntimeConfig(ctx, runtimeconfig.KeyPlatformReleasePause, state.RuntimeConfigScopeGlobal, "")
	switch {
	case errors.Is(err, state.ErrRuntimeConfigNotFound), errors.Is(err, state.ErrNotFound):
	case err != nil:
		return err
	default:
		paused, _ := runtimeconfig.Bool(row.DesiredValue)
		status = map[string]any{"paused": paused, "version": row.Version, "reason": row.Reason, "updated_at": row.UpdatedAt}
	}
	live, err := store.RevokeAllCredentials(ctx, state.BreakGlassCredentialKinds, "", "", true)
	if err != nil {
		return err
	}
	status["live_credentials"] = live
	if *jsonOut || jsonOutput {
		return json.NewEncoder(osStdout).Encode(status)
	}
	_, _ = fmt.Fprintf(osStdout, "release pause: %v\n", status["paused"])
	if r, ok := status["reason"]; ok {
		_, _ = fmt.Fprintf(osStdout, "  last change: %v (version %v, %v)\n", r, status["version"], status["updated_at"])
	}
	for _, c := range live {
		_, _ = fmt.Fprintf(osStdout, "live %s: %d\n", c.Kind, c.Count)
	}
	return nil
}

// runBreakGlassRevoke revokes every live control-plane credential of the
// chosen kinds in one transaction. Without --yes it only counts them.
func runBreakGlassRevoke(args []string) error {
	fs := flag.NewFlagSet("break-glass revoke-credentials", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	kinds := fs.String("kinds", strings.Join(state.BreakGlassCredentialKinds, ","), "comma-separated credential kinds: "+strings.Join(state.BreakGlassCredentialKinds, ", "))
	reason := fs.String("reason", "", "why (recorded)")
	yes := fs.Bool("yes", false, "revoke; without it the command only counts what it would revoke")
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	selected := splitBreakGlassKinds(*kinds)
	why := ""
	if *yes {
		var err error
		if why, err = breakGlassReason(*reason); err != nil {
			return err
		}
	}
	pool, err := openFleetSealPool(*dsn, *dbEnv)
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := state.NewPgStore(pool).RevokeAllCredentials(ctx, selected, breakGlassActor, why, !*yes)
	if err != nil {
		return err
	}
	if *jsonOut || jsonOutput {
		return json.NewEncoder(osStdout).Encode(map[string]any{"revoked": *yes, "credentials": out})
	}
	verb := "would revoke"
	if *yes {
		verb = "revoked"
	}
	for _, c := range out {
		_, _ = fmt.Fprintf(osStdout, "%s %d %s\n", verb, c.Count, c.Kind)
	}
	if !*yes {
		_, _ = fmt.Fprintln(osStdout, "dry run: pass --yes --reason \"...\" to revoke; everyone, operators included, signs in again")
	}
	return nil
}

func splitBreakGlassKinds(raw string) []string {
	var out []string
	for _, k := range strings.Split(raw, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}
