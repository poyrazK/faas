package main

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func secretReferenceCLIFlags() []cliFlag {
	return []cliFlag{{Name: "app", Short: "app slug", Value: "slug", Req: true},
		{Name: "environment", Short: "registered project environment", Value: "ENV", Req: true}}
}

func cmdSecretReferences(args []string) int {
	if len(args) == 0 || (args[0] != "list" && args[0] != "set" && args[0] != "unset") {
		_, _ = fmt.Fprintln(osStderr, "usage: gregale secrets refs <list|set|unset> --app <slug> --environment <name> [KEY=secret:NAME|KEY]")
		return 1
	}
	operation := args[0]
	fs := newFlagSet("secrets refs "+operation, flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	environment := fs.String("environment", "", "registered project environment")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if operation == "list" && fs.NArg() != 0 || operation != "list" && fs.NArg() != 1 {
		return printErr("Invalid reference", fmt.Errorf("unexpected positional arguments: list takes none; set and unset take one destination key"))
	}
	if *app == "" || !api.ValidProjectEnvironmentSlug(*environment) || api.ValidateScope(*environment) != nil {
		return printErr("Select an app and environment", fmt.Errorf("--app and --environment are required"))
	}
	key, reference, err := secretReferenceArguments(operation, fs.Args())
	if err != nil {
		return printErr("Invalid reference", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	switch operation {
	case "list":
		response, err := client.ListAppSecretReferences(ctx, *app, *environment)
		if err != nil {
			return printErr("List references failed", err)
		}
		return renderSecretReferences(*app, response)
	case "set":
		response, err := client.SetAppSecretReference(ctx, *app, *environment, key, api.PutAppSecretReferenceRequest{Reference: reference})
		if err != nil {
			return printErr("Set reference failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(response))
		}
		_, _ = fmt.Fprintf(osStdout, "%s/%s: %s -> %s\n", *app, *environment, key, reference)
	case "unset":
		if err := client.DeleteAppSecretReference(ctx, *app, *environment, key); err != nil {
			return printErr("Remove reference failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]string{"environment": *environment, "key": key}))
		}
		_, _ = fmt.Fprintf(osStdout, "%s/%s: key %s suppressed on future cold wakes\n", *app, *environment, key)
	}
	return 0
}

func secretReferenceArguments(operation string, args []string) (string, string, error) {
	if operation == "list" {
		if len(args) != 0 {
			return "", "", fmt.Errorf("list accepts no positional arguments")
		}
		return "", "", nil
	}
	if len(args) != 1 {
		return "", "", fmt.Errorf("provide one destination key, or KEY=secret:NAME for set")
	}
	key, reference := args[0], ""
	if operation == "set" {
		var found bool
		key, reference, found = strings.Cut(args[0], "=")
		if !found || (api.PutAppSecretReferenceRequest{Reference: reference}).Validate() != nil {
			return "", "", fmt.Errorf("set requires KEY=secret:NAME")
		}
	}
	if problem := api.ValidateEnvKey(key); problem != nil {
		return "", "", fmt.Errorf("destination must be a valid environment key")
	}
	return key, reference, nil
}

func renderSecretReferences(app string, response api.AppSecretReferenceListResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(response))
	}
	_, _ = fmt.Fprintf(osStdout, "%s/%s: %d/%d environment keys across all environments\n", app, response.Environment, response.Count, response.Quota)
	keys := make([]string, 0, len(response.References))
	for key := range response.References {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		_, _ = fmt.Fprintf(osStdout, "  %s -> %s\n", key, response.References[key])
	}
	sort.Strings(response.SuppressedKeys)
	for _, key := range response.SuppressedKeys {
		_, _ = fmt.Fprintf(osStdout, "  %s (suppressed)\n", key)
	}
	return 0
}
