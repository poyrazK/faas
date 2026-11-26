package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Rollbacks that restore the database (ADR-20261023181931).

const rollbackSwitchesUsage = "usage: gregale rollback switches <slug> [--migration] [--json]"
const rollbackSwitchUsage = "usage: gregale release rollback status <slug> <switch-id> [--migration] [--cancel] [--wait] [--timeout 60m] [--poll-interval 2s] [--json]"

// databaseSwitchDefaultTimeout bounds waiting for a switch when --timeout is
// not given: the restore alone can take minutes.
const databaseSwitchDefaultTimeout = time.Hour

// cmdRollbackRestoreDatabases sends a rollback that restores the database,
// explains what it will do, asks for confirmation at a terminal, and follows
// the switch until it ends unless noWait is set.
func cmdRollbackRestoreDatabases(slug, to string, acknowledge, noWait bool, timeout, interval time.Duration) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	req := api.RollbackRequest{RestoreDatabases: true, AcknowledgeReleases: acknowledge}
	if to != "" {
		req.TargetDeploymentID = &to
	}
	resp, err := client.RollbackWithRequest(context.Background(), slug, req)
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Code == api.CodeRollbackCrossesRelease {
		if !jsonOutput {
			writeDatabaseSwitchPlan(osStderr, slug, apiErr.Problem)
		}
		if jsonOutput || nonInteractive || !stdinIsTTY() {
			return printErr("Rollback needs confirmation", errors.New("pass --acknowledge-releases to restore the database and roll back"))
		}
		if !requireTyped(slug) {
			return 1
		}
		req.AcknowledgeReleases = true
		resp, err = client.RollbackWithRequest(context.Background(), slug, req)
	}
	if err != nil {
		return printErr("Rollback failed", err)
	}
	if resp.DatabaseSwitch == nil {
		return printErr("Rollback failed", errors.New("the server did not start a database switch; it may predate restore_databases"))
	}
	sw := *resp.DatabaseSwitch
	if noWait {
		if jsonOutput {
			return jsonOut(writeJSON(sw))
		}
		PrintOK(osStdout, "Database switch %s started; follow it with: gregale release rollback status %s %s --wait", sw.ID, slug, sw.ID)
		return 0
	}
	return followDatabaseSwitch(client, slug, api.MigrationDatabaseSwitch{DatabaseSwitch: sw}, timeout, interval)
}

// writeDatabaseSwitchPlan explains a switch before it is confirmed: the
// releases it undoes, the restore, the writes left in the source database and
// the downtime.
func writeDatabaseSwitchPlan(w io.Writer, slug string, p api.Problem) {
	_, _ = fmt.Fprintln(w, "This rollback undoes the release commands that ran since its target's schema:")
	for _, r := range p.CrossedReleases {
		ref := r.DeploymentID
		if r.Revision > 0 {
			ref = fmt.Sprintf("v%d", r.Revision)
		}
		_, _ = fmt.Fprintf(w, "  %s  %s  started %s\n", ref, r.Status, r.StartedAt)
	}
	for _, rp := range p.RestorePoints {
		_, _ = fmt.Fprintf(w, "%s is restored to %s as a new database; writes to %s after that moment stay only in %s.\n",
			rp.DatabaseName, rp.PointInTime, rp.DatabaseName, rp.DatabaseName)
	}
	_, _ = fmt.Fprintf(w, "%s stops serving while it switches (usually under a minute, then a cold start).\n", slug)
	_, _ = fmt.Fprintln(w, "Pass --acknowledge-releases to restore the database and roll back without this prompt.")
}

// followDatabaseSwitch polls a switch, printing each phase, until it ends or
// the timeout passes. It exits 0 only for a completed switch.
func followDatabaseSwitch(client *api.Client, slug string, sw api.MigrationDatabaseSwitch, timeout, interval time.Duration) int {
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, timeout)
	defer cancel()
	migration := sw.MigrationOperationID != ""
	statusFlags := "--wait"
	if migration {
		statusFlags = "--migration --wait"
	}
	last := ""
	for {
		if !jsonOutput {
			if line := databaseSwitchLine(slug, sw.DatabaseSwitch); line != last {
				_, _ = fmt.Fprintln(osStdout, line)
				last = line
			}
		}
		if api.DatabaseSwitchFinished(sw.Phase) {
			return databaseSwitchOutcome(slug, sw)
		}
		select {
		case <-ctx.Done():
			if jsonOutput {
				_ = writeJSON(sw)
			}
			return printErr("Still switching", fmt.Errorf("switch %s is %s; follow it with: gregale release rollback status %s %s %s", sw.ID, sw.Phase, slug, sw.ID, statusFlags))
		case <-time.After(interval):
		}
		var next api.MigrationDatabaseSwitch
		var err error
		if migration {
			next, err = client.GetMigrationDatabaseSwitch(ctx, slug, sw.ID)
		} else {
			next.DatabaseSwitch, err = client.GetDatabaseSwitch(ctx, slug, sw.ID)
		}
		if err != nil && ctx.Err() == nil {
			return printErr("Read database switch failed", err)
		}
		if err == nil {
			sw = next
		}
	}
}

func databaseSwitchOutcome(slug string, sw api.MigrationDatabaseSwitch) int {
	if jsonOutput {
		if code := jsonOut(writeJSON(sw)); code != 0 || sw.Phase == "completed" {
			return code
		}
		return 1
	}
	switch sw.Phase {
	case "completed":
		if sw.MigrationOperationID != "" {
			PrintOK(osStdout, "%s now uses the native PostgreSQL database %s.", slug, sw.TargetName)
			return 0
		}
		if sw.KeepsRelease {
			PrintOK(osStdout, "Switched %s to %s. %s keeps every write and is no longer bound to %s; delete it when you no longer need it.",
				slug, sw.TargetName, databaseName(sw.DatabaseSwitch), slug)
			return 0
		}
		PrintOK(osStdout, "Rolled back to %s on %s. %s keeps every write and is no longer bound to %s; delete it when you no longer need it.",
			revisionRef(sw.TargetRevision, sw.TargetDeploymentID), sw.TargetName, databaseName(sw.DatabaseSwitch), slug)
		return 0
	case "cancelled":
		PrintWarn(osStderr, "Database switch cancelled; %s still serves %s on %s.", slug, revisionRef(sw.FromRevision, sw.FromDeploymentID), databaseName(sw.DatabaseSwitch))
	default:
		PrintWarn(osStderr, "Database switch failed (%s): %s %s still serves %s on %s.", sw.FailureCode, sw.FailureDetail, slug,
			revisionRef(sw.FromRevision, sw.FromDeploymentID), databaseName(sw.DatabaseSwitch))
	}
	if sw.TargetDatabaseID != "" && sw.MigrationOperationID == "" {
		PrintWarn(osStderr, "The restored copy %s was kept; delete it with gregale postgres delete %s if you do not need it.", sw.TargetName, sw.TargetDatabaseID)
	}
	return 1
}

// databaseSwitchLine describes a switch's current phase in one line.
func databaseSwitchLine(slug string, sw api.DatabaseSwitch) string {
	target := revisionRef(sw.TargetRevision, sw.TargetDeploymentID)
	switch sw.Phase {
	case "restoring":
		return fmt.Sprintf("restoring %s to %s as %s", databaseName(sw), sw.RestorePoint, sw.TargetName)
	case "staging", "verifying":
		return fmt.Sprintf("staging and verifying credentials for %s", sw.TargetName)
	case "fencing":
		return fmt.Sprintf("fencing %s; it stops serving until the switch ends", slug)
	case "settling":
		return "waiting for starting instances and running tasks to finish"
	case "draining":
		return fmt.Sprintf("draining %s: %d retired, %d parked, %d pending", slug, sw.Drain.Retired, sw.Drain.Parked, sw.Drain.Pending)
	case "switching":
		if sw.KeepsRelease {
			return fmt.Sprintf("switching %s to %s", slug, sw.TargetName)
		}
		return fmt.Sprintf("switching %s to %s and %s", slug, target, sw.TargetName)
	case "resuming":
		return fmt.Sprintf("resuming %s on %s", slug, target)
	case "aborting":
		return fmt.Sprintf("aborting (%s): %s", sw.FailureCode, sw.FailureDetail)
	}
	return sw.Phase
}

func revisionRef(revision int, id string) string {
	if revision > 0 {
		return fmt.Sprintf("v%d", revision)
	}
	if id == "" {
		return "the previous release"
	}
	return id
}

func databaseName(sw api.DatabaseSwitch) string {
	if sw.SourceDatabaseName != "" {
		return sw.SourceDatabaseName
	}
	return sw.SourceDatabaseID
}

// cmdRollbackSwitches lists an app's database switches.
func cmdRollbackSwitches(args []string) int {
	if hasHelpFlag(args) {
		PrintUsage(osStdout, rollbackSwitchesUsage, "rollback")
		return 0
	}
	fs := newFlagSet("rollback switches", flag.ContinueOnError)
	migration := fs.Bool("migration", false, "list native PostgreSQL migration switches")
	positionals, err := parseSwitchArgs(fs, args)
	if err != nil || len(positionals) != 1 {
		PrintUsage(os.Stderr, rollbackSwitchesUsage, "rollback")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var switches []api.DatabaseSwitch
	if *migration {
		rows, err := client.ListMigrationDatabaseSwitches(context.Background(), positionals[0])
		if err != nil {
			return printErr("List migration switches failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(api.MigrationDatabaseSwitchList{Items: rows}))
		}
		for _, row := range rows {
			switches = append(switches, row.DatabaseSwitch)
		}
	} else {
		switches, err = client.ListDatabaseSwitches(context.Background(), positionals[0])
		if err != nil {
			return printErr("List database switches failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(api.DatabaseSwitchList{Items: switches}))
		}
	}
	if len(switches) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No database switches.")
		return 0
	}
	for _, sw := range switches {
		outcome := sw.Phase
		if sw.FailureCode != "" {
			outcome += " (" + sw.FailureCode + ")"
		}
		_, _ = fmt.Fprintf(osStdout, "%s  %-22s  %s -> %s  %s -> %s  %s\n", sw.ID, outcome,
			revisionRef(sw.FromRevision, sw.FromDeploymentID), revisionRef(sw.TargetRevision, sw.TargetDeploymentID),
			databaseName(sw), sw.TargetName, sw.CreatedAt)
	}
	return 0
}

// cmdRollbackSwitch shows, follows or cancels one database switch.
func cmdRollbackSwitch(args []string) int {
	if hasHelpFlag(args) {
		PrintUsage(osStdout, rollbackSwitchUsage, "rollback")
		return 0
	}
	// The tree's `release rollback status` runs this command; with
	// --operation it reads a rollback operation, as `rollback status` does.
	if rollbackStatusNamesOperation(args) {
		return cmdRollbackStatus(args)
	}
	fs := newFlagSet("rollback switch", flag.ContinueOnError)
	cancelSwitch := fs.Bool("cancel", false, "cancel the switch")
	migration := fs.Bool("migration", false, "read a native PostgreSQL migration switch")
	wait := fs.Bool("wait", false, "follow the switch until it ends")
	timeout := fs.Duration("timeout", databaseSwitchDefaultTimeout, "wait deadline")
	interval := fs.Duration("poll-interval", 2*time.Second, "poll interval")
	positionals, err := parseSwitchArgs(fs, args)
	if err != nil || len(positionals) != 2 || *timeout <= 0 || *interval <= 0 {
		PrintUsage(os.Stderr, rollbackSwitchUsage, "rollback")
		return 1
	}
	slug, id := positionals[0], positionals[1]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var sw api.MigrationDatabaseSwitch
	if *cancelSwitch {
		if *migration {
			sw, err = client.CancelMigrationDatabaseSwitch(context.Background(), slug, id)
		} else {
			sw.DatabaseSwitch, err = client.CancelDatabaseSwitch(context.Background(), slug, id)
		}
		if err != nil {
			return printErr("Cancel failed", err)
		}
		if !jsonOutput && !*wait {
			PrintOK(osStdout, "Cancellation requested; the switch unwinds and %s keeps serving its current release.", slug)
			return 0
		}
	} else {
		if *migration {
			sw, err = client.GetMigrationDatabaseSwitch(context.Background(), slug, id)
		} else {
			sw.DatabaseSwitch, err = client.GetDatabaseSwitch(context.Background(), slug, id)
		}
		if err != nil {
			return printErr("Read database switch failed", err)
		}
	}
	if *wait {
		return followDatabaseSwitch(client, slug, sw, *timeout, *interval)
	}
	if jsonOutput {
		return jsonOut(writeJSON(sw))
	}
	writeDatabaseSwitch(osStdout, slug, sw)
	return 0
}

// parseSwitchArgs accepts positionals before and after the flags.
func parseSwitchArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		positionals, args = append(positionals, args[0]), args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return append(positionals, fs.Args()...), nil
}

func writeDatabaseSwitch(w io.Writer, slug string, sw api.MigrationDatabaseSwitch) {
	_, _ = fmt.Fprintf(w, "switch:        %s\n", sw.ID)
	_, _ = fmt.Fprintf(w, "phase:         %s\n", databaseSwitchLine(slug, sw.DatabaseSwitch))
	if sw.KeepsRelease {
		_, _ = fmt.Fprintf(w, "release:       %s (kept)\n", revisionRef(sw.TargetRevision, sw.TargetDeploymentID))
	} else {
		_, _ = fmt.Fprintf(w, "release:       %s -> %s\n", revisionRef(sw.FromRevision, sw.FromDeploymentID), revisionRef(sw.TargetRevision, sw.TargetDeploymentID))
	}
	if sw.MigrationOperationID != "" {
		_, _ = fmt.Fprintf(w, "database:      %s -> %s\n", databaseName(sw.DatabaseSwitch), sw.TargetName)
	} else {
		_, _ = fmt.Fprintf(w, "database:      %s -> %s (restored to %s)\n", databaseName(sw.DatabaseSwitch), sw.TargetName, sw.RestorePoint)
	}
	if !sw.KeepsRelease {
		_, _ = fmt.Fprintf(w, "undoes:        %s\n", strings.Join(sw.UndoneDeploymentIDs, ", "))
	}
	if sw.FailureCode != "" {
		_, _ = fmt.Fprintf(w, "failure:       %s: %s\n", sw.FailureCode, sw.FailureDetail)
	}
	if sw.LastErrorCode != "" {
		_, _ = fmt.Fprintf(w, "retrying:      %s\n", sw.LastErrorCode)
	}
	_, _ = fmt.Fprintf(w, "requested:     %s by %s\n", sw.CreatedAt, sw.RequestedBy)
	if sw.FinishedAt != "" {
		_, _ = fmt.Fprintf(w, "finished:      %s\n", sw.FinishedAt)
	}
}
