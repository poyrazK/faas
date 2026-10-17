package main

// `gregale postgres live` enables ADR-1384 live queries on a managed database
// and manages its live secret. The API returns a secret only in the response
// that issues it, so enable and rotate either store it as an app secret
// (--app) or print it once.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const liveSecretEnvDefault = "GREGALE_LIVE_SECRET"

func cmdPostgresLive(args []string) int {
	usage := "usage: gregale postgres live <status|enable|rotate|disable> DATABASE"
	if len(args) == 0 {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	switch args[0] {
	case "status":
		return cmdPostgresLiveStatus(args[1:])
	case "enable":
		return cmdPostgresLiveIssue("enable", args[1:])
	case "rotate":
		return cmdPostgresLiveIssue("rotate", args[1:])
	case "disable":
		return cmdPostgresLiveDisable(args[1:])
	default:
		printCommandValidation(os.Stderr, "unknown postgres live subcommand %q\n", args[0])
		return 1
	}
}

func cmdPostgresLiveStatus(args []string) int {
	fs := newFlagSet("postgres live status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale postgres live status DATABASE", "postgres")
		return 1
	}
	client, database, code := liveDatabase(fs.Arg(0))
	if client == nil {
		return code
	}
	state, err := client.GetManagedPostgresLiveQueries(context.Background(), database.ID)
	if err != nil {
		return printErr("Could not load live-query state", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(state))
	}
	renderPostgresLive(database.Name, state)
	return 0
}

// cmdPostgresLiveIssue enables live queries or rotates the secret, then
// stores the new secret on --app or prints it.
func cmdPostgresLiveIssue(action string, args []string) int {
	fs := newFlagSet("postgres live "+action, flag.ContinueOnError)
	app := fs.String("app", "", "store the secret as an app secret on this app")
	scope := fs.String("scope", "", "environment scope for --app (default scope when empty)")
	env := fs.String("env", liveSecretEnvDefault, "secret name for --app")
	usage := "usage: gregale postgres live " + action + " DATABASE [--app APP] [--scope SCOPE] [--env NAME]"
	overlap := new(time.Duration)
	if action == "rotate" {
		overlap = fs.Duration("overlap", api.LiveQuerySecretRotationOverlap, "how long the previous secret keeps working; 0 revokes it at once")
		usage += " [--overlap 24h]"
	}
	if err := fs.Parse(normalizePostgresAttachArgs(args)); err != nil || fs.NArg() != 1 || *overlap < 0 || *overlap > api.LiveQuerySecretRotationOverlapMax {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	client, database, code := liveDatabase(fs.Arg(0))
	if client == nil {
		return code
	}
	var (
		state api.ManagedPostgresLiveQueries
		err   error
	)
	if action == "enable" {
		state, err = client.EnableManagedPostgresLiveQueries(context.Background(), database.ID)
	} else {
		seconds := int64(*overlap / time.Second)
		state, err = client.RotateManagedPostgresLiveSecret(context.Background(), database.ID, api.RotateManagedPostgresLiveSecretRequest{OverlapSeconds: &seconds})
	}
	if err != nil {
		return printErr("Could not "+action+" live queries", err)
	}
	stored := false
	if *app != "" {
		if err := client.SetSecretWithScope(context.Background(), *app, *env, state.Secret, *scope); err != nil {
			// The secret cannot be read again, so it is printed rather than lost.
			_, _ = fmt.Fprintf(os.Stderr, "Could not store the live secret on %s (%v); it is printed below. Store it yourself: gregale secrets set %s=... --app %s\n", *app, err, *env, *app)
		} else {
			stored = true
		}
	}
	if jsonOutput {
		if stored {
			state.Secret = ""
		}
		return jsonOut(writeJSON(state))
	}
	renderPostgresLive(database.Name, state)
	if stored {
		_, _ = fmt.Fprintf(osStdout, "Stored the live secret as %s on %s; redeploy or reload the app to use it.\n", *env, *app)
	} else {
		_, _ = fmt.Fprintf(osStdout, "Live secret (shown once; store it as a backend secret, e.g. %s):\n%s\n", *env, state.Secret)
	}
	return 0
}

func cmdPostgresLiveDisable(args []string) int {
	fs := newFlagSet("postgres live disable", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale postgres live disable DATABASE", "postgres")
		return 1
	}
	client, database, code := liveDatabase(fs.Arg(0))
	if client == nil {
		return code
	}
	if err := client.DisableManagedPostgresLiveQueries(context.Background(), database.ID); err != nil {
		return printErr("Could not disable live queries", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"database_id": database.ID, "enabled": false}))
	}
	_, _ = fmt.Fprintf(osStdout, "Live queries disabled on %s; every live secret is revoked.\n", database.Name)
	return 0
}

func liveDatabase(reference string) (*api.Client, api.ManagedPostgresDatabase, int) {
	client, err := authedClient()
	if err != nil {
		return nil, api.ManagedPostgresDatabase{}, printErr("Not logged in", err)
	}
	database, err := resolveManagedPostgresDatabase(context.Background(), client, reference)
	if err != nil {
		return nil, api.ManagedPostgresDatabase{}, printErr("Could not find managed PostgreSQL database", err)
	}
	return client, database, 0
}

func renderPostgresLive(name string, state api.ManagedPostgresLiveQueries) {
	status := "disabled"
	if state.Enabled {
		status = "enabled"
	}
	_, _ = fmt.Fprintf(osStdout, "live queries on %s: %s\n", name, status)
	for _, k := range state.Keys {
		if k.Active {
			_, _ = fmt.Fprintf(osStdout, "  key %s  active   created %s\n", k.KeyID, k.CreatedAt)
		} else {
			_, _ = fmt.Fprintf(osStdout, "  key %s  retiring at %s\n", k.KeyID, k.RetiresAt)
		}
	}
}
