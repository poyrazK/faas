package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// realtimeStringList and realtimeClaimsFlag make repeated policy flags easy
// to use while keeping the wire request arrays/maps typed.
type realtimeStringList []string

func (v *realtimeStringList) String() string { return strings.Join(*v, ",") }

func (v *realtimeStringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("value must not be empty")
	}
	*v = append(*v, value)
	return nil
}

type realtimeClaimsFlag map[string]string

func (v *realtimeClaimsFlag) String() string {
	parts := make([]string, 0, len(*v))
	for key, value := range *v {
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, ",")
}

func (v *realtimeClaimsFlag) Set(value string) error {
	key, claimValue, ok := strings.Cut(value, "=")
	key, claimValue = strings.TrimSpace(key), strings.TrimSpace(claimValue)
	if !ok || key == "" || claimValue == "" {
		return fmt.Errorf("must use KEY=VALUE")
	}
	if *v == nil {
		*v = make(map[string]string)
	}
	(*v)[key] = claimValue
	return nil
}

func cmdRealtimeCreate(args []string) int {
	flags, positional := splitArgsForFlags(args, "enabled", "callback-auth-token-stdin", "auth-token-stdin")
	fs := newFlagSet("realtime create", flag.ContinueOnError)
	callbackURL := fs.String("callback-url", "", "application callback URL (required)")
	callbackToken := fs.String("callback-auth-token", "", "callback bearer token (prefer --callback-auth-token-stdin)")
	callbackTokenStdin := fs.Bool("callback-auth-token-stdin", false, "read the callback bearer token from stdin")
	connectPath := fs.String("connect-path", "", "callback path for connect events")
	messagePath := fs.String("message-path", "", "callback path for message events")
	disconnectPath := fs.String("disconnect-path", "", "callback path for disconnect events")
	authToken := fs.String("auth-token", "", "client static bearer token (prefer --auth-token-stdin)")
	authTokenStdin := fs.Bool("auth-token-stdin", false, "read the client static bearer token from stdin")
	authMode := fs.String("auth-mode", "", "client auth mode (none|static_bearer|oidc_jwt)")
	authIssuer := fs.String("auth-issuer", "", "OIDC issuer URL")
	authJWKSURL := fs.String("auth-jwks-url", "", "OIDC JWKS URL")
	var audience, algorithms, origins realtimeStringList
	var claims realtimeClaimsFlag
	fs.Var(&audience, "auth-audience", "OIDC audience (repeatable)")
	fs.Var(&algorithms, "auth-algorithm", "OIDC signing algorithm (repeatable)")
	fs.Var(&claims, "auth-claim", "required OIDC claim as KEY=VALUE (repeatable)")
	fs.Var(&origins, "allowed-origin", "exact browser origin (repeatable)")
	maxConnections := fs.Int("max-connections", -1, "per-endpoint connection cap (0 inherits daemon default)")
	maxMessageBytes := fs.Int64("max-message-bytes", -1, "per-endpoint decoded message cap (0 inherits daemon default)")
	maxConnectionAge := fs.Int64("max-connection-age-seconds", -1, "per-endpoint connection age cap (0 inherits daemon default)")
	enabled := fs.Bool("enabled", true, "enable the endpoint immediately")
	if err := fs.Parse(flags); err != nil || len(positional) != 1 {
		PrintUsage(osStderr, "usage: gregale realtime create APP_SLUG --callback-url URL (--callback-auth-token-stdin|--callback-auth-token TOKEN) [--auth-mode MODE] [--auth-token-stdin|--auth-token TOKEN] [policy flags]", "realtime")
		return 1
	}
	if strings.TrimSpace(positional[0]) == "" || strings.TrimSpace(*callbackURL) == "" {
		PrintUsage(osStderr, "usage: gregale realtime create APP_SLUG --callback-url URL (--callback-auth-token-stdin|--callback-auth-token TOKEN) [--auth-mode MODE] [--auth-token-stdin|--auth-token TOKEN] [policy flags]", "realtime")
		return 1
	}
	if *callbackTokenStdin && *authTokenStdin {
		return printErr("Invalid secret input", fmt.Errorf("--callback-auth-token-stdin and --auth-token-stdin cannot be used together; stdin carries one secret per command"))
	}
	callbackSecret, callbackFromArg, err := readRealtimeEndpointSecret(*callbackToken, *callbackTokenStdin, "callback-auth-token", "Callback auth token: ", api.RealtimeCallbackAuthTokenMaxBytes, true)
	if err != nil {
		return printErr("Could not read callback auth token", err)
	}
	authSecret, authFromArg, err := readRealtimeEndpointSecret(*authToken, *authTokenStdin, "auth-token", "Realtime auth token: ", api.RealtimeAuthTokenMaxBytes, false)
	if err != nil {
		return printErr("Could not read realtime auth token", err)
	}
	if callbackFromArg {
		PrintWarn(osStderr, "--callback-auth-token is visible to shell history and process inspection; prefer --callback-auth-token-stdin")
	}
	if authFromArg {
		PrintWarn(osStderr, "--auth-token is visible to shell history and process inspection; prefer --auth-token-stdin")
	}
	if err := validateRealtimeLifecyclePolicy(*authMode, origins, *maxConnections, *maxMessageBytes, *maxConnectionAge); err != nil {
		return printErr("Invalid realtime endpoint policy", err)
	}
	request := api.CreateManagedRealtimeEndpointRequest{
		CallbackURL:        *callbackURL,
		ConnectPath:        *connectPath,
		MessagePath:        *messagePath,
		DisconnectPath:     *disconnectPath,
		CallbackAuthToken:  callbackSecret,
		AuthToken:          authSecret,
		AuthMode:           *authMode,
		AuthIssuer:         *authIssuer,
		AuthJWKSURL:        *authJWKSURL,
		AuthAudience:       append([]string(nil), audience...),
		AuthAlgorithms:     append([]string(nil), algorithms...),
		AuthRequiredClaims: cloneRealtimeClaimsFlag(claims),
		AllowedOrigins:     append([]string(nil), origins...),
		Enabled:            enabled,
	}
	if *maxConnections >= 0 {
		request.MaxConnections = *maxConnections
	}
	if *maxMessageBytes >= 0 {
		request.MaxMessageBytes = *maxMessageBytes
	}
	if *maxConnectionAge >= 0 {
		request.MaxConnectionAgeSeconds = *maxConnectionAge
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	endpoint, err := client.CreateManagedRealtimeEndpoint(context.Background(), positional[0], request)
	if err != nil {
		return printErr("Could not create realtime endpoint", err)
	}
	return renderRealtimeLifecycleEndpoint(endpoint)
}

func cmdRealtimeUpdate(args []string) int {
	flags, positional := splitArgsForFlags(args, "callback-auth-token-stdin", "auth-token-stdin", "enable", "disable", "clear-allowed-origins", "clear-auth-claims")
	fs := newFlagSet("realtime update", flag.ContinueOnError)
	callbackURL := fs.String("callback-url", "", "new application callback URL")
	callbackToken := fs.String("callback-auth-token", "", "new callback bearer token (prefer --callback-auth-token-stdin)")
	callbackTokenStdin := fs.Bool("callback-auth-token-stdin", false, "read the new callback bearer token from stdin")
	connectPath := fs.String("connect-path", "", "new callback path for connect events")
	messagePath := fs.String("message-path", "", "new callback path for message events")
	disconnectPath := fs.String("disconnect-path", "", "new callback path for disconnect events")
	authToken := fs.String("auth-token", "", "new client static bearer token (prefer --auth-token-stdin)")
	authTokenStdin := fs.Bool("auth-token-stdin", false, "read the new client static bearer token from stdin")
	authMode := fs.String("auth-mode", "", "new client auth mode (none|static_bearer|oidc_jwt)")
	authIssuer := fs.String("auth-issuer", "", "new OIDC issuer URL")
	authJWKSURL := fs.String("auth-jwks-url", "", "new OIDC JWKS URL")
	var audience, algorithms, origins realtimeStringList
	var claims realtimeClaimsFlag
	fs.Var(&audience, "auth-audience", "replace OIDC audience (repeatable)")
	fs.Var(&algorithms, "auth-algorithm", "replace OIDC signing algorithms (repeatable)")
	fs.Var(&claims, "auth-claim", "set required OIDC claim as KEY=VALUE (repeatable)")
	fs.Var(&origins, "allowed-origin", "replace exact browser origins (repeatable)")
	clearOrigins := fs.Bool("clear-allowed-origins", false, "clear the browser-origin allowlist")
	clearClaims := fs.Bool("clear-auth-claims", false, "clear required OIDC claims")
	maxConnections := fs.Int("max-connections", -1, "new per-endpoint connection cap")
	maxMessageBytes := fs.Int64("max-message-bytes", -1, "new per-endpoint decoded message cap")
	maxConnectionAge := fs.Int64("max-connection-age-seconds", -1, "new per-endpoint connection age cap")
	enable := fs.Bool("enable", false, "enable the endpoint")
	disable := fs.Bool("disable", false, "disable the endpoint")
	if err := fs.Parse(flags); err != nil || len(positional) != 2 {
		PrintUsage(osStderr, "usage: gregale realtime update APP_SLUG ENDPOINT_ID [configuration flags]", "realtime")
		return 1
	}
	if strings.TrimSpace(positional[0]) == "" || strings.TrimSpace(positional[1]) == "" {
		PrintUsage(osStderr, "usage: gregale realtime update APP_SLUG ENDPOINT_ID [configuration flags]", "realtime")
		return 1
	}
	if *enable && *disable {
		return printErr("Invalid flags", fmt.Errorf("--enable and --disable are mutually exclusive"))
	}
	if *callbackTokenStdin && *authTokenStdin {
		return printErr("Invalid secret input", fmt.Errorf("--callback-auth-token-stdin and --auth-token-stdin cannot be used together; stdin carries one secret per command"))
	}
	callbackSecret, callbackFromArg, err := readRealtimeEndpointSecret(*callbackToken, *callbackTokenStdin, "callback-auth-token", "Callback auth token: ", api.RealtimeCallbackAuthTokenMaxBytes, false)
	if err != nil {
		return printErr("Could not read callback auth token", err)
	}
	authSecret, authFromArg, err := readRealtimeEndpointSecret(*authToken, *authTokenStdin, "auth-token", "Realtime auth token: ", api.RealtimeAuthTokenMaxBytes, false)
	if err != nil {
		return printErr("Could not read realtime auth token", err)
	}
	if callbackFromArg {
		PrintWarn(osStderr, "--callback-auth-token is visible to shell history and process inspection; prefer --callback-auth-token-stdin")
	}
	if authFromArg {
		PrintWarn(osStderr, "--auth-token is visible to shell history and process inspection; prefer --auth-token-stdin")
	}
	if err := validateRealtimeLifecyclePolicy(*authMode, origins, *maxConnections, *maxMessageBytes, *maxConnectionAge); err != nil {
		return printErr("Invalid realtime endpoint policy", err)
	}
	request := api.UpdateManagedRealtimeEndpointRequest{}
	if *callbackURL != "" {
		request.CallbackURL = callbackURL
	}
	if *connectPath != "" {
		request.ConnectPath = connectPath
	}
	if *messagePath != "" {
		request.MessagePath = messagePath
	}
	if *disconnectPath != "" {
		request.DisconnectPath = disconnectPath
	}
	if *callbackToken != "" || *callbackTokenStdin {
		request.CallbackAuthToken = &callbackSecret
	}
	if *authToken != "" || *authTokenStdin {
		request.AuthToken = &authSecret
	}
	if *authMode != "" {
		request.AuthMode = authMode
	}
	if *authIssuer != "" {
		request.AuthIssuer = authIssuer
	}
	if *authJWKSURL != "" {
		request.AuthJWKSURL = authJWKSURL
	}
	if len(audience) > 0 {
		request.AuthAudience = slicePointer([]string(audience))
	}
	if len(algorithms) > 0 {
		request.AuthAlgorithms = slicePointer([]string(algorithms))
	}
	if len(claims) > 0 {
		request.AuthRequiredClaims = claimsPointer(cloneRealtimeClaimsFlag(claims))
	} else if *clearClaims {
		empty := map[string]string{}
		request.AuthRequiredClaims = &empty
	}
	if len(origins) > 0 {
		request.AllowedOrigins = slicePointer([]string(origins))
	} else if *clearOrigins {
		empty := []string{}
		request.AllowedOrigins = &empty
	}
	if *maxConnections >= 0 {
		request.MaxConnections = maxConnections
	}
	if *maxMessageBytes >= 0 {
		request.MaxMessageBytes = maxMessageBytes
	}
	if *maxConnectionAge >= 0 {
		request.MaxConnectionAgeSeconds = maxConnectionAge
	}
	if *enable {
		value := true
		request.Enabled = &value
	} else if *disable {
		value := false
		request.Enabled = &value
	}
	if requestHasNoRealtimeChanges(request) {
		return printErr("No changes requested", fmt.Errorf("provide at least one endpoint configuration flag"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	endpoint, err := client.UpdateManagedRealtimeEndpoint(context.Background(), positional[0], positional[1], request)
	if err != nil {
		return printErr("Could not update realtime endpoint", err)
	}
	return renderRealtimeLifecycleEndpoint(endpoint)
}

func cmdRealtimeDelete(args []string) int {
	flags, positional := splitArgsForFlags(args, "yes")
	fs := newFlagSet("realtime delete", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "confirm endpoint deletion")
	if err := fs.Parse(flags); err != nil || len(positional) != 2 {
		PrintUsage(osStderr, "usage: gregale realtime delete APP_SLUG ENDPOINT_ID --yes", "realtime")
		return 1
	}
	if !*yes {
		return printErr("Confirmation required", fmt.Errorf("realtime endpoint deletion requires --yes"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteManagedRealtimeEndpoint(context.Background(), positional[0], positional[1]); err != nil {
		return printErr("Could not delete realtime endpoint", err)
	}
	result := map[string]any{"app_slug": positional[0], "endpoint_id": positional[1], "removed": true}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	PrintOK(osStdout, "Removed realtime endpoint %s.", positional[1])
	return 0
}

func readRealtimeEndpointSecret(explicit string, fromStdin bool, name, prompt string, maxBytes int, required bool) (string, bool, error) {
	if explicit != "" && fromStdin {
		return "", false, fmt.Errorf("--%s and --%s-stdin are mutually exclusive", name, name)
	}
	fromArg := explicit != ""
	value := explicit
	if fromStdin {
		body, err := readLimitedStdin(maxBytes)
		if err != nil {
			return "", false, err
		}
		value = strings.TrimRight(string(body), "\r\n")
	} else if required && value == "" && stdinIsTTY() {
		secret, err := readInteractivePassword(bufio.NewReader(osStdin), prompt)
		if err != nil {
			return "", false, err
		}
		value = secret
	}
	if required && value == "" {
		return "", false, fmt.Errorf("provide --%s or --%s-stdin", name, name)
	}
	if len(value) > maxBytes {
		return "", false, fmt.Errorf("%s exceeds %d bytes", name, maxBytes)
	}
	return value, fromArg, nil
}

func readLimitedStdin(maxBytes int) ([]byte, error) {
	return io.ReadAll(io.LimitReader(osStdin, int64(maxBytes)+1))
}

func validateRealtimeLifecyclePolicy(authMode string, origins realtimeStringList, maxConnections int, maxMessageBytes, maxConnectionAge int64) error {
	if authMode != "" {
		if _, err := api.NormalizeRealtimeAuthMode(authMode, false); err != nil {
			return err
		}
	}
	if len(origins) > 0 {
		if err := api.ValidateRealtimeOrigins([]string(origins)); err != nil {
			return err
		}
	}
	if maxConnections < -1 || maxConnections > api.RealtimeMaxConnections {
		return fmt.Errorf("max-connections must be between 0 and %d", api.RealtimeMaxConnections)
	}
	if maxMessageBytes < -1 || maxMessageBytes > api.RealtimeMessageMaxBytes {
		return fmt.Errorf("max-message-bytes must be between 0 and %d", api.RealtimeMessageMaxBytes)
	}
	if maxConnectionAge < -1 || maxConnectionAge > api.RealtimeMaxConnectionAgeSeconds {
		return fmt.Errorf("max-connection-age-seconds must be between 0 and %d", api.RealtimeMaxConnectionAgeSeconds)
	}
	return nil
}

func cloneRealtimeClaimsFlag(values realtimeClaimsFlag) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func slicePointer[T any](values []T) *[]T { return &values }

func claimsPointer(values map[string]string) *map[string]string { return &values }

func requestHasNoRealtimeChanges(request api.UpdateManagedRealtimeEndpointRequest) bool {
	return request.CallbackURL == nil && request.ConnectPath == nil && request.MessagePath == nil && request.DisconnectPath == nil && request.CallbackAuthToken == nil && request.AuthToken == nil && request.AuthMode == nil && request.AuthIssuer == nil && request.AuthJWKSURL == nil && request.AuthAudience == nil && request.AuthAlgorithms == nil && request.AuthRequiredClaims == nil && request.AllowedOrigins == nil && request.MaxConnections == nil && request.MaxMessageBytes == nil && request.MaxConnectionAgeSeconds == nil && request.Enabled == nil
}

func renderRealtimeLifecycleEndpoint(endpoint api.ManagedRealtimeEndpointResponse) int {
	if jsonOutput {
		return jsonOut(writeJSON(endpoint))
	}
	renderRealtimeEndpoint(osStdout, endpoint)
	return 0
}
