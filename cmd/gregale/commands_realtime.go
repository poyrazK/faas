package main

// Customer-facing managed realtime commands. Rotation credentials are accepted
// from stdin or a hidden prompt; no successful response contains token data.

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdRealtime(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale realtime <list|get|create|update|delete|connections|drain|drain-status|send|close|subscribe|unsubscribe|publish|auth>", "realtime")
		return 1
	}
	switch args[0] {
	case "list":
		return cmdRealtimeList(args[1:])
	case "get":
		return cmdRealtimeGet(args[1:])
	case "create":
		return cmdRealtimeCreate(args[1:])
	case "update":
		return cmdRealtimeUpdate(args[1:])
	case "delete", "rm":
		return cmdRealtimeDelete(args[1:])
	case "connections":
		return cmdRealtimeConnections(args[1:])
	case "drain":
		return cmdRealtimeDrain(args[1:])
	case "drain-status", "drain_status":
		return cmdRealtimeDrainStatus(args[1:])
	case "send":
		return cmdRealtimeSend(args[1:])
	case "close":
		return cmdRealtimeClose(args[1:])
	case "subscribe":
		return cmdRealtimeSubscribe(args[1:])
	case "unsubscribe":
		return cmdRealtimeUnsubscribe(args[1:])
	case "publish":
		return cmdRealtimePublish(args[1:])
	case "auth":
		return cmdRealtimeAuth(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown realtime subcommand %q\n", args[0])
		return 1
	}
}

func cmdRealtimeList(args []string) int {
	fs := newFlagSet("realtime list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		PrintUsage(os.Stderr, "usage: gregale realtime list APP_SLUG", "realtime")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	endpoints, err := client.ListManagedRealtimeEndpoints(context.Background(), fs.Arg(0))
	if err != nil {
		return printErr("Could not list realtime endpoints", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(endpoints))
	}
	if len(endpoints) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No managed realtime endpoints.")
		return 0
	}
	_, _ = fmt.Fprintln(osStdout, "ID                                      AUTH MODE      ENABLED  ROTATION")
	for _, endpoint := range endpoints {
		_, _ = fmt.Fprintf(osStdout, "%-39s %-14s %-8t %s\n", endpoint.ID, endpoint.AuthMode, endpoint.Enabled, realtimeRotationLabel(endpoint.AuthTokenPreviousExpiresAt))
	}
	return 0
}

func cmdRealtimeGet(args []string) int {
	return cmdRealtimeEndpointRead("realtime get", "usage: gregale realtime get APP_SLUG ENDPOINT_ID", args)
}

func cmdRealtimeAuth(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale realtime auth <rotate|finalize|status>", "realtime")
		return 1
	}
	switch args[0] {
	case "rotate":
		return cmdRealtimeAuthRotate(args[1:])
	case "finalize":
		return cmdRealtimeAuthFinalize(args[1:])
	case "status":
		return cmdRealtimeEndpointRead("realtime auth status", "usage: gregale realtime auth status APP_SLUG ENDPOINT_ID", args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown realtime auth subcommand %q\n", args[0])
		return 1
	}
}

func cmdRealtimeEndpointRead(command, usage string, args []string) int {
	fs := newFlagSet(command, flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 2 || strings.TrimSpace(fs.Arg(0)) == "" || strings.TrimSpace(fs.Arg(1)) == "" {
		PrintUsage(os.Stderr, usage, "realtime")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	endpoint, err := client.GetManagedRealtimeEndpoint(context.Background(), fs.Arg(0), fs.Arg(1))
	if err != nil {
		return printErr("Could not load realtime endpoint", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(endpoint))
	}
	renderRealtimeEndpoint(osStdout, endpoint)
	return 0
}

func cmdRealtimeAuthRotate(args []string) int {
	args = normalizeRealtimeRotateArgs(args)
	fs := newFlagSet("realtime auth rotate", flag.ContinueOnError)
	token := fs.String("token", "", "replacement token (compatibility; visible in shell history; prefer --token-stdin)")
	tokenStdin := fs.Bool("token-stdin", false, "read the replacement token from stdin")
	grace := fs.Int64("grace-period", -1, "seconds to accept the previous token (0..86400)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 2 || strings.TrimSpace(fs.Arg(0)) == "" || strings.TrimSpace(fs.Arg(1)) == "" {
		PrintUsage(os.Stderr, "usage: gregale realtime auth rotate APP_SLUG ENDPOINT_ID (--token-stdin|--token TOKEN) [--grace-period SECONDS]", "realtime")
		return 1
	}
	if *grace < -1 || *grace > api.RealtimeAuthRotationMaxGraceSeconds {
		return printErr("Invalid grace period", fmt.Errorf("must be between 0 and %d seconds", api.RealtimeAuthRotationMaxGraceSeconds))
	}
	newToken, fromArg, err := readRealtimeAuthToken(*token, *tokenStdin)
	if err != nil {
		return printErr("Could not read replacement token", err)
	}
	if fromArg {
		PrintWarn(osStderr, "--token is visible to shell history and process inspection; prefer --token-stdin")
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	request := api.RotateManagedRealtimeAuthRequest{NewAuthToken: newToken}
	if *grace >= 0 {
		value := *grace
		request.GracePeriodSeconds = &value
	}
	response, err := client.RotateManagedRealtimeAuth(context.Background(), fs.Arg(0), fs.Arg(1), request)
	if err != nil {
		return printErr("Could not rotate realtime auth", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(response))
	}
	PrintOK(osStdout, "Realtime auth rotation started for endpoint %s.", response.EndpointID)
	_, _ = fmt.Fprintf(osStdout, "  previous_token_expires_at: %s\n", realtimeExpiryValue(response.PreviousTokenExpiresAt))
	return 0
}

func cmdRealtimeAuthFinalize(args []string) int {
	fs := newFlagSet("realtime auth finalize", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 2 || strings.TrimSpace(fs.Arg(0)) == "" || strings.TrimSpace(fs.Arg(1)) == "" {
		PrintUsage(os.Stderr, "usage: gregale realtime auth finalize APP_SLUG ENDPOINT_ID", "realtime")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	response, err := client.FinalizeManagedRealtimeAuth(context.Background(), fs.Arg(0), fs.Arg(1))
	if err != nil {
		return printErr("Could not finalize realtime auth rotation", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(response))
	}
	PrintOK(osStdout, "Realtime auth rotation finalized for endpoint %s.", response.EndpointID)
	_, _ = fmt.Fprintln(osStdout, "  previous_token_expires_at: none (revoked immediately)")
	return 0
}

func readRealtimeAuthToken(explicit string, fromStdin bool) (string, bool, error) {
	if explicit != "" && fromStdin {
		return "", false, fmt.Errorf("--token and --token-stdin are mutually exclusive")
	}
	fromArg := explicit != ""
	token := explicit
	if fromStdin {
		body, err := io.ReadAll(io.LimitReader(osStdin, int64(api.RealtimeAuthTokenMaxBytes)+2))
		if err != nil {
			return "", false, err
		}
		token = strings.TrimRight(string(body), "\r\n")
	} else if token == "" && stdinIsTTY() {
		value, err := readInteractivePassword(bufio.NewReader(osStdin), "New realtime token: ")
		if err != nil {
			return "", false, err
		}
		token = value
	}
	if token == "" {
		return "", false, fmt.Errorf("provide --token-stdin, --token, or enter a token interactively")
	}
	if len(token) > api.RealtimeAuthTokenMaxBytes {
		return "", false, fmt.Errorf("token exceeds %d bytes", api.RealtimeAuthTokenMaxBytes)
	}
	return token, fromArg, nil
}

func normalizeRealtimeRotateArgs(args []string) []string {
	var flags, positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--token", "--grace-period":
			flags = append(flags, arg)
			if i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
		case "--token-stdin":
			flags = append(flags, arg)
		default:
			if strings.HasPrefix(arg, "-") {
				flags = append(flags, arg)
			} else {
				positionals = append(positionals, arg)
			}
		}
	}
	return append(flags, positionals...)
}

func renderRealtimeEndpoint(w io.Writer, endpoint api.ManagedRealtimeEndpointResponse) {
	_, _ = fmt.Fprintf(w, "realtime endpoint %s\n", endpoint.ID)
	_, _ = fmt.Fprintf(w, "  app_id:                       %s\n", endpoint.AppID)
	_, _ = fmt.Fprintf(w, "  callback_url:                 %s\n", endpoint.CallbackURL)
	_, _ = fmt.Fprintf(w, "  connect_path:                 %s\n", endpoint.ConnectPath)
	_, _ = fmt.Fprintf(w, "  message_path:                 %s\n", endpoint.MessagePath)
	_, _ = fmt.Fprintf(w, "  disconnect_path:              %s\n", endpoint.DisconnectPath)
	_, _ = fmt.Fprintf(w, "  auth_mode:                    %s\n", endpoint.AuthMode)
	if endpoint.AuthTokenMasked == "" {
		_, _ = fmt.Fprintln(w, "  auth_token:                   not configured")
	} else {
		_, _ = fmt.Fprintln(w, "  auth_token:                   configured")
	}
	if endpoint.AuthTokenPreviousExpiresAt == nil {
		_, _ = fmt.Fprintln(w, "  auth_rotation:                inactive")
	} else {
		_, _ = fmt.Fprintf(w, "  auth_rotation:                %s\n", realtimeRotationLabel(endpoint.AuthTokenPreviousExpiresAt))
		_, _ = fmt.Fprintf(w, "  previous_token_expires_at:    %s\n", *endpoint.AuthTokenPreviousExpiresAt)
	}
	_, _ = fmt.Fprintf(w, "  allowed_origins:              %s\n", realtimeOriginsValue(endpoint.AllowedOrigins))
	_, _ = fmt.Fprintf(w, "  max_connections:              %d\n", endpoint.MaxConnections)
	_, _ = fmt.Fprintf(w, "  max_message_bytes:            %d\n", endpoint.MaxMessageBytes)
	_, _ = fmt.Fprintf(w, "  max_connection_age_seconds:   %d\n", endpoint.MaxConnectionAgeSeconds)
	_, _ = fmt.Fprintf(w, "  enabled:                      %t\n", endpoint.Enabled)
}

func realtimeOriginsValue(origins []string) string {
	if len(origins) == 0 {
		return "none"
	}
	return strings.Join(origins, ",")
}

func realtimeRotationLabel(expiry *string) string {
	if expiry == nil || strings.TrimSpace(*expiry) == "" {
		return "inactive"
	}
	parsed, err := time.Parse(time.RFC3339, *expiry)
	if err == nil && time.Now().UTC().Before(parsed) {
		return "active"
	}
	return "expired"
}

func realtimeExpiryValue(expiry *string) string {
	if expiry == nil || strings.TrimSpace(*expiry) == "" {
		return "not reported"
	}
	return *expiry
}
