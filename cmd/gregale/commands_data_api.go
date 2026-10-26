package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dataapi/clientgen"
)

func cmdDataAPI(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale data-api <create|deploy|configure|server-key|usage|live|storage|types|diff|check|refresh|sync|status|rotation|health|replay|diagnostics|dev|install|onboard|migration|fixture|explain|verify|transaction>", "data-api")
		return 1
	}
	switch args[0] {
	case "rotation":
		return cmdDataAPIRotation(args[1:])
	case "health":
		return cmdDataAPIHealth(args[1:])
	case "transaction":
		return cmdDataAPITransaction(args[1:])
	case "verify":
		return cmdDataAPIVerify(args[1:])
	case "explain":
		return cmdDataAPIExplain(args[1:])
	case "fixture":
		return cmdDataAPIFixture(args[1:])
	case "migration":
		return cmdDataAPIMigration(args[1:])
	case "onboard":
		return cmdDataAPIOnboard(args[1:])
	case "install":
		return cmdDataAPIInstall(args[1:])
	case "dev":
		return cmdDataAPIDev(args[1:])
	case "create":
		return cmdDataAPICreate(args[1:])
	case "types":
		return cmdDataAPITypes(args[1:])
	case "check":
		return cmdDataAPICheck(args[1:])
	case "diff":
		return cmdDataAPIDiff(args[1:])
	case "refresh":
		return cmdDataAPIRefresh(args[1:])
	case "replay":
		return cmdDataAPIReplay(args[1:])
	case "diagnostics":
		return cmdDataAPIDiagnostics(args[1:])
	case "status":
		return cmdDataAPIStatus(args[1:])
	case "configure":
		return cmdDataAPIConfigure(args[1:])
	case "server-key":
		return cmdDataAPIServerKey(args[1:])
	case "usage":
		return cmdDataAPIUsage(args[1:])
	case "live":
		return cmdDataAPILive(args[1:])
	case "storage":
		return cmdDataAPIStorage(args[1:])
	case "deploy":
		return cmdDataAPIDeploy(args[1:])
	case "sync":
		return cmdDataAPISync(args[1:])
	default:
		return printErr("Unknown Data API command", fmt.Errorf("use create, deploy, configure, server-key, usage, live, storage, types, diff, refresh, sync, status, rotation, health, replay, diagnostics, check, dev, install, onboard, migration, fixture, explain, verify or transaction"))
	}
}

// A snapshot restart would preserve PostgREST's stale schema cache. Use the
// existing fresh-restart lifecycle to rebuild it on every active instance.
func cmdDataAPIRefresh(args []string) int {
	fs := newFlagSet("data-api refresh", flag.ContinueOnError)
	wait := fs.Bool("wait", false, "wait for fresh-restart completion and Data API readiness")
	timeout := fs.Duration("timeout", 5*time.Minute, "deadline for the complete refresh wait (maximum 1h)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || *timeout <= 0 || *timeout > time.Hour {
		PrintUsage(os.Stderr, "usage: gregale data-api refresh NAME [--wait [--timeout DURATION]]", "data-api")
		return 1
	}
	var explicitTimeout bool
	fs.Visit(func(f *flag.Flag) { explicitTimeout = explicitTimeout || f.Name == "timeout" })
	if explicitTimeout && !*wait {
		return printErr("Invalid flags", errors.New("--timeout requires --wait"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if *wait {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}
	var healthURL string
	if *wait {
		app, getErr := client.GetApp(ctx, fs.Arg(0))
		if getErr != nil {
			return printErr("Could not inspect Data API app", getErr)
		}
		healthURL, err = dataAPIReadinessURL(app)
		if err != nil {
			return printErr("Could not check Data API readiness", err)
		}
	}
	result, err := client.RestartAppFresh(ctx, fs.Arg(0))
	if err != nil {
		return printErr("Could not refresh Data API schema", err)
	}
	if *wait {
		if err = waitDataAPIRefresh(ctx, client, fs.Arg(0), result.WakeID, healthURL, time.Second); err != nil {
			return printErr("Could not verify Data API schema refresh", dataAPIRefreshDiagnostic(err))
		}
		if jsonOutput {
			return jsonOut(writeJSON(dataAPIRefreshReceipt{AppRestartResponse: result, Status: "completed", Ready: true}))
		}
		PrintOK(osStdout, "Schema refresh completed for %s (wake_id=%s); Data API is ready", fs.Arg(0), result.WakeID)
		return 0
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	PrintOK(osStdout, "Schema refresh requested for %s; verify /healthz before using the new contract", fs.Arg(0))
	return 0
}

func cmdDataAPICreate(args []string) int {
	fs := newFlagSet("data-api create", flag.ContinueOnError)
	database := fs.String("database", "", "managed PostgreSQL name or ID")
	schema := fs.String("schema", "api", "exposed schema (api, or public with --read-only)")
	readOnly := fs.Bool("read-only", false, "use a SELECT-only public-schema binding and reject mutation requests")
	scope := fs.String("scope", api.DefaultEnvScope, "environment scope")
	issuer := fs.String("issuer", "", "HTTPS application JWT issuer")
	jwks := fs.String("jwks-url", "", "HTTPS application JWKS URL")
	audience := fs.String("audience", "", "required application JWT audience")
	origins := fs.String("origins", "", "comma-separated allowed browser origins")
	resume := fs.Bool("resume", false, "resume configuration and deployment of an existing app")
	authApp := unreleasedStringFlag(fs, "auth", "Auth app whose tokens to verify (replaces --issuer, --jwks-url and --audience)")
	publicRead := fs.String("public-read", "", "comma-separated schema.relation list readable without a token")
	publicRPC := fs.String("public-rpc", "", "comma-separated schema.function list callable without a token by GET")
	aggregates := fs.Bool("aggregates", false, "enable aggregate queries (also enables the SQL deadline)")
	migrations := fs.String("migrations", "", "single-app project: migration directory run as this app's release task")
	releaseCommand := fs.String("release-command", dataAPIDefaultReleaseCommand, "single-app release command run with "+dataAPIMigrationEnv)
	if err := fs.Parse(normalizeDataAPITypeArgs(args)); err != nil {
		return 1
	}
	schemaExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "schema" {
			schemaExplicit = true
		}
	})
	if *readOnly && !schemaExplicit {
		*schema = "public"
	}
	explicitIssuer := dataAPIHTTPS(*issuer) && dataAPIHTTPS(*jwks) && strings.TrimSpace(*audience) != ""
	if *authApp != "" && (*issuer != "" || *jwks != "" || *audience != "") {
		return printErr("Invalid flags", errors.New("--auth replaces --issuer, --jwks-url and --audience; pass one or the other"))
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || *database == "" || (!*readOnly && *schema != "api") || (*readOnly && *schema != "public") || api.ValidateScope(*scope) != nil || (*authApp == "" && !explicitIssuer) {
		PrintUsage(os.Stderr, dataAPICreateUsage(), "data-api")
		return 1
	}
	if *publicRead != "" && !validDataAPIPublicRead(*publicRead) {
		return printErr("Invalid --public-read", fmt.Errorf("list at most %d unique schema.relation names", api.DataAPIPublicReadMaxRelations))
	}
	if *publicRPC != "" && !validDataAPIPublicRPC(*publicRPC) {
		return printErr("Invalid --public-rpc", fmt.Errorf("list at most %d unique schema.function names", api.DataAPIPublicRPCMaxFunctions))
	}
	if *migrations != "" {
		if _, err := checkedDataAPIMigrations(*migrations, *releaseCommand); err != nil {
			return printErr("Invalid --migrations", err)
		}
	}
	for _, origin := range strings.Split(*origins, ",") {
		if origin != "" && !dataAPIOrigin(origin) {
			return printErr("Invalid origin", errors.New("origins must be HTTPS origins without credentials, paths, queries or fragments"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if *authApp != "" {
		resolved, err := resolveAuthIssuer(ctx, client, *authApp)
		if err != nil {
			return printErr("Could not resolve the Auth app", err)
		}
		*issuer, *jwks, *audience = resolved.Issuer, resolved.JWKSURL, resolved.Audience
	}
	db, err := resolveManagedPostgresDatabase(ctx, client, *database)
	if err != nil {
		return printErr("Could not resolve PostgreSQL database", err)
	}
	if db.State != "ready" {
		return printErr("Database unavailable", errors.New("the managed database must be ready"))
	}
	// Check the backend offers data_api credentials before creating anything.
	// On production-us the command created an app first and only then failed
	// the binding, leaving an undeployed app on the plan's app count (hunt #9
	// H9-P119; live test 2026-10-14 hit the 100-app cap instead). An unreadable
	// capability answer does not block: the binding still decides.
	access := "data_api"
	if *readOnly {
		access = "read_only"
	}
	if caps, capErr := client.GetManagedPostgresCapabilities(ctx, db.Region); capErr == nil && !slices.Contains(caps.CredentialAccess, access) {
		return printErr("Data API not offered", fmt.Errorf("the database's backend in %s does not offer data_api credential access; no app was created", db.Region))
	}
	var app api.AppResponse
	if *resume {
		app, err = client.GetApp(ctx, fs.Arg(0))
	} else {
		requireAuthn := false
		app, err = client.CreateApp(ctx, api.CreateAppRequest{Slug: fs.Arg(0), Type: "app", RequireAuthn: &requireAuthn})
	}
	if err != nil {
		return printErr("Could not create Data API app", err)
	}
	if app.Type != "" && app.Type != "app" {
		return printErr("Invalid Data API app", errors.New("data APIs require an app workload"))
	}
	// Retain partially configured intent on failure: the ordinary app and
	// binding recovery paths own cleanup and retries, never an ad-hoc rollback.
	settings := map[string]string{"DATA_API_SCHEMAS": *schema, "DATA_API_ISSUER": *issuer, "DATA_API_JWKS_URL": *jwks, "DATA_API_AUDIENCE": *audience, "DATA_API_ALLOWED_ORIGINS": *origins, "DATA_API_PUBLIC_READ": *publicRead, "DATA_API_PUBLIC_RPC": *publicRPC, "DATA_API_READ_ONLY": fmt.Sprint(*readOnly)}
	// Usage reporting is on for new APIs whenever the platform API is HTTPS.
	if origin, ok := dataAPIUsageURL(client.BaseURL()); ok {
		settings[dataAPIUsageURLKey] = origin
	}
	if *aggregates {
		settings["DATA_API_SQL_DEADLINE"], settings["DATA_API_AGGREGATES"] = "1", "1"
	}
	if err := configureDataAPI(ctx, client, app, db.ID, *scope, settings); err != nil {
		return printErr("Data API configuration failed; the app is retained for recovery", err)
	}
	if *migrations != "" {
		if err := attachDataAPIMigrationBinding(ctx, client, app, db.ID, *scope); err != nil {
			return printErr("Migration binding failed; the app is retained for recovery", err)
		}
		return deploySingleAppDataAPI(ctx, app.Slug, *scope, *migrations, *releaseCommand, "15m")
	}
	deployArgs := []string{"--name", app.Slug, "--template", "data-api", "--dockerfile", "--healthcheck-path", "/healthz", "--no-doctor"}
	if *scope != api.DefaultEnvScope {
		deployArgs = append(deployArgs, "--environment", *scope)
	}
	return cmdDeployTarballToExisting(ctx, deployArgs, true)
}

func configureDataAPI(ctx context.Context, client *api.Client, app api.AppResponse, database, scope string, settings map[string]string) error {
	access := "data_api"
	if settings["DATA_API_READ_ONLY"] == "true" {
		access = "read_only"
	}
	binding, err := client.CreateManagedPostgresBinding(api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("data-api", app.ID, database, scope)), database, api.CreateManagedPostgresBindingRequest{AppID: app.ID, Scope: scope, EnvironmentKey: "DATABASE_URL", Access: access})
	if err != nil {
		return err
	}
	if _, err = waitForManagedPostgresBinding(ctx, client, binding, addResourceDefaultWait); err != nil {
		return err
	}
	// PostgreSQL's extra egress port remains subject to the normal plan gate.
	ports := []int{5432}
	// Application JWTs are verified by the Data API runtime. Paid-plan edge
	// defaults require an owner API key, which would reject those JWTs first.
	requireAuthn := false
	publicAuth := openPublicAuthAfterTokenRemoval(app)
	update := api.UpdateAppRequest{EgressPorts: &ports, RequireAuthn: &requireAuthn, PublicAuth: publicAuth, SetPublicAuth: publicAuth != nil}
	setDataAPILifecycle(&update)
	if _, err = client.UpdateApp(ctx, app.Slug, update); err != nil {
		return err
	}
	for _, key := range []string{"DATA_API_SCHEMAS", "DATA_API_ISSUER", "DATA_API_JWKS_URL", "DATA_API_AUDIENCE", "DATA_API_ALLOWED_ORIGINS", "DATA_API_READ_ONLY"} {
		if err = client.SetSecretWithScope(ctx, app.Slug, key, func() string {
			if key == "DATA_API_READ_ONLY" && settings[key] == "" {
				return "false"
			}
			return settings[key]
		}(), scope); err != nil {
			return err
		}
	}
	// Optional owner opt-ins are stored only when requested; absence keeps them off.
	for _, key := range []string{"DATA_API_PUBLIC_READ", "DATA_API_PUBLIC_RPC", "DATA_API_SQL_DEADLINE", "DATA_API_AGGREGATES", dataAPIUsageURLKey} {
		if settings[key] == "" {
			continue
		}
		if err = client.SetSecretWithScope(ctx, app.Slug, key, settings[key], scope); err != nil {
			return err
		}
	}
	return nil
}

// The runtime stops PostgREST before a snapshot and starts it after a restore,
// so no snapshot holds a SQL session (templates/data-api/engine.mjs).
const (
	dataAPIBeforeCheckpointPath = "/__gregale/lifecycle/before-checkpoint"
	dataAPIAfterRestorePath     = "/__gregale/lifecycle/after-restore"
)

func setDataAPILifecycle(update *api.UpdateAppRequest) {
	update.BeforeCheckpoint = &api.BeforeCheckpointHook{Path: dataAPIBeforeCheckpointPath, TimeoutMS: api.AfterRestoreHookMaxTimeoutMS}
	update.AfterRestore = &api.AfterRestoreHook{Path: dataAPIAfterRestorePath, TimeoutMS: api.AfterRestoreHookMaxTimeoutMS}
}

func dataAPIHTTPS(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

func dataAPIOrigin(value string) bool {
	u, err := url.Parse(value)
	return err == nil && dataAPIHTTPS(value) && u.Path == "" && u.RawQuery == ""
}

var dataAPIFingerprint = regexp.MustCompile(`(?m)^// Schema fingerprint: ([a-f0-9]{64})$`)

func cmdDataAPITypes(args []string) int {
	fs := newFlagSet("data-api types", flag.ContinueOnError)
	output := fs.String("output", "", "write generated types atomically to a file")
	check := fs.Bool("check", false, "fail if --output differs from the database contract")
	snapshot := fs.Bool("snapshot", false, "export a JSON contract baseline for data-api diff")
	validators := fs.Bool("validators", false, "include typed runtime table and RPC argument validators")
	language := fs.String("language", "typescript", "client language: typescript, python or go")
	pkg := fs.String("package", "gregaledata", "Go package name for --language go")
	snapshotFile := fs.String("snapshot-file", "", "render Python or Go from a saved snapshot without contacting the API")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum task wait")
	// This command never downloads credentials or introspects customer SQL
	// from apid. A bounded manual task runs inside the selected app revision.
	if err := fs.Parse(normalizeDataAPITypeArgs(args)); err != nil {
		return 1
	}
	usage := "usage: gregale data-api types NAME [--output FILE] [--check] [--snapshot | --validators] [--timeout DURATION]\n       gregale data-api types [NAME | --snapshot-file FILE] --language python|go [--package NAME] [--output FILE [--check]]"
	if *language != "typescript" {
		// Python and Go render from the contract snapshot, locally or via the app task.
		named := fs.NArg() == 1 && api.ValidAppSlug(fs.Arg(0))
		if !dataAPIDiagnosticAllowed(*language, "python", "go") || named == (*snapshotFile != "") || fs.NArg() > 1 || *snapshot || *validators || (*check && *output == "") || (*language == "python" && *pkg != "gregaledata") || !clientgen.GoPackageValid(*pkg) || *timeout <= 0 || *timeout > time.Hour {
			PrintUsage(os.Stderr, usage, "data-api")
			return 1
		}
		return dataAPIClientTypes(fs.Arg(0), *language, *pkg, *snapshotFile, *output, *check, *timeout)
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || (*check && *output == "") || (*snapshot && *validators) || *snapshotFile != "" || *pkg != "gregaledata" || *timeout <= 0 || *timeout > time.Hour {
		PrintUsage(os.Stderr, usage, "data-api")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var task api.AppTaskResponse
	if *snapshot {
		task, err = generateDataAPIContract(ctx, client, fs.Arg(0))
	} else if *validators {
		task, err = generateDataAPIInputValidators(ctx, client, fs.Arg(0))
	} else {
		task, err = generateDataAPITypes(ctx, client, fs.Arg(0))
	}
	if err != nil {
		return printErr("Schema generation failed", err)
	}
	if err = writeDataAPITypes(*output, *check, task.StdoutTail); err != nil {
		return printErr("Could not export schema types", err)
	}
	if *snapshot {
		if *output == "" {
			_, err = fmt.Fprint(osStdout, task.StdoutTail)
			if err != nil {
				return printErr("Could not write snapshot", err)
			}
		} else if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"task_id": task.ID, "deployment_id": task.DeploymentID, "output": *output}))
		} else {
			PrintOK(osStdout, "Database snapshot written: %s", *output)
		}
		return 0
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"task_id": task.ID, "deployment_id": task.DeploymentID, "fingerprint": dataAPIFingerprint.FindStringSubmatch(task.StdoutTail)[1], "output": *output, "types": task.StdoutTail}))
	}
	if *output == "" {
		_, err = fmt.Fprint(osStdout, task.StdoutTail)
		if err != nil {
			return printErr("Could not write types", err)
		}
	} else {
		PrintOK(osStdout, "Database types %s: %s", map[bool]string{true: "match", false: "written"}[*check], *output)
	}
	return 0
}

func generateDataAPIInputValidators(ctx context.Context, client *api.Client, slug string) (api.AppTaskResponse, error) {
	task, err := client.CreateAppTask(ctx, slug, api.CreateAppTaskRequest{Command: []string{"node", "/app/types.mjs", "--validators"}, TimeoutSeconds: 60, MaxOutputBytes: api.AppTaskDefaultMaxOutputBytes})
	if err != nil {
		return task, fmt.Errorf("start input validator generation: %w", err)
	}
	task, err = waitDataAPITypeTask(ctx, client, slug, task)
	if err == nil && (!strings.Contains(task.StdoutTail, "export const inputValidators = {") || !strings.Contains(task.StdoutTail, "export const rpcValidators = {")) {
		err = errors.New("runtime does not support table and RPC argument validator generation; update the Data API runtime")
	}
	return task, err
}

func generateDataAPITypes(ctx context.Context, client *api.Client, slug string) (api.AppTaskResponse, error) {
	task, err := client.CreateAppTask(ctx, slug, api.CreateAppTaskRequest{Command: []string{"node", "/app/types.mjs"}, TimeoutSeconds: 60, MaxOutputBytes: api.AppTaskDefaultMaxOutputBytes})
	if err != nil {
		return task, fmt.Errorf("start schema generation: %w", err)
	}
	return waitDataAPITypeTask(ctx, client, slug, task)
}

// --check is a boolean; the positional normalizer for PostgreSQL flags would
// otherwise treat an app following it as the flag's value.
func normalizeDataAPITypeArgs(args []string) []string {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return append(append([]string{}, args[1:]...), args[0])
	}
	return args
}

func waitDataAPITypeTask(ctx context.Context, client *api.Client, slug string, task api.AppTaskResponse) (api.AppTaskResponse, error) {
	return waitDataAPIContractTask(ctx, client, slug, task, false)
}

func waitDataAPIContractTask(ctx context.Context, client *api.Client, slug string, task api.AppTaskResponse, snapshot bool) (api.AppTaskResponse, error) {
	for !task.Status.Terminal() {
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return task, ctx.Err()
		case <-timer.C:
		}
		var err error
		task, err = client.GetAppTask(ctx, slug, task.ID)
		if err != nil {
			return task, err
		}
	}
	if task.Status != api.AppTaskStatusSucceeded {
		return task, fmt.Errorf("task %s ended with status %s", task.ID, task.Status)
	}
	if snapshot {
		if task.OutputTruncated {
			return task, errors.New("schema snapshot was truncated")
		}
		_, err := parseDataAPIContract([]byte(task.StdoutTail))
		return task, err
	}
	if task.OutputTruncated || len(task.StdoutTail) > api.AppTaskDefaultMaxOutputBytes || !strings.HasPrefix(task.StdoutTail, "// Generated by gregale data-api types. Do not edit.\n") || !dataAPIFingerprint.MatchString(task.StdoutTail) {
		return task, errors.New("schema generation returned incomplete or invalid output")
	}
	return task, nil
}

func writeDataAPITypes(output string, check bool, content string) error {
	if output == "" {
		return nil
	}
	if check {
		current, err := os.ReadFile(output)
		if err != nil {
			return err
		}
		if string(current) != content {
			return errors.New("database contract is stale; regenerate with gregale data-api types")
		}
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(output), ".gregale-types-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.WriteString(content); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Chmod(0o644); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), output)
}
