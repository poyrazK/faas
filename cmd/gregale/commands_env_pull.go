package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

type envPullChange struct {
	Key    string `json:"key"`
	Action string `json:"action"`
}

type envPullPlan struct {
	AppSlug       string          `json:"app_slug"`
	Scope         string          `json:"scope"`
	OutputPath    string          `json:"output_path"`
	RemoteKeys    int             `json:"remote_key_count"`
	AddedKeys     int             `json:"added_key_count"`
	PreservedKeys int             `json:"preserved_key_count"`
	KeyOnly       bool            `json:"key_only"`
	DryRun        bool            `json:"dry_run"`
	WriteRequired bool            `json:"write_required"`
	Written       bool            `json:"written"`
	Changes       []envPullChange `json:"changes"`
}

func envPull(args []string) int {
	fs := newFlagSet("env pull", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	scope := fs.String("scope", "", "env scope (defaults to linked project environment)")
	out := fs.String("o", ".env", "output file (default .env)")
	dryRun := fs.Bool("dry-run", false, "preview missing key skeletons and preserved local keys without writing files")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	resolvedApp, resolveErr := resolveAppFlagOrContext(*app)
	if resolveErr == nil {
		*app = resolvedApp
	} else if !errors.Is(resolveErr, errProjectContextNotFound) {
		return printErr("Could not read local project context", resolveErr)
	}
	if *app == "" {
		PrintUsage(os.Stderr, "usage: gregale env pull --app <slug> [--env <name>] [-o .env] [--dry-run] (or run `gregale link <project-slug>`)", "env")
		return 1
	}
	resolvedScope, err := resolveEnvironmentFlagOrContext(*scope)
	if err != nil {
		return printErr("Could not read local project context", err)
	}
	// Capture before the request, so an editor's changes during the request
	// cannot be overwritten by a plan built from an older local snapshot.
	before, err := readEnvPullFile(*out)
	if err != nil {
		return printErr("Could not read existing .env", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	response, err := client.ListSecretsWithScope(ctx, *app, resolvedScope)
	if err != nil {
		return printErr("List failed", err)
	}
	plan, contents, err := planEnvPull(response.Secrets, before.data, before.exists())
	if err != nil {
		return printErr("Could not plan env pull", err)
	}
	plan.AppSlug, plan.Scope, plan.OutputPath, plan.DryRun = *app, scopeOrDefault(resolvedScope), *out, *dryRun
	if !*dryRun && plan.WriteRequired {
		staged, err := stageEnvPullFile(before, contents)
		if err != nil {
			return printErr("Could not stage .env", err)
		}
		defer staged.cleanup()
		if err := staged.commit(ctx); err != nil {
			return printErr("Could not write .env", err)
		}
		plan.Written = true
	}
	return renderEnvPullPlan(plan)
}

// The same plan supplies both the receipt and the bytes published on apply.
// Only key names enter the public plan; the existing file stays byte-for-byte
// intact and only missing, empty assignments are appended.
func planEnvPull(remote []api.AppSecretResponse, existing []byte, exists bool) (envPullPlan, []byte, error) {
	plan := envPullPlan{KeyOnly: true, Changes: []envPullChange{}}
	present := envAssignmentKeys(existing)
	keys := map[string]bool{}
	for _, secret := range remote {
		if api.ValidateSecretKey(secret.Key) != nil {
			return plan, nil, errors.New("server returned an invalid secret key; no file was written")
		}
		keys[secret.Key] = true
	}
	plan.RemoteKeys, plan.PreservedKeys = len(keys), len(present)
	all := map[string]bool{}
	for key := range keys {
		all[key] = true
	}
	for key := range present {
		all[key] = true
	}
	names := make([]string, 0, len(all))
	for key := range all {
		names = append(names, key)
	}
	sort.Strings(names)
	contents := bytes.Clone(existing)
	newline := []byte("\n")
	if bytes.Contains(existing, []byte("\r\n")) {
		newline = []byte("\r\n")
	}
	for _, key := range names {
		action := "local_only"
		_, local := present[key]
		if keys[key] && local {
			action = "preserve"
		} else if keys[key] {
			action = "add"
			if len(contents) > 0 && contents[len(contents)-1] != '\n' {
				contents = append(contents, newline...)
			}
			contents = append(contents, key...)
			contents = append(contents, '=')
			contents = append(contents, newline...)
			plan.AddedKeys++
		}
		plan.Changes = append(plan.Changes, envPullChange{Key: key, Action: action})
	}
	plan.WriteRequired = !exists || plan.AddedKeys > 0
	return plan, contents, nil
}

func renderEnvPullPlan(plan envPullPlan) int {
	if jsonOutput {
		return jsonOut(writeJSON(plan))
	}
	if plan.DryRun {
		_, _ = fmt.Fprintf(osStdout, "Env pull preview: %s (%s) -> %q\n", plan.AppSlug, plan.Scope, plan.OutputPath)
		for _, change := range plan.Changes {
			_, _ = fmt.Fprintf(osStdout, "%-10s %s\n", change.Action, change.Key)
		}
		_, _ = fmt.Fprintf(osStdout, "%d missing key(s); %d local key(s) preserved. No output file was written. Values are never downloaded.\n", plan.AddedKeys, plan.PreservedKeys)
		return 0
	}
	if !plan.Written {
		PrintOK(osStdout, "No missing keys in %s; existing values and local keys were preserved (file unchanged)", plan.OutputPath)
	} else if plan.RemoteKeys == 0 && plan.PreservedKeys == 0 {
		PrintOK(osStdout, "Wrote empty %s (%s has no secrets)", plan.OutputPath, plan.AppSlug)
	} else if plan.PreservedKeys == 0 {
		PrintOK(osStdout, "Wrote %d key(s) to %s (values intentionally blank — fill by hand)", plan.AddedKeys, plan.OutputPath)
	} else {
		PrintOK(osStdout, "Added %d missing key(s) to %s; existing values and local keys were preserved", plan.AddedKeys, plan.OutputPath)
	}
	return 0
}
