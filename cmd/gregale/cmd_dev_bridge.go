package main

import (
	"context"
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
	"time"

	"github.com/gorilla/websocket"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
)

func cmdDevBridge(args []string) int {
	if len(args) == 0 {
		return printErr("Missing service", errors.New("usage: gregale dev bridge APP --environment development --local-port 8080"))
	}
	app := args[0]
	fs := newFlagSet("dev bridge", flag.ContinueOnError)
	environment := fs.String("environment", "development", "named development environment")
	port := fs.Int("local-port", 8080, "local HTTP service port")
	dependencies := fs.String("dependencies", "", "comma-separated remote dependency app names (default: declared bindings)")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *port < 1 || *port > 65535 {
		return printErr("Invalid bridge options", errors.New("local-port must be 1–65535"))
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	createCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	session, err := client.CreateDevBridge(createCtx, api.CreateDevBridgeRequest{App: app, Environment: *environment, DeveloperID: developer, Dependencies: names})
	cancel()
	if err != nil {
		return printErr("Could not create development bridge", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := client.RevokeDevBridge(cleanup, session.Session.ID); err != nil {
			fmt.Fprintln(osStderr, "Bridge cleanup failed; the session will expire automatically.")
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
	socket, _, err := dialer.DialContext(ctx, attach.String(), headers)
	if err != nil {
		return printErr("Could not connect development bridge", errors.New("relay connection failed; inspect bridge configuration and retry"))
	}
	defer socket.Close()
	// A loopback session URL attaches routing credentials without exposing them
	// to the browser, terminal output or a query string.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return printErr("Could not start session proxy", err)
	}
	environmentOrigin, err := url.Parse(session.EnvironmentURL)
	if err != nil || environmentOrigin.Host == "" {
		return printErr("Invalid environment URL", errors.New("server returned an invalid environment URL"))
	}
	localProxy := bridgeLocalProxy(environmentOrigin, session.Session, session.Credentials.RequestToken, "")
	sessionServer := &http.Server{Handler: localProxy, ReadHeaderTimeout: 10 * time.Second}
	defer sessionServer.Close()
	go func() { _ = sessionServer.Serve(listener) }()
	for _, dependency := range session.Dependencies {
		dependencyListener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return printErr("Could not start dependency proxy", err)
		}
		dependencyServer := &http.Server{Handler: bridgeLocalProxy(base, session.Session, session.Credentials.AttachmentToken, "dependencies/"+url.PathEscape(dependency.AppID)+"/"), ReadHeaderTimeout: 10 * time.Second}
		defer dependencyServer.Close()
		go func() { _ = dependencyServer.Serve(dependencyListener) }()
		fmt.Fprintf(osStdout, "Dependency %s: http://%s\n", dependency.Name, dependencyListener.Addr())
	}
	fmt.Fprintf(osStdout, "Bridge connected: %s (%s) → 127.0.0.1:%d\nSession URL: http://%s\nExpires: %s\n", app, *environment, *port, listener.Addr(), session.Session.ExpiresAt.Format(time.RFC3339))
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", *port))
	if err := devbridge.ServeLocal(ctx, devbridge.NewWebSocketConn(socket), target, api.DevBridgeMaxConcurrentRequests); err != nil && ctx.Err() == nil {
		return printErr("Development bridge disconnected", err)
	}
	return 0
}

func bridgeLocalProxy(base *url.URL, session devbridge.Session, credential, route string) http.Handler {
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
		p.Out.Header.Set(devbridge.AccountHeader, session.Scope.AccountID)
		p.Out.Header.Set(devbridge.TokenHeader, credential)
	}, FlushInterval: -1, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "development bridge unavailable", 503)
	}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin, err := url.Parse("http://" + r.Host)
		if err != nil || net.ParseIP(origin.Hostname()) == nil || !net.ParseIP(origin.Hostname()).IsLoopback() {
			http.Error(w, "session proxy requires a loopback hostname", 403)
			return
		}
		if supplied := r.Header.Get("Origin"); supplied != "" && supplied != "http://"+r.Host {
			http.Error(w, "session proxy requires a same-origin request", 403)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}
