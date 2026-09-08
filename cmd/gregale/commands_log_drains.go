package main

// Customer runtime log destinations are managed below the existing logs
// resource so the command reads naturally alongside `logs tail`:
//
//   gregale logs drain list --app <slug>
//   gregale logs drain add --app <slug> --kind http_json --target-url <url>
//   gregale logs drain info --app <slug> <id>
//   gregale logs drain update --app <slug> <id> [flags]
//   gregale logs drain rm --app <slug> <id>
//
// Authentication headers are accepted only through stdin. The API returns a
// masked sentinel, so plaintext credentials never appear in command output.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

var logDrainIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$|^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func cmdLogDrain(args []string) int {
	parent, _ := lookupCliCommand("logs")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale logs drain <list|add|info|update|rm> [args]", "logs")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdLogDrainsList(args[1:])
	case subAdd:
		return cmdLogDrainAdd(args[1:])
	case subInfo:
		return cmdLogDrainInfo(args[1:])
	case subUpdate:
		return cmdLogDrainUpdate(args[1:])
	case subRm:
		return cmdLogDrainRm(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown logs drain subcommand %q\n", args[0])
		sug, _ := suggestSubcommand(args[0], parent)
		maybeSuggestSub(sug)
		return 1
	}
}

func cmdLogDrainsList(args []string) int {
	fs := flag.NewFlagSet("logs drain list", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *app == "" {
		PrintUsage(os.Stderr, "usage: gregale logs drain list --app <slug>", "logs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListAppLogDrains(context.Background(), *app)
	if err != nil {
		return printErr("List failed", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(out))
	}
	if len(out) == 0 {
		_, _ = fmt.Fprintf(osStdout, "%s: no log drains\n", *app)
		return 0
	}
	_, _ = fmt.Fprintln(osStdout, "ID                               KIND      TARGET                                               STATE     AUTH  UPDATED")
	for _, d := range out {
		auth := "—"
		if d.AuthHeaderMasked != "" {
			auth = d.AuthHeaderMasked
		}
		_, _ = fmt.Fprintf(osStdout, "%-32s %-9s %-50s %-9s %-5s %s\n",
			d.ID, d.Kind, truncate(d.TargetURL, 50), enabledStr(d.Enabled), auth, d.UpdatedAt)
	}
	return 0
}

func cmdLogDrainAdd(args []string) int {
	fs := flag.NewFlagSet("logs drain add", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (required)")
	kind := fs.String("kind", "", "encoding kind (http_json|otlp; required)")
	target := fs.String("target-url", "", "HTTPS target URL (required)")
	authStdin := fs.Bool("auth-header-stdin", false, "read the authentication header from stdin")
	disabled := fs.Bool("disabled", false, "create the drain disabled")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *app == "" || *kind == "" || *target == "" {
		PrintUsage(os.Stderr, "usage: gregale logs drain add --app <slug> --kind http_json|otlp --target-url <url> [--auth-header-stdin] [--disabled]", "logs")
		return 1
	}
	if !strInSlice(*kind, api.AllowedAppLogDrainKinds) {
		return printErr("Invalid --kind", fmt.Errorf("must be one of %s; got %q", strings.Join(api.AllowedAppLogDrainKinds, ", "), *kind))
	}
	auth, err := logDrainAuthHeader(*authStdin)
	if err != nil {
		return printErr("Invalid authentication header", err)
	}
	req := api.CreateAppLogDrainRequest{Kind: *kind, TargetURL: *target, AuthHeader: auth}
	if *disabled {
		enabled := false
		req.Enabled = &enabled
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.CreateAppLogDrain(context.Background(), *app, req)
	if err != nil {
		return printErr("Create failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Log drain created: %s (id=%s)", out.TargetURL, out.ID)
	return 0
}

func cmdLogDrainInfo(args []string) int {
	fs := flag.NewFlagSet("logs drain info", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *app == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale logs drain info --app <slug> <id>", "logs")
		return 1
	}
	id := fs.Arg(0)
	if !logDrainIDPattern.MatchString(id) {
		return printErr("Invalid log drain id", fmt.Errorf("must be a 32-hex-char UUID; got %q", id))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.GetAppLogDrain(context.Background(), *app, id)
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	printLogDrainInfo(out)
	return 0
}

func cmdLogDrainUpdate(args []string) int {
	fs := flag.NewFlagSet("logs drain update", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (required)")
	kind := fs.String("kind", "", "new encoding kind (http_json|otlp)")
	target := fs.String("target-url", "", "new HTTPS target URL")
	authStdin := fs.Bool("auth-header-stdin", false, "read the replacement authentication header from stdin")
	clearAuth := fs.Bool("clear-auth-header", false, "remove the configured authentication header")
	enable := fs.Bool("enable", false, "enable the drain")
	disable := fs.Bool("disable", false, "disable the drain")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *app == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale logs drain update --app <slug> <id> [--kind http_json|otlp] [--target-url <url>] [--auth-header-stdin|--clear-auth-header] [--enable|--disable]", "logs")
		return 1
	}
	id := fs.Arg(0)
	if !logDrainIDPattern.MatchString(id) {
		return printErr("Invalid log drain id", fmt.Errorf("must be a 32-hex-char UUID; got %q", id))
	}
	if *enable && *disable {
		return printErr("Invalid flags", fmt.Errorf("--enable and --disable are mutually exclusive"))
	}
	if *authStdin && *clearAuth {
		return printErr("Invalid flags", fmt.Errorf("--auth-header-stdin and --clear-auth-header are mutually exclusive"))
	}
	if *kind != "" && !strInSlice(*kind, api.AllowedAppLogDrainKinds) {
		return printErr("Invalid --kind", fmt.Errorf("must be one of %s; got %q", strings.Join(api.AllowedAppLogDrainKinds, ", "), *kind))
	}
	auth, err := logDrainAuthHeader(*authStdin)
	if err != nil {
		return printErr("Invalid authentication header", err)
	}
	req := api.UpdateAppLogDrainRequest{}
	if *kind != "" {
		v := *kind
		req.Kind = &v
	}
	if *target != "" {
		v := *target
		req.TargetURL = &v
	}
	if *authStdin {
		req.AuthHeader = &auth
	} else if *clearAuth {
		clear := ""
		req.AuthHeader = &clear
	}
	if *enable {
		v := true
		req.Enabled = &v
	} else if *disable {
		v := false
		req.Enabled = &v
	}
	if req.Kind == nil && req.TargetURL == nil && req.AuthHeader == nil && req.Enabled == nil {
		return printErr("No changes", fmt.Errorf("provide at least one update flag"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.UpdateAppLogDrain(context.Background(), *app, id, req)
	if err != nil {
		return printErr("Update failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Updated log drain %s", out.ID)
	return 0
}

func cmdLogDrainRm(args []string) int {
	fs := flag.NewFlagSet("logs drain rm", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *app == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale logs drain rm --app <slug> <id>", "logs")
		return 1
	}
	id := fs.Arg(0)
	if !logDrainIDPattern.MatchString(id) {
		return printErr("Invalid log drain id", fmt.Errorf("must be a 32-hex-char UUID; got %q", id))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteAppLogDrain(context.Background(), *app, id); err != nil {
		return printErr("Delete failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]string{"id": id, "status": "removed"}))
	}
	PrintOK(osStdout, "Removed log drain %s", id)
	return 0
}

func logDrainAuthHeader(fromStdin bool) (string, error) {
	if !fromStdin {
		return "", nil
	}
	data, err := io.ReadAll(io.LimitReader(osStdin, int64(api.AppLogDrainAuthHeaderMaxBytes)+1))
	if err != nil {
		return "", fmt.Errorf("could not read stdin: %w", err)
	}
	if len(data) > api.AppLogDrainAuthHeaderMaxBytes {
		return "", fmt.Errorf("stdin value exceeds %d bytes", api.AppLogDrainAuthHeaderMaxBytes)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("stdin value is empty")
	}
	return value, nil
}

func printLogDrainInfo(d api.AppLogDrainResponse) {
	auth := "not configured"
	if d.AuthHeaderMasked != "" {
		auth = d.AuthHeaderMasked
	}
	_, _ = fmt.Fprintf(osStdout, "id: %s\napp_id: %s\nkind: %s\ntarget_url: %s\nstate: %s\nauth_header: %s\ncreated_at: %s\nupdated_at: %s\n",
		d.ID, d.AppID, d.Kind, d.TargetURL, enabledStr(d.Enabled), auth, d.CreatedAt, d.UpdatedAt)
}
