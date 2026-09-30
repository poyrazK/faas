package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
)

func cmdDevBridge(args []string) (exit int) {
	if len(args) > 0 {
		switch args[0] {
		case "list", "status", "revoke", "doctor":
			return cmdDevBridgeControl(args)
		}
	}
	if len(args) == 0 {
		return printErr("Missing service", errors.New("usage: gregale dev bridge APP --environment development --local-port 8080"))
	}
	app := args[0]
	var command []string
	for i, argument := range args {
		if argument == "--" {
			command, args = args[i+1:], args[:i]
			if len(command) == 0 {
				return printErr("Missing local command", errors.New("provide a command after --"))
			}
			break
		}
	}
	fs := newFlagSet("dev bridge", flag.ContinueOnError)
	environment := fs.String("environment", "development", "named development environment")
	port := fs.Int("local-port", 8080, "local HTTP service port")
	dependencies := fs.String("dependencies", "", "comma-separated remote dependency app names (default: declared bindings)")
	entrypoint := fs.String("entrypoint", "", "remote frontend for the session URL (default: intercepted service)")
	inspect := fs.Bool("inspect", false, "open a local request inspection endpoint")
	replayWebhook := fs.String("replay-webhook", "", "copy one provider-verified webhook receipt to the local service")
	readyPath := fs.String("ready-path", "", "local HTTP readiness path (default: check TCP listener)")
	bindings := make(map[string]string)
	fs.Func("bind-env", "map ENV_KEY=dependency to its loopback URL; repeatable with a local command", func(value string) error {
		key, dependency, ok := strings.Cut(value, "=")
		if !ok || !bridgeEnvKey(key) || dependency == "" {
			return errors.New("bind-env requires ENV_KEY=dependency")
		}
		if _, exists := bindings[key]; exists {
			return errors.New("duplicate bind-env key")
		}
		bindings[key] = dependency
		return nil
	})
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *port < 1 || *port > 65535 {
		return printErr("Invalid bridge options", errors.New("local-port must be 1–65535"))
	}
	if (*readyPath != "" && (!strings.HasPrefix(*readyPath, "/") || strings.HasPrefix(*readyPath, "//"))) || (len(bindings) > 0 && len(command) == 0) {
		return printErr("Invalid bridge options", errors.New("ready-path must be absolute; bind-env requires a command after --"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	base, err := url.Parse(client.BaseURL())
	if err != nil {
		return printErr("Invalid API URL", err)
	}
	if base.Scheme != "https" {
		ip := net.ParseIP(base.Hostname())
		if base.Scheme != "http" || ip == nil || !ip.IsLoopback() {
			return printErr("Invalid API URL", errors.New("bridge credentials require HTTPS"))
		}
	}
	developer, err := loadOrCreateDeveloperID()
	if err != nil {
		return printErr("Developer identity unavailable", err)
	}
	var names []string
	if *dependencies != "" {
		for _, name := range strings.Split(*dependencies, ",") {
			names = append(names, strings.TrimSpace(name))
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), testTerminationSignals()...)
	defer stop()
	createCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	session, err := client.CreateDevBridge(createCtx, api.CreateDevBridgeRequest{App: app, Environment: *environment, DeveloperID: developer, Dependencies: names, Entrypoint: *entrypoint})
	cancel()
	if err != nil {
		return printErr("Could not create development bridge", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := client.RevokeDevBridge(cleanup, session.Session.ID); err != nil {
			_, _ = fmt.Fprintln(osStderr, "Bridge cleanup failed; the session will expire automatically.")
			if exit == 0 {
				exit = 1
			}
		}
	}()
	ctx, cancel = context.WithDeadline(ctx, session.Session.ExpiresAt)
	defer cancel()
	attach := *base
	attach.Path = strings.TrimRight(base.Path, "/") + "/v1/dev/bridges/" + url.PathEscape(session.Session.ID) + "/connect"
	if attach.Scheme == "https" {
		attach.Scheme = "wss"
	} else {
		attach.Scheme = "ws"
	}
	headers := http.Header{devbridge.AccountHeader: []string{session.Session.Scope.AccountID}, devbridge.TokenHeader: []string{session.Credentials.AttachmentToken}}
	dialer := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 15 * time.Second, Subprotocols: []string{"gregale-dev-bridge-v1"}}
	// A loopback session URL attaches routing credentials without exposing them
	// to the browser, terminal output or a query string.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return printErr("Could not start session proxy", err)
	}
	defer func() { _ = listener.Close() }()
	environmentOrigin, err := url.Parse(session.EnvironmentURL)
	if err != nil || environmentOrigin.Host == "" {
		return printErr("Invalid environment URL", errors.New("server returned an invalid environment URL"))
	}
	requestContext := (devbridge.RequestContext{AccountID: session.Session.Scope.AccountID, SessionID: session.Session.ID, Token: session.Credentials.RequestToken}).Encode()
	localProxy := bridgeLocalProxy(environmentOrigin, session.Session, session.Credentials.RequestToken, "", requestContext)
	sessionServer := &http.Server{Handler: localProxy, ReadHeaderTimeout: 10 * time.Second}
	defer func() { _ = sessionServer.Close() }()
	go func() { _ = sessionServer.Serve(listener) }()
	dependencyURLs := make(map[string]string)
	for _, dependency := range session.Dependencies {
		dependencyListener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return printErr("Could not start dependency proxy", err)
		}
		dependencyServer := &http.Server{Handler: bridgeLocalProxy(base, session.Session, session.Credentials.AttachmentToken, "dependencies/"+url.PathEscape(dependency.AppID)+"/", requestContext), ReadHeaderTimeout: 10 * time.Second}
		defer func() { _ = dependencyServer.Close() }()
		go func() { _ = dependencyServer.Serve(dependencyListener) }()
		dependencyURLs[dependency.Name] = "http://" + dependencyListener.Addr().String()
		_, _ = fmt.Fprintf(osStdout, "Dependency %s: http://%s\n", dependency.Name, dependencyListener.Addr())
	}
	_, _ = fmt.Fprintf(osStdout, "Bridge: %s (%s) → 127.0.0.1:%d\nSession: %s\nSession URL: http://%s\nExpires: %s\n", app, *environment, *port, session.Session.ID, listener.Addr(), session.Session.ExpiresAt.Format(time.RFC3339))
	var inspector *devbridge.Inspector
	if *inspect {
		inspector = devbridge.NewInspector(api.DevBridgeInspectionRecords, api.DevBridgeInspectionPathBytes)
		inspectionListener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return printErr("Could not start request inspection", err)
		}
		inspectionServer := &http.Server{Handler: bridgeLoopbackOnly(bridgeInspectionHandler(inspector)), ReadHeaderTimeout: 10 * time.Second}
		defer func() { _ = inspectionServer.Close() }()
		go func() { _ = inspectionServer.Serve(inspectionListener) }()
		_, _ = fmt.Fprintf(osStdout, "Inspection URL: http://%s\n", inspectionListener.Addr())
	}
	var process *bridgeProcess
	if len(command) > 0 {
		environment, err := bridgeProcessEnvironment(os.Environ(), *port, session.Session.ID, "http://"+listener.Addr().String(), dependencyURLs, bindings)
		if err != nil {
			return printErr("Invalid local dependency configuration", err)
		}
		process, err = startBridgeProcess(command, environment)
		if err != nil {
			return printErr("Could not start local command", err)
		}
		defer func() {
			if err := process.stop(); err != nil {
				_, _ = fmt.Fprintln(osStderr, "Local command cleanup failed:", err)
				if exit == 0 {
					exit = 1
				}
			}
		}()
		if err := bridgeWaitLocalReady(ctx, net.JoinHostPort("127.0.0.1", fmt.Sprint(*port)), *readyPath, process); err != nil {
			select {
			case <-process.done:
				if code := process.exitCode(); code != 0 {
					return code
				}
				return printErr("Local command exited before becoming ready", err)
			default:
				return printErr("Local service is not ready", err)
			}
		}
	}
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", *port))
	dial := func(ctx context.Context) (net.Conn, error) {
		socket, response, err := dialer.DialContext(ctx, attach.String(), headers)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if err != nil {
			if response != nil && (response.StatusCode == 401 || response.StatusCode == 403 || response.StatusCode == 404 || response.StatusCode == 410) {
				return nil, devbridge.ErrUnauthorized
			}
			return nil, devbridge.ErrDisconnected
		}
		return devbridge.NewWebSocketConn(socket), nil
	}
	serve := func(ctx context.Context, socket net.Conn) error {
		return devbridge.ServeLocal(ctx, socket, target, api.DevBridgeMaxConcurrentRequests, inspector)
	}
	var replayOnce sync.Once
	state := func(connected bool) {
		if ctx.Err() != nil {
			return
		}
		if connected {
			_, _ = fmt.Fprintln(osStdout, "Bridge connected.")
			if *replayWebhook != "" {
				replayOnce.Do(func() { go bridgeReplaySelectedWebhook(ctx, client, base, session, *replayWebhook) })
			}
		} else {
			_, _ = fmt.Fprintln(osStderr, "Bridge disconnected; reconnecting within the session lease. Run gregale dev bridge doctor for connection diagnostics.")
		}
	}
	if process != nil {
		go func() {
			select {
			case <-process.done:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	if err := devbridge.RunReconnecting(ctx, dial, serve, state); err != nil && ctx.Err() == nil {
		return printErr("Development bridge disconnected", err)
	}
	if process != nil {
		select {
		case <-process.done:
			return process.exitCode()
		default:
		}
	}
	return 0
}

func bridgeReplaySelectedWebhook(ctx context.Context, client *api.Client, base *url.URL, session api.CreateDevBridgeResponse, invocation string) {
	ready, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	status := *base
	status.Path = strings.TrimRight(base.Path, "/") + "/v1/dev/bridges/" + session.Session.ID + "/status"
	statusClient := *client.HTTPClient()
	statusClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for ready.Err() == nil {
		request, _ := http.NewRequestWithContext(ready, "GET", status.String(), nil)
		request.Header.Set(devbridge.AccountHeader, session.Session.Scope.AccountID)
		request.Header.Set(devbridge.TokenHeader, session.Credentials.AttachmentToken)
		response, err := statusClient.Do(request)
		connected := false
		if err == nil {
			var value struct {
				Connected bool `json:"connected"`
			}
			_ = json.NewDecoder(response.Body).Decode(&value)
			_ = response.Body.Close()
			connected = response.StatusCode == 200 && value.Connected
		}
		if connected {
			break
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ready.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	if ready.Err() != nil {
		_, _ = fmt.Fprintln(osStderr, "Webhook replay was not sent; the bridge did not become ready.")
		return
	}
	replayCtx, replayCancel := context.WithTimeout(ctx, api.DevBridgeWebhookReplayTimeout+5*time.Second)
	defer replayCancel()
	key := uuid.NewString()
	_, _ = fmt.Fprintf(osStdout, "Webhook replay receipt: %s\n", devbridge.WebhookReplayID(session.Session.ID, key))
	receipt, err := client.ReplayDevBridgeWebhook(replayCtx, session.Session.ID, api.ReplayDevBridgeWebhookRequest{InvocationID: invocation, RequestToken: session.Credentials.RequestToken, IdempotencyKey: key})
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "Webhook replay could not be confirmed; inspect the receipt before sending another copy.")
		return
	}
	_, _ = fmt.Fprintf(osStdout, "Webhook replay %s: %s (HTTP %d)\n", receipt.ID, receipt.State, receipt.HTTPStatus)
}

func bridgeLocalProxy(base *url.URL, session devbridge.Session, credential, route string, requestContext ...string) http.Handler {
	proxy := &httputil.ReverseProxy{Rewrite: func(p *httputil.ProxyRequest) {
		p.Out.URL.Scheme = base.Scheme
		p.Out.URL.Host = base.Host
		if route != "" {
			prefix := strings.TrimRight(base.Path, "/") + "/v1/dev/bridges/" + url.PathEscape(session.ID) + "/" + route
			p.Out.URL.Path = prefix + strings.TrimPrefix(p.In.URL.Path, "/")
			if p.In.URL.RawPath != "" {
				p.Out.URL.RawPath = prefix + strings.TrimPrefix(p.In.URL.EscapedPath(), "/")
			}
		} else {
			p.Out.Header.Set(devbridge.SessionHeader, session.ID)
		}
		p.Out.Host = base.Host
		devbridge.ClearCredentials(p.Out.Header)
		devbridge.ClearRequestContext(p.Out.Header)
		if route == "" {
			p.Out.Header.Set(devbridge.SessionHeader, session.ID)
		}
		if len(requestContext) > 0 {
			p.Out.Header.Set(devbridge.ContextHeader, requestContext[0])
		}
		p.Out.Header.Set(devbridge.AccountHeader, session.Scope.AccountID)
		p.Out.Header.Set(devbridge.TokenHeader, credential)
	}, FlushInterval: -1, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "development bridge unavailable", http.StatusServiceUnavailable)
	}}
	return bridgeLoopbackOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = http.NewResponseController(w).EnableFullDuplex()
		proxy.ServeHTTP(w, r)
	}))
}

func bridgeLoopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin, err := url.Parse("http://" + r.Host)
		if err != nil || net.ParseIP(origin.Hostname()) == nil || !net.ParseIP(origin.Hostname()).IsLoopback() {
			http.Error(w, "session proxy requires a loopback hostname", http.StatusForbidden)
			return
		}
		if supplied := r.Header.Get("Origin"); supplied != "" && supplied != "http://"+r.Host {
			http.Error(w, "session proxy requires a same-origin request", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
