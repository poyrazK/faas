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
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolconnection"
	"github.com/onebox-faas/faas/pkg/toolgrant"
)

func cmdMCPToolConnectionBrowser(args []string) int {
	return runMCPToolConnectionBrowser(args, openBrowser)
}

func runMCPToolConnectionBrowser(args []string, launch func(string) error) int {
	fs := newFlagSet("mcp-tool-connection-"+args[0], flag.ContinueOnError)
	id := fs.String("id", "", "exact new guided connection UUID")
	var from string
	if args[0] == "reconnect" {
		fs.StringVar(&from, "from", "", "old guided connection to replace and locally revoke")
	}
	port := fs.Int("callback-port", 0, "IPv4 loopback callback port; 0 chooses a dynamic port")
	timeout := fs.Duration("timeout", api.ToolOAuthAuthorizationLifetime, "browser authorization deadline, at most 5m")
	if fs.Parse(args[1:]) != nil {
		return 1
	}
	if !toolgrant.ValidID(*id) || fs.NArg() != 0 || *port < 0 || *port > api.MCPCatalogOAuthMaxCallbackPort || *timeout <= 0 || *timeout > api.ToolOAuthAuthorizationLifetime || args[0] == "reconnect" && (!toolgrant.ValidID(from) || from == *id) {
		return printErr("MCP execution OAuth", errors.New("provide canonical connection IDs, a valid callback port and a deadline up to 5m"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	parent, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(parent, *timeout)
	defer cancel()
	if from != "" {
		old, e := client.GetToolOAuthConnection(ctx, from)
		if e != nil || !old.Intent.Guided {
			return printErr("MCP execution OAuth", errors.New("reconnect requires an existing guided execution connection"))
		}
		current, e := client.GetApp(ctx, old.Intent.App)
		if e != nil || current.ID != old.AppID || current.ProjectID != old.ProjectID {
			return printErr("MCP execution OAuth", errors.New("application identity changed; review a new connection configuration"))
		}
		created, e := client.PutToolOAuthConnection(ctx, *id, old.Intent)
		if e != nil {
			return printErr("MCP execution OAuth", e)
		}
		if created.AppID != old.AppID || created.ProjectID != old.ProjectID {
			return printErr("MCP execution OAuth", errors.New("application identity changed during reconnect"))
		}
		if _, err = client.RevokeToolOAuthConnection(ctx, from); err != nil {
			return printErr("MCP execution OAuth", err)
		}
	}
	c, err := client.GetToolOAuthConnection(ctx, *id)
	if err != nil || !c.Intent.Guided || toolconnection.ValidateRecord(c) != nil {
		return printErr("MCP execution OAuth", errors.New("create a guided public-client connection before connecting"))
	}
	out, err := runToolBrowserConnection(ctx, client, c, *port, launch)
	if err != nil {
		return printErr("MCP execution OAuth", errors.New("authorization did not complete; inspect connection status before reconnecting with a new UUID and new grants"))
	}
	code := jsonOut(writeJSON(out))
	if code == 0 && out.State != "connected" {
		return 1
	}
	return code
}

func runToolBrowserConnection(ctx context.Context, client *Client, c api.ToolOAuthConnectionRecord, port int, launch func(string) error) (api.ToolOAuthAuthorizationResponse, error) {
	var empty api.ToolOAuthAuthorizationResponse
	if port < 0 || port > api.MCPCatalogOAuthMaxCallbackPort || !c.Intent.Guided || toolconnection.ValidateRecord(c) != nil {
		return empty, errors.New("invalid guided connection")
	}
	if c.Generation > 0 {
		return client.GetToolOAuthAuthorization(ctx, c.ID)
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return empty, err
	}
	defer func() { _ = listener.Close() }()
	redirect := "http://" + listener.Addr().String() + "/callback"
	start, err := client.AuthorizeToolOAuthConnection(ctx, c.ID, api.ToolOAuthAuthorizationRequest{Action: "begin", RedirectURI: redirect})
	if err != nil {
		return empty, err
	}
	if !validToolAuthorizationURL(c, start, redirect) {
		return empty, errors.New("unbound authorization URL")
	}
	callback := make(chan api.ToolOAuthAuthorizationRequest, 1)
	server := &http.Server{ReadHeaderTimeout: api.ToolOAuthCallbackTimeout, WriteTimeout: api.ToolOAuthCallbackTimeout, Handler: toolAuthorizationCallback(listener.Addr().String(), start.CallbackState, c.Intent.Issuer, callback)}
	defer func() { _ = server.Close() }()
	go func() { _ = server.Serve(listener) }()
	if err = launch(start.AuthorizationURL); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Open this URL to authorize the execution connection:\n%s\n", start.AuthorizationURL)
	}
	select {
	case <-ctx.Done():
		return empty, ctx.Err()
	case in := <-callback:
		return client.AuthorizeToolOAuthConnection(ctx, c.ID, in)
	}
}

func sameToolOAuthScopes(wanted []string, raw string) bool {
	got := strings.Fields(raw)
	slices.Sort(wanted)
	slices.Sort(got)
	return slices.Equal(wanted, got)
}

func validToolAuthorizationURL(c api.ToolOAuthConnectionRecord, out api.ToolOAuthAuthorizationResponse, redirect string) bool {
	if out.ConnectionID != c.ID || out.State != "authorization_pending" || !toolconnection.ValidAuthorizationState(out.CallbackState) || out.ExpiresAt == nil || !out.ExpiresAt.After(time.Now()) || out.ExpiresAt.After(time.Now().Add(api.ToolOAuthAuthorizationLifetime)) {
		return false
	}
	u, err := url.Parse(out.AuthorizationURL)
	expected, e := url.Parse(c.Intent.AuthorizationEndpoint)
	if err != nil || e != nil || u.Scheme != expected.Scheme || u.Host != expected.Host || u.Path != expected.Path || u.RawPath != expected.RawPath || u.User != nil || u.Fragment != "" {
		return false
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil || len(q) != 8 || q.Get("response_type") != "code" || q.Get("client_id") != c.Intent.ClientID || q.Get("redirect_uri") != redirect || q.Get("state") != out.CallbackState || q.Get("code_challenge_method") != "S256" || !toolconnection.ValidAuthorizationState(q.Get("code_challenge")) || q.Get("resource") != c.Intent.Resource {
		return false
	}
	for _, v := range q {
		if len(v) != 1 {
			return false
		}
	}
	wanted := append([]string{}, c.Intent.Scopes...)
	return sameToolOAuthScopes(wanted, q.Get("scope"))
}

func toolAuthorizationCallback(host, stateValue, issuer string, callback chan<- api.ToolOAuthAuthorizationRequest) http.Handler {
	var claimed atomic.Bool
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		q, err := url.ParseQuery(r.URL.RawQuery)
		denied := q.Get("error") != ""
		code := q.Get("code")
		iss := q.Get("iss")
		if err != nil || r.Method != http.MethodGet || r.Host != host || r.URL.Path != "/callback" || r.URL.RawPath != "" || len(q["state"]) != 1 || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(stateValue)) != 1 || len(q["iss"]) > 1 || iss != "" && iss != issuer || denied && (len(q["error"]) != 1 || len(q["code"]) != 0) || !denied && (len(q["error"]) != 0 || len(q["code"]) != 1 || code == "" || len(code) > api.MCPCatalogOAuthMaxTokenBytes) {
			http.Error(w, "Invalid authorization callback", http.StatusBadRequest)
			return
		}
		in := api.ToolOAuthAuthorizationRequest{Action: "complete", State: stateValue, Code: code, Denied: denied, Issuer: iss}
		if !claimed.CompareAndSwap(false, true) {
			http.Error(w, "Authorization already received", http.StatusConflict)
			return
		}
		select {
		case callback <- in:
			_, _ = fmt.Fprint(w, "Authorization received. Return to Gregale.")
		default:
			http.Error(w, "Authorization already received", http.StatusConflict)
		}
	})
}
