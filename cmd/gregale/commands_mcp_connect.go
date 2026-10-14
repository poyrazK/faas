package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdMCPMonitorConnect(args []string) int {
	fs := newFlagSet("mcp-monitor-"+args[0], flag.ContinueOnError)
	app := fs.String("app", "", "HTTP MCP app slug")
	role := fs.String("role", "default", "managed OAuth role")
	callbackPort := fs.Int("callback-port", -1, "override a dynamic loopback callback port (0–65535)")
	timeout := fs.Duration("timeout", time.Duration(api.MCPCatalogOAuthFlowSeconds)*time.Second, "browser authorization deadline")
	if fs.Parse(args[1:]) != nil {
		return 1
	}
	if *callbackPort < -1 || *callbackPort > api.MCPCatalogOAuthMaxCallbackPort || !api.ValidAppSlug(*app) || !api.ValidCatalogMonitorRole(*role) || fs.NArg() != 0 || *timeout <= 0 || *timeout > time.Duration(api.MCPCatalogOAuthFlowSeconds)*time.Second {
		return printErr("MCP OAuth flags", errors.New("supply --app, --role and a deadline up to 5m"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	parent, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(parent, *timeout)
	defer cancel()
	snapshot, err := client.GetMCPCatalogMonitorRole(ctx, *app, *role)
	if err != nil {
		return printErr("MCP OAuth", errors.New("could not read role configuration"))
	}
	if snapshot.Config.Observer == nil || snapshot.Config.Observer.OAuth == nil || !snapshot.Config.Observer.OAuth.Guided {
		return printErr("MCP OAuth configuration", errors.New("configure this role with --managed --oauth-guided before using browser connection commands"))
	}
	cfg := snapshot.Config.Observer.OAuth
	if problem := cfg.Validate(); problem != nil {
		return printErr("MCP OAuth configuration", errors.New(problem.Detail))
	}
	port := cfg.CallbackPort
	if *callbackPort != -1 {
		if cfg.CallbackPort != 0 && cfg.CallbackPort != *callbackPort {
			return printErr("MCP OAuth callback", errors.New("--callback-port must match the configured OAuth callback port"))
		}
		port = *callbackPort
	}
	req := api.MCPCatalogOAuthRequest{Role: *role, Revision: snapshot.Config.Revision}
	if args[0] == "provider-check" {
		req.Action = "check"
		out, err := client.MCPCatalogOAuth(ctx, *app, req)
		if err != nil {
			return printErr("MCP OAuth provider check", errors.New("could not check provider metadata; verify platform permissions and configuration"))
		}
		if jsonOutput {
			code := writeJSONStdout(out)
			if code == 0 && (out.State == "unsupported" || out.State == "unavailable") {
				return 1
			}
			return code
		}
		_, _ = fmt.Fprintf(osStdout, "OAuth provider: %s\n", out.State)
		for _, check := range out.Checks {
			_, _ = fmt.Fprintf(osStdout, "  %s: %s %s\n", check.Name, check.State, check.Reason)
		}
		if out.State == "unsupported" || out.State == "unavailable" {
			return 1
		}
		return 0
	}
	if args[0] == "disconnect" {
		req.Action = "disconnect"
		out, err := client.MCPCatalogOAuth(ctx, *app, req)
		if err != nil {
			return printErr("MCP OAuth", errors.New("could not disconnect role"))
		}
		return writeMCPConnection(out)
	}
	out, err := runMCPBrowserConnection(ctx, client, *app, req, openBrowser, port)
	if err != nil {
		return printErr("MCP OAuth", safeMCPConnectionError(err))
	}
	return writeMCPConnection(out)
}
func writeMCPConnection(out api.MCPCatalogOAuthResponse) int {
	if jsonOutput {
		return writeJSONStdout(out)
	}
	_, _ = fmt.Fprintf(osStdout, "OAuth connection: %s\n", out.State)
	if out.ProviderRevocation != "" {
		_, _ = fmt.Fprintf(osStdout, "Provider revocation: %s\n", out.ProviderRevocation)
	}
	return 0
}
func runMCPBrowserConnection(ctx context.Context, client *Client, app string, req api.MCPCatalogOAuthRequest, launch func(string) error, ports ...int) (api.MCPCatalogOAuthResponse, error) {
	var empty api.MCPCatalogOAuthResponse
	port := 0
	if len(ports) > 0 {
		port = ports[0]
	}
	if port < 0 || port > api.MCPCatalogOAuthMaxCallbackPort {
		return empty, errors.New("invalid callback port")
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return empty, err
	}
	defer func() { _ = listener.Close() }()
	req.Action = "begin"
	req.RedirectURI = "http://" + listener.Addr().String() + "/callback"
	begun, err := client.MCPCatalogOAuth(ctx, app, req)
	if err != nil {
		return empty, err
	}
	if begun.State == "unsupported" || begun.State == "unavailable" {
		return empty, errors.New("provider compatibility check failed; run mcp monitor provider-check")
	}
	target, err := url.Parse(begun.AuthorizationURL)
	if err != nil || target.Scheme != "https" || target.User != nil || len(begun.CallbackState) != 43 || target.Query().Get("state") != begun.CallbackState {
		return empty, errors.New("invalid authorization URL")
	}
	callback := make(chan api.MCPCatalogOAuthRequest, 1)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		query, e := url.ParseQuery(r.URL.RawQuery)
		stateValue := query.Get("state")
		code := query.Get("code")
		denied := query.Get("error") != ""
		if e != nil || r.Method != http.MethodGet || r.Host != listener.Addr().String() || r.URL.Path != "/callback" || len(query["state"]) != 1 || subtle.ConstantTimeCompare([]byte(stateValue), []byte(begun.CallbackState)) != 1 || (!denied && (len(query["code"]) != 1 || len(code) == 0 || len(code) > api.MCPCatalogOAuthMaxTokenBytes)) {
			http.Error(w, "Invalid authorization callback", 400)
			return
		}
		complete := api.MCPCatalogOAuthRequest{Action: "complete", Role: req.Role, Revision: begun.Revision, State: stateValue, Code: code, Denied: denied}
		select {
		case callback <- complete:
			_, _ = fmt.Fprint(w, "Authorization received. Return to Gregale.")
		default:
			http.Error(w, "Authorization already received", http.StatusConflict)
		}
	})}
	defer func() { _ = server.Close() }()
	go func() { _ = server.Serve(listener) }()
	if err = launch(begun.AuthorizationURL); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Open this URL to authorize the role:\n%s\n", begun.AuthorizationURL)
	}
	select {
	case <-ctx.Done():
		return empty, ctx.Err()
	case complete := <-callback:
		return client.MCPCatalogOAuth(ctx, app, complete)
	}
}

func safeMCPConnectionError(err error) error {
	var networkError *net.OpError
	if errors.As(err, &networkError) && networkError.Op == "listen" {
		return errors.New("OAuth callback port is unavailable; free it or update the configured port")
	}
	if err != nil && err.Error() == "provider compatibility check failed; run mcp monitor provider-check" {
		return err
	}
	return errors.New("authorization did not complete; check provider settings and secrets:read permission, then reconnect")
}
