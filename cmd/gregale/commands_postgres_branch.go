package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdPostgresBranch is the customer's word for a restore from latest, with an
// optional TTL (ADR-20261106103654). A branch is an ordinary database whose
// restore_source_database_id names its source.
func cmdPostgresBranch(args []string) int {
	const usage = "usage: gregale postgres branch [create] <database> <name> [--from live|RFC3339] [--ttl DURATION]\n" +
		"       gregale postgres branch list <database>\n" +
		"       gregale postgres branch delete <database> <name> [--yes]"
	if len(args) == 0 {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdPostgresBranchList(args[1:])
	case "delete", "rm":
		return cmdPostgresBranchDelete(args[1:])
	case subCreate:
		return cmdPostgresBranchCreate(args[1:])
	default:
		if strings.HasPrefix(args[0], "-") {
			PrintUsage(os.Stderr, usage, "postgres")
			return 1
		}
		return cmdPostgresBranchCreate(args)
	}
}

func cmdPostgresBranchCreate(args []string) int {
	const usage = "usage: gregale postgres branch [create] <database> <name> [--from live|RFC3339] [--ttl DURATION]"
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("postgres branch", flag.ContinueOnError)
	from := fs.String("from", "live", "live (the latest point) or an RFC3339 time inside the source's restore range")
	ttl := fs.String("ttl", "", "purge the branch this long after it is created, e.g. 2h or 7d (5m to 30d)")
	if err := fs.Parse(append(flags, positional...)); err != nil {
		return 1
	}
	if fs.NArg() != 2 || strings.TrimSpace(fs.Arg(0)) == "" || strings.TrimSpace(fs.Arg(1)) == "" {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	request := api.RestoreManagedPostgresDatabaseRequest{Name: fs.Arg(1)}
	pointInTime, err := postgresBranchPoint(*from)
	if err != nil {
		printCommandValidation(os.Stderr, "%v\n", err)
		return 1
	}
	request.PointInTime = pointInTime
	if request.TTLSeconds, err = postgresBranchTTL(*ttl); err != nil {
		printCommandValidation(os.Stderr, "%v\n", err)
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	sourceID, err := postgresDatabaseID(context.Background(), client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	result, err := client.RestoreManagedPostgresDatabase(context.Background(), sourceID, request)
	if err != nil {
		return printErr("Could not branch managed PostgreSQL database", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderPostgresDatabase(osStdout, result)
	_, _ = fmt.Fprintf(osStdout, "Attach it with: gregale postgres attach %s <app> --env DATABASE_URL\n", result.Name)
	_, _ = fmt.Fprintf(osStdout, "Delete it with: gregale postgres branch delete %s %s --yes\n", fs.Arg(0), result.Name)
	return 0
}

// postgresBranchPoint maps --from to the request's point_in_time: empty for
// the latest point, which apid picks on its own clock.
func postgresBranchPoint(from string) (string, error) {
	from = strings.TrimSpace(from)
	switch strings.ToLower(from) {
	case "", "live", "latest", "now":
		return "", nil
	}
	if _, err := time.Parse(time.RFC3339Nano, from); err != nil {
		return "", fmt.Errorf("--from must be live or an RFC3339 time such as 2026-11-06T09:00:00Z, not %q", from)
	}
	return from, nil
}

// postgresBranchTTL parses --ttl as a Go duration or a whole number of days
// ("7d") into whole seconds, inside the API's bounds.
func postgresBranchTTL(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	var ttl time.Duration
	if days, ok := strings.CutSuffix(raw, "d"); ok {
		n, err := strconv.ParseInt(days, 10, 64)
		if err != nil || n < 1 || n > api.ManagedPostgresBranchTTLMaxSeconds/86400 {
			return 0, fmt.Errorf("--ttl %q is not a duration from 5m to 30d", raw)
		}
		ttl = time.Duration(n) * 24 * time.Hour
	} else {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return 0, fmt.Errorf("--ttl %q is not a duration from 5m to 30d (for example 90m, 2h or 7d)", raw)
		}
		ttl = parsed
	}
	if ttl%time.Second != 0 {
		return 0, fmt.Errorf("--ttl %q must be whole seconds", raw)
	}
	seconds := int64(ttl / time.Second)
	if seconds < api.ManagedPostgresBranchTTLMinSeconds || seconds > api.ManagedPostgresBranchTTLMaxSeconds {
		return 0, fmt.Errorf("--ttl %q is not a duration from 5m to 30d", raw)
	}
	return seconds, nil
}

func cmdPostgresBranchList(args []string) int {
	source, ok := onePostgresID("postgres branch list", args)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	sourceID, err := postgresDatabaseID(context.Background(), client, source)
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	all, err := client.ListManagedPostgresDatabases(context.Background())
	if err != nil {
		return printErr("Could not list managed PostgreSQL databases", err)
	}
	branches := postgresBranchesOf(all.Items, sourceID)
	if jsonOutput {
		return jsonOut(writeJSON(api.ManagedPostgresDatabaseList{Items: branches}))
	}
	renderPostgresBranches(osStdout, source, branches)
	return 0
}

// postgresBranchesOf keeps the undeleted databases restored from sourceID.
func postgresBranchesOf(databases []api.ManagedPostgresDatabase, sourceID string) []api.ManagedPostgresDatabase {
	branches := []api.ManagedPostgresDatabase{}
	for _, database := range databases {
		if database.RestoreSourceDatabaseID == sourceID && database.State != "deleted" {
			branches = append(branches, database)
		}
	}
	return branches
}

func renderPostgresBranches(w io.Writer, source string, branches []api.ManagedPostgresDatabase) {
	if len(branches) == 0 {
		_, _ = fmt.Fprintf(w, "No branches of %s.\n", source)
		return
	}
	_, _ = fmt.Fprintln(w, "NAME                 STATE         FROM                             EXPIRES")
	for _, branch := range branches {
		expires := branch.ExpiresAt
		if expires == "" {
			expires = "-"
		}
		_, _ = fmt.Fprintf(w, "%-20s %-13s %-32s %s\n", branch.Name, branch.State, branch.RestorePointInTime, expires)
	}
}

// cmdPostgresBranchDelete purges a branch. It refuses a database that was not
// restored from the named source, so a typo cannot purge another database.
func cmdPostgresBranchDelete(args []string) int {
	const usage = "usage: gregale postgres branch delete <database> <name> [--yes|--quiet]"
	flags, positional := splitArgsForFlags(args, "quiet", "yes")
	fs := newFlagSet("postgres branch delete", flag.ContinueOnError)
	quiet := fs.Bool("quiet", false, "skip the typed confirmation (for scripts)")
	fs.BoolVar(quiet, "yes", false, "confirm deletion without prompting")
	if err := fs.Parse(append(flags, positional...)); err != nil {
		return 1
	}
	if fs.NArg() != 2 || strings.TrimSpace(fs.Arg(0)) == "" || strings.TrimSpace(fs.Arg(1)) == "" {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	if code := requireAutomationConfirmation(*quiet, "--yes (or --quiet)"); code != 0 {
		return code
	}
	if !*quiet && jsonOutput {
		return printErr("Confirmation required", errors.New("postgres branch delete requires --yes or --quiet in JSON mode"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	sourceID, err := postgresDatabaseID(ctx, client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	branchID, err := postgresDatabaseID(ctx, client, fs.Arg(1))
	if err != nil {
		return printErr("Could not find branch", err)
	}
	branch, err := client.GetManagedPostgresDatabase(ctx, branchID)
	if err != nil {
		return printErr("Could not load branch", err)
	}
	if err := postgresBranchOf(branch, sourceID, fs.Arg(0)); err != nil {
		return printErr("Refusing to delete", err)
	}
	if !*quiet {
		_, _ = fmt.Fprintf(osStderr, "Delete branch %q (%s) of %s now? Data written to the branch is deleted and cannot be recovered; %s is not changed.\n",
			branch.Name, branch.ID, fs.Arg(0), fs.Arg(0))
		if !requireTyped(branch.Name) {
			return 130
		}
	}
	result, err := client.PurgeManagedPostgresDatabase(ctx, branch.ID)
	if err != nil {
		return printErr("Could not delete branch", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "Branch %s (%s) is %s.\n", branch.Name, branch.ID, result.State)
	return 0
}

func postgresBranchOf(branch api.ManagedPostgresDatabase, sourceID, sourceName string) error {
	if branch.RestoreSourceDatabaseID != sourceID {
		return fmt.Errorf("%s is not a branch of %s; delete other databases with gregale postgres delete", branch.Name, sourceName)
	}
	return nil
}
