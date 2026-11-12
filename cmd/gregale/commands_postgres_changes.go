package main

// gregale postgres-changes (ADR-20261112140000): capture Postgres row changes
// into one of an app's queues. Managed databases are installed by Gregale;
// for a database you run, `install` runs the capture install on your machine
// with an owner URL from a local environment variable and uploads only the
// least-privilege relay login it creates.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

const postgresChangesUsage = "usage: gregale postgres-changes <apply|status|install|uninstall> [--app <slug>] [--path <dir>] [--connection-url-env <VAR>]"

type postgresChangesClient interface {
	PutPostgresChanges(context.Context, string, api.PutPostgresChangesRequest) (api.PostgresChangesResponse, error)
	GetPostgresChanges(context.Context, string) (api.PostgresChangesResponse, error)
	DeletePostgresChanges(context.Context, string) error
	PutPostgresChangesConnection(context.Context, string, string) error
}

func cmdPostgresChanges(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, postgresChangesUsage, "postgres-changes")
		return 1
	}
	verb := args[0]
	fs := newFlagSet("postgres-changes "+verb, flag.ContinueOnError)
	appFlag := fs.String("app", "", "app slug (defaults to the linked app)")
	path := fs.String("path", ".", "directory holding gregale.yaml")
	urlEnv := fs.String("connection-url-env", "", "local environment variable holding the database owner URL (defaults to the manifest's connection_url_env)")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	slug, err := resolveLinkedApp(*appFlag, *path)
	if err != nil {
		return printErr("No app selected; pass --app or run in a linked directory", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	switch verb {
	case "apply":
		m, ok, err := gregalemanifest.Load(*path)
		if err != nil {
			return printErr("Could not load gregale.yaml", err)
		}
		if !ok || m == nil || m.PostgresChanges == nil {
			return printErr("Nothing to apply", errors.New("gregale.yaml has no postgres_changes block"))
		}
		if err := m.Validate(); err != nil {
			return printErr("Invalid gregale.yaml", err)
		}
		if err := applyPostgresChanges(ctx, client, slug, m.PostgresChanges, *urlEnv, true); err != nil {
			return printErr("Postgres changes failed", err)
		}
		return 0
	case "status":
		out, err := client.GetPostgresChanges(ctx, slug)
		if err != nil {
			return printErr("Postgres changes request failed", err)
		}
		return jsonOut(writeJSON(out))
	case "install", "uninstall":
		env := *urlEnv
		if env == "" {
			if m, ok, err := gregalemanifest.Load(*path); err == nil && ok && m != nil && m.PostgresChanges != nil {
				env = m.PostgresChanges.ConnectionURLEnv
			}
		}
		if env == "" {
			return printErr("Owner URL unknown", errors.New("pass --connection-url-env or set postgres_changes.connection_url_env in gregale.yaml"))
		}
		if verb == "install" {
			err = installPostgresChanges(ctx, client, slug, env)
		} else {
			err = uninstallPostgresChanges(ctx, client, slug, env)
		}
		if err != nil {
			return printErr("Postgres changes "+verb+" failed", err)
		}
		return 0
	default:
		PrintUsage(os.Stderr, postgresChangesUsage, "postgres-changes")
		return 1
	}
}

// deployManifestPostgresChanges applies the manifest's postgres_changes block
// after queue bindings. An account the feature is not released to gets a
// warning, not a failed deploy.
func deployManifestPostgresChanges(ctx context.Context, client postgresChangesClient, slug, cwd string) error {
	if cwd == "" {
		return nil
	}
	m, ok, err := gregalemanifest.Load(cwd)
	if err != nil || !ok || m == nil || m.PostgresChanges == nil {
		return err
	}
	if err := m.Validate(); err != nil {
		return err
	}
	return applyPostgresChanges(ctx, client, slug, m.PostgresChanges, "", false)
}

func applyPostgresChanges(ctx context.Context, client postgresChangesClient, slug string, spec *gregalemanifest.PostgresChangesSpec, urlEnv string, explicit bool) error {
	req, err := spec.Request()
	if err != nil {
		return err
	}
	out, err := client.PutPostgresChanges(ctx, slug, req)
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Code == "postgres_changes_unavailable" && !explicit {
		PrintWarn(os.Stderr, "postgres_changes skipped: Postgres change workers are not released to this account yet")
		return nil
	}
	if err != nil {
		return err
	}
	if out.Managed {
		PrintOK(osStdout, "Postgres changes installed on %s; changes go to queue %s.", out.Database, out.QueueName)
		return nil
	}
	if urlEnv == "" {
		urlEnv = spec.ConnectionURLEnv
	}
	if os.Getenv(urlEnv) == "" {
		if !out.Connected {
			PrintWarn(os.Stderr, "postgres_changes declared; set %s to the database owner URL and run `gregale postgres-changes install --app %s` to start capture", urlEnv, slug)
		}
		return nil
	}
	return installPostgresChanges(ctx, client, slug, urlEnv)
}

// installPostgresChanges installs capture with the owner URL in the local
// environment variable env and uploads only the relay login it creates.
func installPostgresChanges(ctx context.Context, client postgresChangesClient, slug, env string) error {
	raw := os.Getenv(env)
	if raw == "" {
		return fmt.Errorf("%s is not set", env)
	}
	status, err := client.GetPostgresChanges(ctx, slug)
	if err != nil {
		return err
	}
	if status.Managed {
		return errors.New("this app captures a managed database; Gregale installs it on deploy")
	}
	password, err := commitwork.NewRelayPassword()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, raw)
	if err != nil {
		return fmt.Errorf("connect with %s: %w", env, redactPostgresError(err, raw))
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	res, err := commitwork.InstallChanges(ctx, tx, commitwork.ChangesInstall{SourceID: status.SourceID, Feeds: status.Feeds, RelayPassword: password})
	if err != nil {
		return redactPostgresError(err, raw)
	}
	cfg := conn.Config()
	relayURL, err := commitwork.ChangesRelayURL(cfg.Host, cfg.Port, cfg.Database, res.RelayRole, password)
	if err != nil {
		return fmt.Errorf("the relay connects with verified TLS to a host name: %w", err)
	}
	// Upload before committing: if Gregale refuses the login, the database
	// keeps its previous relay password and nothing is half-installed.
	if err := client.PutPostgresChangesConnection(ctx, slug, relayURL); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, removed := range res.Removed {
		PrintOK(osStdout, "Removed capture %s (no longer declared).", removed)
	}
	PrintOK(osStdout, "Postgres changes installed; relay login %s connected. Changes go to queue %s.", res.RelayRole, status.QueueName)
	return nil
}

func uninstallPostgresChanges(ctx context.Context, client postgresChangesClient, slug, env string) error {
	raw := os.Getenv(env)
	if raw == "" {
		return fmt.Errorf("%s is not set", env)
	}
	status, err := client.GetPostgresChanges(ctx, slug)
	if err != nil {
		return err
	}
	if err := client.DeletePostgresChanges(ctx, slug); err != nil {
		return err
	}
	if status.Managed {
		PrintOK(osStdout, "Postgres changes stopped; Gregale removed capture from %s.", status.Database)
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, raw)
	if err != nil {
		return fmt.Errorf("connect with %s: %w", env, redactPostgresError(err, raw))
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	removed, err := commitwork.RemoveChanges(ctx, tx, status.SourceID, "")
	if err != nil {
		return redactPostgresError(err, raw)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	PrintOK(osStdout, "Postgres changes stopped; removed %s capture trigger(s) and the relay login.", strconv.Itoa(len(removed)))
	return nil
}

// redactPostgresError keeps the owner URL's password out of printed errors.
func redactPostgresError(err error, raw string) error {
	if err == nil {
		return nil
	}
	u, perr := url.Parse(raw)
	if perr != nil || u.User == nil {
		return err
	}
	if pw, ok := u.User.Password(); ok && pw != "" {
		return errors.New(strings.ReplaceAll(err.Error(), pw, "REDACTED"))
	}
	return err
}
