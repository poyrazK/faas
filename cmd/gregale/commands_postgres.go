package main

// Customer-facing managed PostgreSQL commands. These handlers intentionally
// use only the provider-neutral SDK DTOs: credentials and connection URLs are
// never returned by the API and therefore can never leak into CLI output.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgres(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale postgres <list|create|get|delete|restore|bindings>", "postgres")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdPostgresList(args[1:])
	case subCreate:
		return cmdPostgresCreate(args[1:])
	case subGet:
		return cmdPostgresGet(args[1:])
	case "delete":
		return cmdPostgresDelete(args[1:])
	case subRestore:
		return cmdPostgresRestore(args[1:])
	case "bindings":
		return cmdPostgresBindings(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown postgres subcommand %q\n", args[0])
		return 1
	}
}

func cmdPostgresList(args []string) int {
	fs := flag.NewFlagSet("postgres list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale postgres list", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListManagedPostgresDatabases(context.Background())
	if err != nil {
		return printErr("Could not list managed PostgreSQL databases", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No managed PostgreSQL databases.")
		return 0
	}
	_, _ = fmt.Fprintln(osStdout, "ID                                      NAME                 REGION           CLASS        STATE         STORAGE")
	for _, database := range resp.Items {
		_, _ = fmt.Fprintf(osStdout, "%-39s %-20s %-16s %-12s %-13s %s\n", database.ID, database.Name, database.Region, database.ServiceClass, database.State, formatPostgresStorage(database.StorageLimitBytes))
	}
	return 0
}

func cmdPostgresCreate(args []string) int {
	args = normalizePostgresArgs(args)
	fs := flag.NewFlagSet("postgres create", flag.ContinueOnError)
	region := fs.String("region", "", "provider-neutral region (required)")
	major := fs.Int("postgres-major", 16, "PostgreSQL major version")
	serviceClass := fs.String("class", "development", "service class: development|burstable|production")
	availability := fs.String("availability", "single_zone", "availability: single_zone|high_availability")
	scaleToZero := fs.Bool("scale-to-zero", true, "suspend compute when idle")
	storage := fs.Int64("storage-bytes", 0, "storage limit in bytes (0 uses plan allowance)")
	restoreWindow := fs.Int64("restore-window-seconds", 0, "point-in-time restore window (0 uses plan allowance)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *region == "" {
		PrintUsage(os.Stderr, "usage: gregale postgres create NAME --region REGION [--postgres-major N] [--class development|burstable|production] [--availability single_zone|high_availability] [--scale-to-zero[=BOOL]] [--storage-bytes N] [--restore-window-seconds N]", "postgres")
		return 1
	}
	if *storage < 0 || *restoreWindow < 0 || !postgresServiceClassOK(*serviceClass) || !postgresAvailabilityOK(*availability) {
		PrintUsage(os.Stderr, "usage: gregale postgres create NAME --region REGION [--postgres-major N] [--class development|burstable|production] [--availability single_zone|high_availability] [--scale-to-zero[=BOOL]] [--storage-bytes N] [--restore-window-seconds N]", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.CreateManagedPostgresDatabase(context.Background(), api.CreateManagedPostgresDatabaseRequest{
		Name: fs.Arg(0), Region: *region, PostgresMajor: *major, ServiceClass: *serviceClass,
		Availability: *availability, ScaleToZero: scaleToZero, StorageLimitBytes: *storage,
		RestoreWindowSeconds: *restoreWindow,
	})
	if err != nil {
		return printErr("Could not create managed PostgreSQL database", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderPostgresDatabase(osStdout, result)
	return 0
}

func cmdPostgresGet(args []string) int {
	id, ok := onePostgresID("postgres get", args)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.GetManagedPostgresDatabase(context.Background(), id)
	if err != nil {
		return printErr("Could not load managed PostgreSQL database", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderPostgresDatabase(osStdout, result)
	return 0
}

func cmdPostgresDelete(args []string) int {
	id, ok := onePostgresID("postgres delete", args)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.DeleteManagedPostgresDatabase(context.Background(), id)
	if err != nil {
		return printErr("Could not delete managed PostgreSQL database", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "Managed PostgreSQL database %s is %s.\n", result.ID, result.State)
	return 0
}

func cmdPostgresRestore(args []string) int {
	args = normalizePostgresArgs(args)
	fs := flag.NewFlagSet("postgres restore", flag.ContinueOnError)
	name := fs.String("name", "", "name for the restored database (required)")
	pointInTime := fs.String("point-in-time", "", "RFC3339 restore timestamp (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *name == "" || *pointInTime == "" {
		PrintUsage(os.Stderr, "usage: gregale postgres restore DATABASE_ID --name NAME --point-in-time RFC3339", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.RestoreManagedPostgresDatabase(context.Background(), fs.Arg(0), api.RestoreManagedPostgresDatabaseRequest{Name: *name, PointInTime: *pointInTime})
	if err != nil {
		return printErr("Could not restore managed PostgreSQL database", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderPostgresDatabase(osStdout, result)
	return 0
}

func cmdPostgresBindings(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale postgres bindings <list|create|get|delete> ...", "postgres")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdPostgresBindingsList(args[1:])
	case subCreate:
		return cmdPostgresBindingsCreate(args[1:])
	case subGet:
		return cmdPostgresBindingsGet(args[1:])
	case "delete":
		return cmdPostgresBindingsDelete(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown postgres bindings subcommand %q\n", args[0])
		return 1
	}
}

func cmdPostgresBindingsList(args []string) int {
	databaseID, ok := onePostgresID("postgres bindings list", args)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.ListManagedPostgresBindings(context.Background(), databaseID)
	if err != nil {
		return printErr("Could not list PostgreSQL bindings", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No bindings for this database.")
		return 0
	}
	for _, binding := range resp.Items {
		renderPostgresBinding(osStdout, binding)
	}
	return 0
}

func cmdPostgresBindingsCreate(args []string) int {
	args = normalizePostgresArgs(args)
	fs := flag.NewFlagSet("postgres bindings create", flag.ContinueOnError)
	app := fs.String("app", "", "app ID (required)")
	scope := fs.String("scope", "", "environment scope (required)")
	environmentKey := fs.String("environment-key", "", "environment variable name (required)")
	access := fs.String("access", "read_write", "credential access: read_write|read_only")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *app == "" || *scope == "" || *environmentKey == "" || !postgresAccessOK(*access) {
		PrintUsage(os.Stderr, "usage: gregale postgres bindings create DATABASE_ID --app APP_ID --scope SCOPE --environment-key KEY [--access read_write|read_only]", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.CreateManagedPostgresBinding(context.Background(), fs.Arg(0), api.CreateManagedPostgresBindingRequest{AppID: *app, Scope: *scope, EnvironmentKey: *environmentKey, Access: *access})
	if err != nil {
		return printErr("Could not create PostgreSQL binding", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderPostgresBinding(osStdout, result)
	return 0
}

func cmdPostgresBindingsGet(args []string) int {
	id, ok := onePostgresID("postgres bindings get", args)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.GetManagedPostgresBinding(context.Background(), id)
	if err != nil {
		return printErr("Could not load PostgreSQL binding", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderPostgresBinding(osStdout, result)
	return 0
}

func cmdPostgresBindingsDelete(args []string) int {
	id, ok := onePostgresID("postgres bindings delete", args)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.DeleteManagedPostgresBinding(context.Background(), id)
	if err != nil {
		return printErr("Could not delete PostgreSQL binding", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "PostgreSQL binding %s is %s.\n", result.ID, result.State)
	return 0
}

func onePostgresID(command string, args []string) (string, bool) {
	args = normalizePostgresArgs(args)
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		PrintUsage(os.Stderr, "usage: gregale "+strings.ReplaceAll(command, " ", " ")+" ID", "postgres")
		return "", false
	}
	return fs.Arg(0), true
}

// normalizePostgresArgs lets customers put the required resource name/ID
// before flags (the conventional `gregale postgres create NAME --region eu`)
// while still using the standard Go flag parser underneath.
func normalizePostgresArgs(args []string) []string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return args
	}
	return append(append([]string(nil), args[1:]...), args[0])
}

func postgresServiceClassOK(value string) bool {
	switch value {
	case "development", "burstable", "production":
		return true
	default:
		return false
	}
}

func postgresAvailabilityOK(value string) bool {
	return value == "single_zone" || value == "high_availability"
}

func postgresAccessOK(value string) bool { return value == "read_write" || value == "read_only" }

func formatPostgresStorage(bytes int64) string {
	if bytes <= 0 {
		return "0 B"
	}
	const gib = int64(1 << 30)
	if bytes%gib == 0 {
		return strconv.FormatInt(bytes/gib, 10) + " GiB"
	}
	return strconv.FormatInt(bytes, 10) + " B"
}

func renderPostgresDatabase(w io.Writer, database api.ManagedPostgresDatabase) {
	_, _ = fmt.Fprintf(w, "postgres database %s\n", database.ID)
	_, _ = fmt.Fprintf(w, "  name:             %s\n", database.Name)
	_, _ = fmt.Fprintf(w, "  region:           %s\n", database.Region)
	_, _ = fmt.Fprintf(w, "  postgres_major:   %d\n", database.PostgresMajor)
	_, _ = fmt.Fprintf(w, "  class:            %s\n", database.ServiceClass)
	_, _ = fmt.Fprintf(w, "  availability:     %s\n", database.Availability)
	_, _ = fmt.Fprintf(w, "  scale_to_zero:    %t\n", database.ScaleToZero)
	_, _ = fmt.Fprintf(w, "  storage_limit:    %s\n", formatPostgresStorage(database.StorageLimitBytes))
	_, _ = fmt.Fprintf(w, "  restore_window:   %s\n", formatPostgresDuration(database.RestoreWindowSeconds))
	_, _ = fmt.Fprintf(w, "  state:            %s\n", database.State)
	if database.LastErrorCode != "" {
		_, _ = fmt.Fprintf(w, "  last_error_code:  %s\n", database.LastErrorCode)
	}
	_, _ = fmt.Fprintf(w, "  created_at:       %s\n", database.CreatedAt)
	_, _ = fmt.Fprintf(w, "  updated_at:       %s\n", database.UpdatedAt)
}

func renderPostgresBinding(w io.Writer, binding api.ManagedPostgresBinding) {
	_, _ = fmt.Fprintf(w, "binding %s\n", binding.ID)
	_, _ = fmt.Fprintf(w, "  database_id:      %s\n", binding.DatabaseID)
	_, _ = fmt.Fprintf(w, "  app_id:            %s\n", binding.AppID)
	_, _ = fmt.Fprintf(w, "  scope:             %s\n", binding.Scope)
	_, _ = fmt.Fprintf(w, "  environment_key:   %s\n", binding.EnvironmentKey)
	_, _ = fmt.Fprintf(w, "  access:            %s\n", binding.Access)
	_, _ = fmt.Fprintf(w, "  credential_generation: %d\n", binding.CredentialGeneration)
	_, _ = fmt.Fprintf(w, "  state:             %s\n", binding.State)
	if binding.LastErrorCode != "" {
		_, _ = fmt.Fprintf(w, "  last_error_code:   %s\n", binding.LastErrorCode)
	}
}

func formatPostgresDuration(seconds int64) string {
	if seconds <= 0 {
		return "0s"
	}
	const day = int64(24 * 60 * 60)
	if seconds%day == 0 {
		return strconv.FormatInt(seconds/day, 10) + "d"
	}
	return strconv.FormatInt(seconds, 10) + "s"
}
