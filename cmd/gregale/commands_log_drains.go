package main

// `gregale log-drains` — the CLI for /v1/apps/{slug}/log-drains. The API and
// SDK client existed; production-us hunt #4 (H4-63) found no command to reach
// them. The endpoint credential is read from an environment variable so it
// never appears in argv or shell history; responses only carry it masked.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const logDrainsUsage = "usage: gregale log-drains <list|add|get|health|update|rm> --app <slug> [flags]"

func cmdLogDrains(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, logDrainsUsage, "log-drains")
		return 1
	}
	switch args[0] {
	case "list", "ls":
		return cmdLogDrainsList(args[1:])
	case "add", "create":
		return cmdLogDrainsAdd(args[1:])
	case "get", "info":
		return cmdLogDrainsGet(args[1:])
	case "health":
		return cmdLogDrainsHealth(args[1:])
	case "update":
		return cmdLogDrainsUpdate(args[1:])
	case "rm", "delete", "remove":
		return cmdLogDrainsRm(args[1:])
	default:
		PrintUsage(os.Stderr, logDrainsUsage, "log-drains")
		return 1
	}
}

// logDrainFlags parses --app (or a leading slug) plus the leaf's own flags.
func logDrainFlags(name string, args []string, needID bool, define func(*flag.FlagSet)) (slug, id string, ok bool) {
	fs := newFlagSet("log-drains "+name, flag.ContinueOnError)
	app := fs.String("app", "", "app slug (required)")
	drain := fs.String("id", "", "log drain id")
	if define != nil {
		define(fs)
	}
	leading, rest := peelLeadingSlug(args)
	if err := fs.Parse(rest); err != nil {
		return "", "", false
	}
	if rejectUnexpectedFlagArgs(fs) {
		return "", "", false
	}
	if err := mergeLeadingSlug(app, leading); err != nil {
		printErr("Invalid arguments", err)
		return "", "", false
	}
	if *app == "" || (needID && *drain == "") {
		usage := "usage: gregale log-drains " + name + " --app <slug>"
		if needID {
			usage += " --id <drain-id>"
		}
		PrintUsage(os.Stderr, usage, "log-drains")
		return "", "", false
	}
	return *app, *drain, true
}

func cmdLogDrainsList(args []string) int {
	slug, _, ok := logDrainFlags("list", args, false, nil)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	drains, err := client.ListAppLogDrains(context.Background(), slug)
	if err != nil {
		return printErr("List failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(drains))
	}
	if len(drains) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no log drains)")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "%-36s  %-9s  %-7s  %s\n", "ID", "KIND", "ENABLED", "TARGET")
	for _, d := range drains {
		_, _ = fmt.Fprintf(osStdout, "%-36s  %-9s  %-7t  %s\n", d.ID, d.Kind, d.Enabled, d.TargetURL)
	}
	return 0
}

// logDrainAuthHeader reads the destination credential from the named
// environment variable; an unset or empty variable is an error so a typo
// cannot silently create an unauthenticated drain.
func logDrainAuthHeader(env string) (*string, error) {
	if env == "" {
		return nil, nil
	}
	value, set := os.LookupEnv(env)
	if !set || strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("environment variable %s is not set", env)
	}
	if len(value) > api.AppLogDrainAuthHeaderMaxBytes {
		return nil, fmt.Errorf("%s is longer than %d bytes", env, api.AppLogDrainAuthHeaderMaxBytes)
	}
	return &value, nil
}

func cmdLogDrainsAdd(args []string) int {
	var kind, target, authEnv *string
	var disabled *bool
	slug, _, ok := logDrainFlags("add", args, false, func(fs *flag.FlagSet) {
		kind = fs.String("kind", "http_json", "destination format: "+strings.Join(api.AllowedAppLogDrainKinds, "|"))
		target = fs.String("url", "", "destination URL (required)")
		authEnv = fs.String("auth-header-env", "", "environment variable holding the Authorization header value sent to the destination")
		disabled = fs.Bool("disabled", false, "create the drain disabled")
	})
	if !ok {
		return 1
	}
	if *target == "" {
		PrintUsage(os.Stderr, "usage: gregale log-drains add --app <slug> --url <URL> [--kind http_json|otlp] [--auth-header-env VAR]", "log-drains")
		return 1
	}
	auth, err := logDrainAuthHeader(*authEnv)
	if err != nil {
		return printErr("Invalid arguments", err)
	}
	req := api.CreateAppLogDrainRequest{Kind: *kind, TargetURL: *target}
	if auth != nil {
		req.AuthHeader = *auth
	}
	if *disabled {
		enabled := false
		req.Enabled = &enabled
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	drain, err := client.CreateAppLogDrain(context.Background(), slug, req)
	if err != nil {
		return printErr("Create failed", err)
	}
	return renderLogDrain(drain, "Created")
}

func cmdLogDrainsGet(args []string) int {
	slug, id, ok := logDrainFlags("get", args, true, nil)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	drain, err := client.GetAppLogDrain(context.Background(), slug, id)
	if err != nil {
		return printErr("Get failed", err)
	}
	return renderLogDrain(drain, "")
}

func cmdLogDrainsHealth(args []string) int {
	slug, id, ok := logDrainFlags("health", args, true, nil)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	h, err := client.GetAppLogDrainHealth(context.Background(), slug, id)
	if err != nil {
		return printErr("Health failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(h))
	}
	_, _ = fmt.Fprintf(osStdout, "Drain:      %s\nStatus:     %s (active=%t)\n", h.LogDrainID, h.Status, h.Active)
	_, _ = fmt.Fprintf(osStdout, "Queue:      %d/%d records, %d/%d bytes pending\n", h.QueueDepth, h.QueueCapacity, h.PendingBytes, h.PendingBytesCapacity)
	_, _ = fmt.Fprintf(osStdout, "Delivered:  %d  failed: %d  dropped: %d  retries: %d  dead-lettered: %d\n",
		h.DeliveredTotal, h.FailedTotal, h.DroppedTotal, h.RetriesTotal, h.DeadLetterTotal)
	if h.LastSuccessAt != "" {
		_, _ = fmt.Fprintf(osStdout, "Last ok:    %s\n", h.LastSuccessAt)
	}
	if h.LastFailureAt != "" {
		_, _ = fmt.Fprintf(osStdout, "Last error: %s (%s)\n", h.LastError, h.LastFailureAt)
	}
	return 0
}

func cmdLogDrainsUpdate(args []string) int {
	var target, authEnv *string
	var enable, disable *bool
	slug, id, ok := logDrainFlags("update", args, true, func(fs *flag.FlagSet) {
		target = fs.String("url", "", "new destination URL")
		authEnv = fs.String("auth-header-env", "", "environment variable holding the new Authorization header value")
		enable = fs.Bool("enable", false, "resume delivery")
		disable = fs.Bool("disable", false, "pause delivery")
	})
	if !ok {
		return 1
	}
	if *enable && *disable {
		return printErr("Invalid arguments", errors.New("choose one of --enable and --disable"))
	}
	auth, err := logDrainAuthHeader(*authEnv)
	if err != nil {
		return printErr("Invalid arguments", err)
	}
	req := api.UpdateAppLogDrainRequest{AuthHeader: auth}
	if *target != "" {
		req.TargetURL = target
	}
	if *enable || *disable {
		enabled := *enable
		req.Enabled = &enabled
	}
	if req.TargetURL == nil && req.AuthHeader == nil && req.Enabled == nil {
		return printErr("Invalid arguments", errors.New("nothing to update: pass --url, --auth-header-env, --enable or --disable"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	drain, err := client.UpdateAppLogDrain(context.Background(), slug, id, req)
	if err != nil {
		return printErr("Update failed", err)
	}
	return renderLogDrain(drain, "Updated")
}

func cmdLogDrainsRm(args []string) int {
	slug, id, ok := logDrainFlags("rm", args, true, nil)
	if !ok {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteAppLogDrain(context.Background(), slug, id); err != nil {
		return printErr("Delete failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"deleted": true, "id": id}))
	}
	_, _ = fmt.Fprintf(osStdout, "Deleted log drain %s\n", id)
	return 0
}

func renderLogDrain(d api.AppLogDrainResponse, verb string) int {
	if jsonOutput {
		return jsonOut(writeJSON(d))
	}
	if verb != "" {
		_, _ = fmt.Fprintf(osStdout, "%s log drain %s\n", verb, d.ID)
	}
	_, _ = fmt.Fprintf(osStdout, "ID:       %s\nKind:     %s\nTarget:   %s\nEnabled:  %t\n", d.ID, d.Kind, d.TargetURL, d.Enabled)
	if d.AuthHeaderMasked != "" {
		_, _ = fmt.Fprintf(osStdout, "Auth:     %s\n", d.AuthHeaderMasked)
	}
	return 0
}
