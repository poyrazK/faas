package main

// Customer-facing managed PostgreSQL commands. These handlers intentionally
// use only the provider-neutral SDK DTOs: credentials and connection URLs are
// never returned by the API and therefore can never leak into CLI output.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgres(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale postgres <list|capabilities|usage|diagnostics|usage-import|reconcile|create|get|delete|resize|resize-status|compute-policy|compute-policy-status|restore|bindings|attach|cutover|live>", "postgres")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdPostgresList(args[1:])
	case "capabilities":
		return cmdPostgresCapabilities(args[1:])
	case "usage":
		return cmdPostgresUsage(args[1:])
	case "diagnostics":
		return cmdPostgresDiagnostics(args[1:])
	case "usage-import":
		return cmdPostgresUsageImport(args[1:])
	case "reconcile":
		return cmdPostgresReconcile(args[1:])
	case subCreate:
		return cmdPostgresCreate(args[1:])
	case subGet:
		return cmdPostgresGet(args[1:])
	case "delete":
		return cmdPostgresDelete(args[1:])
	case "compute-policy":
		return cmdPostgresComputePolicy(args[1:])
	case "compute-policy-status":
		return cmdPostgresComputePolicyStatus(args[1:])
	case "resize":
		return cmdPostgresResize(args[1:])
	case "resize-status":
		return cmdPostgresResizeStatus(args[1:])
	case subRestore:
		return cmdPostgresRestore(args[1:])
	case "cutover":
		return cmdPostgresCutover(args[1:])
	case "bindings":
		return cmdPostgresBindings(args[1:])
	case "attach", "bind":
		return cmdPostgresAttach(args[1:])
	case "live":
		return cmdPostgresLive(args[1:])
	default:
		printCommandValidation(os.Stderr, "unknown postgres subcommand %q\n", args[0])
		return unknownSubcommandChoices(os.Stderr, "postgres", nestedCliCommand("postgres"), args[0])
	}
}

// cmdPostgresAttach is the short, customer-facing path for connecting a
// logical database to an app. The lower-level `bindings create` command stays
// available for API-ID-oriented automation, while this command accepts the
// app slug and database name customers already use in their project.
func cmdPostgresAttach(args []string) int {
	args = normalizePostgresAttachArgs(args)
	fs := newFlagSet("postgres attach", flag.ContinueOnError)
	scope := fs.String("scope", "", "environment scope (defaults to linked project environment, otherwise default)")
	environmentKey := fs.String("env", "DATABASE_URL", "environment variable name")
	fs.Var(newStringAlias(environmentKey), "environment-key", "environment variable name")
	access := fs.String("access", "read_write", "backend-supported credential access: read_write|read_only|migration|data_api|auth")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	resolvedScope, resolveErr := resolveEnvironmentFlagOrContext(*scope)
	if resolveErr != nil {
		return printErr("Could not read local project context", resolveErr)
	}
	if resolvedScope == "" {
		resolvedScope = api.DefaultEnvScope
	}
	*scope = resolvedScope
	if invalidPostgresChoice("postgres attach", "access", *access) {
		return 1
	}
	if fs.NArg() != 2 || strings.TrimSpace(fs.Arg(0)) == "" || strings.TrimSpace(fs.Arg(1)) == "" ||
		api.ValidateScope(*scope) != nil || api.ValidateEnvKey(*environmentKey) != nil || !postgresAccessOK(*access) {
		PrintUsage(os.Stderr, "usage: gregale postgres attach DATABASE APP_SLUG [--scope SCOPE] [--env KEY] [--access read_write|read_only|migration|data_api|auth]", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	app, err := client.GetApp(context.Background(), fs.Arg(1))
	if err != nil {
		return printErr("Could not find app", err)
	}
	database, err := resolveManagedPostgresDatabase(context.Background(), client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	bindingIsNew := postgresBindingIsNew(context.Background(), client, database.ID, app.ID, *scope, *environmentKey)
	binding, err := client.CreateManagedPostgresBinding(context.Background(), database.ID, api.CreateManagedPostgresBindingRequest{
		AppID: app.ID, Scope: *scope, EnvironmentKey: *environmentKey, Access: *access,
	})
	if err != nil {
		return printErr("Could not attach managed PostgreSQL database", err)
	}
	ensurePostgresEgress(context.Background(), client, app)
	if jsonOutput {
		return jsonOut(writeJSON(binding))
	}
	renderPostgresBinding(osStdout, binding)
	if binding.State == "ready" {
		_, _ = fmt.Fprintf(osStdout, "Attached %s to app %s as %s.\n", database.Name, app.Slug, binding.EnvironmentKey)
		if bindingIsNew {
			printPostgresBindingDelivery(osStdout, app.Slug, binding.EnvironmentKey, binding.Access)
		}
	}
	return 0
}

// normalizePostgresAttachArgs permits both of the natural CLI forms:
// `attach DATABASE APP --scope production` and
// `attach --scope production DATABASE APP`.
func normalizePostgresAttachArgs(args []string) []string {
	var flags, positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if !strings.Contains(arg, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	return append(flags, positionals...)
}

// stringAlias lets --env and --environment-key share one destination while
// retaining the short flag in the primary user-facing syntax.
type stringAlias struct{ target *string }

func newStringAlias(target *string) *stringAlias { return &stringAlias{target: target} }

func (a *stringAlias) String() string {
	if a == nil || a.target == nil {
		return ""
	}
	return *a.target
}

func (a *stringAlias) Set(value string) error {
	if a == nil || a.target == nil {
		return fmt.Errorf("environment key alias is not initialized")
	}
	*a.target = value
	return nil
}

func cmdPostgresUsage(args []string) int {
	fs := newFlagSet("postgres usage", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale postgres usage", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.GetManagedPostgresUsage(context.Background())
	if err != nil {
		return printErr("Could not load managed PostgreSQL usage", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintln(osStdout, "managed postgres usage")
	_, _ = fmt.Fprintf(osStdout, "  period_start:                 %s\n", result.PeriodStart.UTC().Format("2006-01-02"))
	_, _ = fmt.Fprintf(osStdout, "  observed_at:                  %s\n", postgresUsageObservedLabel(result.ObservedAt, result.Fresh))
	_, _ = fmt.Fprintf(osStdout, "  policy_enabled:               %t\n", result.PolicyEnabled)
	_, _ = fmt.Fprintf(osStdout, "  fresh:                        %t\n", result.Fresh)
	_, _ = fmt.Fprintf(osStdout, "  guardrail_state:              %s\n", result.GuardrailState)
	_, _ = fmt.Fprintf(osStdout, "  ready_databases:              %d / %d\n", result.ReadyDatabases, result.DatabaseLimit)
	_, _ = fmt.Fprintf(osStdout, "  compute_unit_seconds:         %d (%s)\n", result.ComputeUnitSeconds, formatComputeUnitHours(result.ComputeUnitSeconds))
	_, _ = fmt.Fprintf(osStdout, "  storage_byte_seconds:         %d / %d (remaining %d)\n", result.StorageByteSeconds, result.StorageByteSecondsLimit, result.StorageByteSecondsRemaining)
	_, _ = fmt.Fprintf(osStdout, "                                (%s of %s used)\n", formatGiBMonths(result.StorageByteSeconds), formatGiBMonths(result.StorageByteSecondsLimit))
	_, _ = fmt.Fprintf(osStdout, "  history_byte_seconds:         %d (%s)\n", result.HistoryByteSeconds, formatGiBMonths(result.HistoryByteSeconds))
	_, _ = fmt.Fprintf(osStdout, "  egress_bytes:                 %d (%s)\n", result.EgressBytes, formatIECBytes(result.EgressBytes))
	return 0
}

// postgresBillingMonthSeconds matches the 31-day month apid uses to turn the
// plan storage entitlement into a byte-second ceiling, so that ceiling reads
// as a whole number of GiB-months.
const postgresBillingMonthSeconds = 31 * 24 * 60 * 60

// formatGiBMonths renders a byte-second meter readably; raw byte-seconds
// on the canary ran to nineteen digits. Small readings step down to
// GiB-hours or MiB-hours so a fresh database does not print 0.00.
func formatGiBMonths(byteSeconds int64) string {
	gibSeconds := float64(byteSeconds) / float64(int64(1)<<30)
	switch {
	case byteSeconds == 0 || gibSeconds/postgresBillingMonthSeconds >= 0.01:
		return fmt.Sprintf("%.2f GiB-months", gibSeconds/postgresBillingMonthSeconds)
	case gibSeconds/3600 >= 0.01:
		return fmt.Sprintf("%.2f GiB-hours", gibSeconds/3600)
	default:
		return fmt.Sprintf("%.2f MiB-hours", gibSeconds*1024/3600)
	}
}

func formatComputeUnitHours(seconds int64) string {
	return fmt.Sprintf("%.2f CU-hours", float64(seconds)/3600)
}

func formatIECBytes(value int64) string {
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	size, unit := float64(value)/1024, 0
	for size >= 1024 && unit < len(units)-1 {
		size, unit = size/1024, unit+1
	}
	return fmt.Sprintf("%.1f %s", size, units[unit])
}

// postgresUsageObservedLabel explains a missing observation on a fresh
// summary: fresh means no observation is overdue, which also holds before the
// first one, and "never" next to "fresh: true" read as a contradiction
// (Snapbox SB-12).
func postgresUsageObservedLabel(observedAt *time.Time, fresh bool) string {
	label := formatPostgresObservedAt(observedAt)
	if label == "never" && fresh {
		return "never (no usage metered yet; nothing is overdue)"
	}
	return label
}

func formatPostgresObservedAt(observedAt *time.Time) string {
	if observedAt == nil || observedAt.IsZero() {
		return "never"
	}
	return observedAt.UTC().Format(time.RFC3339)
}

func cmdPostgresList(args []string) int {
	fs := newFlagSet("postgres list", flag.ContinueOnError)
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
	fs := newFlagSet("postgres create", flag.ContinueOnError)
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
	if invalidPostgresChoice("postgres create", "class", *serviceClass, "availability", *availability) {
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
	id, err = postgresDatabaseID(context.Background(), client, id)
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
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
	id, err = postgresDatabaseID(context.Background(), client, id)
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
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
	fs := newFlagSet("postgres restore", flag.ContinueOnError)
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
	sourceID, err := postgresDatabaseID(context.Background(), client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	result, err := client.RestoreManagedPostgresDatabase(context.Background(), sourceID, api.RestoreManagedPostgresDatabaseRequest{Name: *name, PointInTime: *pointInTime})
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
		PrintUsage(os.Stderr, "usage: gregale postgres bindings <list|create|get|rotate|delete> ...", "postgres")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdPostgresBindingsList(args[1:])
	case subCreate:
		return cmdPostgresBindingsCreate(args[1:])
	case subGet:
		return cmdPostgresBindingsGet(args[1:])
	case "rotate":
		return cmdPostgresBindingsRotate(args[1:])
	case "delete":
		return cmdPostgresBindingsDelete(args[1:])
	default:
		printCommandValidation(os.Stderr, "unknown postgres bindings subcommand %q\n", args[0])
		return unknownSubcommandChoices(os.Stderr, "postgres bindings", nestedCliCommand("postgres", "bindings"), args[0])
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
	databaseID, err = postgresDatabaseID(context.Background(), client, databaseID)
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
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
	fs := newFlagSet("postgres bindings create", flag.ContinueOnError)
	app := fs.String("app", "", "app ID (required)")
	scope := fs.String("scope", "", "environment scope (required)")
	environmentKey := fs.String("environment-key", "", "environment variable name (required)")
	access := fs.String("access", "read_write", "backend-supported credential access: read_write|read_only|migration|data_api|auth")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if invalidPostgresChoice("postgres bindings create", "access", *access) {
		return 1
	}
	if fs.NArg() != 1 || *app == "" || *scope == "" || *environmentKey == "" || !postgresAccessOK(*access) {
		PrintUsage(os.Stderr, "usage: gregale postgres bindings create DATABASE_ID --app APP_ID --scope SCOPE --environment-key KEY [--access read_write|read_only|migration|data_api|auth]", "postgres")
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

func cmdPostgresBindingsRotate(args []string) int {
	usage := "usage: gregale postgres bindings rotate ID [--wait] [--wait-timeout DURATION] [--poll-interval DURATION]"
	positionals, waitOptions, ok := parseBindingRotationWaitArgs(args, "postgres bindings rotate", 1)
	if !ok {
		PrintUsage(osStderr, usage, "postgres")
		return 1
	}
	id := strings.TrimSpace(positionals[0])
	if id == "" {
		PrintUsage(osStderr, usage, "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	before, beforeErr := client.GetManagedPostgresBinding(context.Background(), id)
	result, err := client.RotateManagedPostgresBinding(context.Background(), id)
	if err != nil {
		adopted, ok := adoptRotationAfterUncertainError(context.Background(), client, err, id, before, beforeErr)
		if !ok {
			return printErr("Could not rotate PostgreSQL binding", err)
		}
		result = adopted
	} else if beforeErr == nil && before.RotationPending && result.CredentialGeneration == before.CredentialGeneration {
		PrintWarn(osStderr, "A rotation was already in progress (generation %d); this call joined it instead of starting another.", result.CredentialGeneration)
	}
	timedOut := false
	if waitOptions.Wait {
		result, timedOut, err = waitForBindingRotation(context.Background(), result, waitOptions,
			func(binding api.ManagedPostgresBinding) bool { return binding.RotationPending },
			func(ctx context.Context) (api.ManagedPostgresBinding, error) {
				return client.GetManagedPostgresBinding(ctx, id)
			},
		)
		if err != nil {
			return printErr("Could not check PostgreSQL binding rotation", err)
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else {
		renderPostgresBinding(osStdout, result)
	}
	if timedOut {
		PrintFail(osStderr, "timed out after %s waiting for PostgreSQL binding %s rotation", waitOptions.WaitTimeout, id)
		return 1
	}
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
	fs := newFlagSet(command, flag.ContinueOnError)
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

// postgresFlagVocab is the closed vocabulary of each enumerated postgres flag.
var postgresFlagVocab = map[string][]string{
	"class":        {"development", "burstable", "production"},
	"availability": {"single_zone", "high_availability"},
	"access":       {"read_write", "read_only", "migration", "data_api"},
}

// postgresVocab is a flag's vocabulary. The access mode of application Auth,
// a dark launch (unreleased.go), joins it only while Auth is turned on.
func postgresVocab(name string) []string {
	if name == "access" && applicationAuthCLI() {
		return append(slices.Clone(postgresFlagVocab[name]), "auth")
	}
	return postgresFlagVocab[name]
}

func postgresServiceClassOK(value string) bool {
	return slices.Contains(postgresVocab("class"), value)
}

func postgresAvailabilityOK(value string) bool {
	return slices.Contains(postgresVocab("availability"), value)
}

func postgresAccessOK(value string) bool {
	return slices.Contains(postgresVocab("access"), value)
}

// invalidPostgresChoice names the first enumerated flag whose value is outside
// its vocabulary and reports whether it found one. Pairs are flag name then
// value. A bare usage line left `--class prod` looking like a syntax error.
func invalidPostgresChoice(command string, flagValues ...string) bool {
	for i := 0; i+1 < len(flagValues); i += 2 {
		name, value := flagValues[i], flagValues[i+1]
		if vocab := postgresVocab(name); !slices.Contains(vocab, value) {
			printCommandValidation(os.Stderr, "gregale %s: invalid --%s %q (want %s)\n", command, name, value, strings.Join(vocab, "|"))
			return true
		}
	}
	return false
}

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
	if health := database.Health; health != nil {
		_, _ = fmt.Fprintf(w, "  provider_health:  %s (fresh=%t)\n", health.Status, health.Fresh)
		_, _ = fmt.Fprintf(w, "  compute_state:    %s\n", health.ComputeState)
		if health.CheckedAt != "" {
			_, _ = fmt.Fprintf(w, "  health_checked:   %s\n", health.CheckedAt)
		}
		if health.LastSuccessAt != "" {
			_, _ = fmt.Fprintf(w, "  health_success:   %s\n", health.LastSuccessAt)
		}
		if health.LastErrorCode != "" {
			_, _ = fmt.Fprintf(w, "  health_error:     %s\n", health.LastErrorCode)
		}
	}
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
	_, _ = fmt.Fprintf(w, "  rotation_pending:  %t\n", binding.RotationPending)
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

// adoptRotationAfterUncertainError recovers a rotation whose response was
// lost. On production-us `bindings rotate --wait` failed with "Could not reach
// Gregale" after 26.6 s while the rotation had committed and the running app
// had already switched to the new role (live test 2026-10-14). Only a
// server-side or transport failure is uncertain; a generation past the one
// read before the call is this rotation.
func adoptRotationAfterUncertainError(ctx context.Context, client interface {
	GetManagedPostgresBinding(context.Context, string) (api.ManagedPostgresBinding, error)
}, err error, id string, before api.ManagedPostgresBinding, beforeErr error) (api.ManagedPostgresBinding, bool) {
	var apiErr *APIError
	if beforeErr != nil || errors.As(err, &apiErr) && apiErr.Problem.Status < 500 {
		return api.ManagedPostgresBinding{}, false
	}
	now, getErr := client.GetManagedPostgresBinding(ctx, id)
	if getErr != nil || now.CredentialGeneration <= before.CredentialGeneration {
		return api.ManagedPostgresBinding{}, false
	}
	return now, true
}
