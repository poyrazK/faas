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
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdDataAPI(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale data-api <create|types|refresh>", "data-api")
		return 1
	}
	switch args[0] {
	case "create":
		return cmdDataAPICreate(args[1:])
	case "types":
		return cmdDataAPITypes(args[1:])
	case "refresh":
		return cmdDataAPIRefresh(args[1:])
	default:
		return printErr("Unknown Data API command", fmt.Errorf("use create, types or refresh"))
	}
}

// A snapshot restart would preserve PostgREST's stale schema cache. Use the
// existing fresh-restart lifecycle to rebuild it on every active instance.
func cmdDataAPIRefresh(args []string) int {
	if len(args) != 1 || !api.ValidAppSlug(args[0]) {
		PrintUsage(os.Stderr, "usage: gregale data-api refresh NAME", "data-api")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.RestartAppFresh(context.Background(), args[0])
	if err != nil {
		return printErr("Could not refresh Data API schema", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	PrintOK(osStdout, "Schema refresh requested for %s; verify /healthz before using the new contract", args[0])
	return 0
}

func cmdDataAPICreate(args []string) int {
	fs := newFlagSet("data-api create", flag.ContinueOnError)
	database := fs.String("database", "", "managed PostgreSQL name or ID")
	schema := fs.String("schema", "api", "exposed schema (managed Data APIs use api)")
	scope := fs.String("scope", api.DefaultEnvScope, "environment scope")
	issuer := fs.String("issuer", "", "HTTPS application JWT issuer")
	jwks := fs.String("jwks-url", "", "HTTPS application JWKS URL")
	audience := fs.String("audience", "", "required application JWT audience")
	origins := fs.String("origins", "", "comma-separated allowed browser origins")
	resume := fs.Bool("resume", false, "resume configuration and deployment of an existing app")
	if err := fs.Parse(normalizeDataAPITypeArgs(args)); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || *database == "" || *schema != "api" || api.ValidateScope(*scope) != nil || !dataAPIHTTPS(*issuer) || !dataAPIHTTPS(*jwks) || strings.TrimSpace(*audience) == "" {
		PrintUsage(os.Stderr, "usage: gregale data-api create NAME --database DATABASE --issuer HTTPS_URL --jwks-url HTTPS_URL --audience AUDIENCE [--scope SCOPE] [--origins ORIGINS]", "data-api")
		return 1
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
	db, err := resolveManagedPostgresDatabase(ctx, client, *database)
	if err != nil {
		return printErr("Could not resolve PostgreSQL database", err)
	}
	if db.State != "ready" {
		return printErr("Database unavailable", errors.New("the managed database must be ready"))
	}
	var app api.AppResponse
	if *resume {
		app, err = client.GetApp(ctx, fs.Arg(0))
	} else {
		app, err = client.CreateApp(ctx, api.CreateAppRequest{Slug: fs.Arg(0), Type: "app"})
	}
	if err != nil {
		return printErr("Could not create Data API app", err)
	}
	if app.Type != "" && app.Type != "app" {
		return printErr("Invalid Data API app", errors.New("Data APIs require an app workload"))
	}
	// Retain partially configured intent on failure: the ordinary app and
	// binding recovery paths own cleanup and retries, never an ad-hoc rollback.
	if err := configureDataAPI(ctx, client, app, db.ID, *scope, map[string]string{"DATA_API_SCHEMAS": *schema, "DATA_API_ISSUER": *issuer, "DATA_API_JWKS_URL": *jwks, "DATA_API_AUDIENCE": *audience, "DATA_API_ALLOWED_ORIGINS": *origins}); err != nil {
		return printErr("Data API configuration failed; the app is retained for recovery", err)
	}
	deployArgs := []string{"--name", app.Slug, "--template", "data-api", "--dockerfile", "--healthcheck-path", "/healthz", "--no-doctor"}
	if *scope != api.DefaultEnvScope {
		deployArgs = append(deployArgs, "--environment", *scope)
	}
	return cmdDeployTarballToExisting(ctx, deployArgs, true)
}

func configureDataAPI(ctx context.Context, client *api.Client, app api.AppResponse, database, scope string, settings map[string]string) error {
	binding, err := client.CreateManagedPostgresBinding(api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("data-api", app.ID, database, scope)), database, api.CreateManagedPostgresBindingRequest{AppID: app.ID, Scope: scope, EnvironmentKey: "DATABASE_URL", Access: "data_api"})
	if err != nil {
		return err
	}
	if _, err = waitForManagedPostgresBinding(ctx, client, binding, addResourceDefaultWait); err != nil {
		return err
	}
	// PostgreSQL's extra egress port remains subject to the normal plan gate.
	ports := []int{5432}
	if _, err = client.UpdateApp(ctx, app.Slug, api.UpdateAppRequest{EgressPorts: &ports}); err != nil {
		return err
	}
	for _, key := range []string{"DATA_API_SCHEMAS", "DATA_API_ISSUER", "DATA_API_JWKS_URL", "DATA_API_AUDIENCE", "DATA_API_ALLOWED_ORIGINS"} {
		if err = client.SetSecretWithScope(ctx, app.Slug, key, settings[key], scope); err != nil {
			return err
		}
	}
	return nil
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
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum task wait")
	// This command never downloads credentials or introspects customer SQL
	// from apid. A bounded manual task runs inside the selected app revision.
	if err := fs.Parse(normalizeDataAPITypeArgs(args)); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || (*check && *output == "") || *timeout <= 0 || *timeout > time.Hour {
		PrintUsage(os.Stderr, "usage: gregale data-api types NAME [--output FILE] [--check] [--timeout DURATION]", "data-api")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	task, err := client.CreateAppTask(ctx, fs.Arg(0), api.CreateAppTaskRequest{Command: []string{"node", "/app/types.mjs"}, TimeoutSeconds: 60, MaxOutputBytes: api.AppTaskDefaultMaxOutputBytes})
	if err != nil {
		return printErr("Could not start schema generation", err)
	}
	task, err = waitDataAPITypeTask(ctx, client, fs.Arg(0), task)
	if err != nil {
		return printErr("Schema generation failed", err)
	}
	if err = writeDataAPITypes(*output, *check, task.StdoutTail); err != nil {
		return printErr("Could not export schema types", err)
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

// --check is a boolean; the positional normalizer for PostgreSQL flags would
// otherwise treat an app following it as the flag's value.
func normalizeDataAPITypeArgs(args []string) []string {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return append(append([]string{}, args[1:]...), args[0])
	}
	return args
}

func waitDataAPITypeTask(ctx context.Context, client *api.Client, slug string, task api.AppTaskResponse) (api.AppTaskResponse, error) {
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
			return errors.New("database types are stale; regenerate with gregale data-api types")
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
