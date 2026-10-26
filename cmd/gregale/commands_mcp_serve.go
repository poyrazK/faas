package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/mcpmanagement"
	"github.com/onebox-faas/faas/pkg/wire"
)

type mcpServeOptions struct {
	adminSocket   string
	lifecycle     *mcpmanagement.Lifecycle
	metricsListen string
	telemetry     *mcpmanagement.Telemetry
	transport     string
	timeout       time.Duration
	allowWrites   bool
	listen        string
	resourceURL   string
	redirects     []string
	onboarding    string
	stateFile     string
	statePostgres string
	hosted        bool
	stateKeyFile  string
	toolsets      []string
}

func parseMCPServeOptions(args []string) (mcpServeOptions, error) {
	var opts mcpServeOptions
	fs := newFlagSet("mcp-serve", flag.ContinueOnError)
	fs.StringVar(&opts.adminSocket, "admin-socket", "", "optional owner-protected Unix socket for listing and revoking HTTP grants")
	fs.StringVar(&opts.metricsListen, "metrics-listen", "", "optional literal loopback IP:port for private management MCP Prometheus metrics")
	fs.StringVar(&opts.transport, "transport", "stdio", "management MCP transport (stdio or streamable-http)")
	fs.DurationVar(&opts.timeout, "timeout", 30*time.Second, "per-tool API timeout (positive, at most 1m)")
	fs.BoolVar(&opts.allowWrites, "allow-writes", false, "enable reviewed release actions; disabled by default")
	fs.StringVar(&opts.listen, "listen", "127.0.0.1:8787", "HTTP listen address behind a TLS reverse proxy")
	fs.StringVar(&opts.resourceURL, "resource-url", "", "public HTTPS URL ending in /mcp for remote OAuth")
	fs.Func("oauth-redirect-uri", "allowed OAuth callback URI; repeat; omit loopback HTTP port for ephemeral client ports", func(value string) error { opts.redirects = append(opts.redirects, value); return nil })
	fs.StringVar(&opts.onboarding, "oauth-onboarding", "browser", "remote onboarding: browser sign-in or explicit api-key fallback")
	fs.StringVar(&opts.stateFile, "state-file", "", "private encrypted remote state file for restart-safe connections")
	fs.BoolVar(&opts.hosted, "hosted", false, "serve as Gregale's hosted bridge: path issuer under /mcp; requires --state-postgres")
	fs.StringVar(&opts.statePostgres, "state-postgres", "", "keep remote state in Postgres under the named bridge lease (DATABASE_URL or FAAS_DATABASE_URL)")
	fs.StringVar(&opts.stateKeyFile, "state-key-file", "", "independent private file with the 32-byte state encryption key")
	tools := fs.String("tools", mcpmanagement.ToolsetAll, "toolsets to list, comma-separated: core, release, diagnose, provision or all")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	toolsets, err := mcpmanagement.ParseToolsets(*tools)
	if err != nil {
		return opts, err
	}
	opts.toolsets = toolsets
	if fs.NArg() != 0 || (opts.transport != "stdio" && opts.transport != "streamable-http") || opts.timeout <= 0 || opts.timeout > time.Minute {
		return opts, errors.New("use --transport stdio or streamable-http and a positive --timeout up to 1m; no positional arguments are accepted")
	}
	if opts.onboarding != "browser" && opts.onboarding != "api-key" {
		return opts, errors.New("--oauth-onboarding must be browser or api-key")
	}
	if opts.stateFile != "" && opts.statePostgres != "" {
		return opts, errors.New("use --state-file or --state-postgres, not both")
	}
	if (opts.stateFile == "" && opts.statePostgres == "") != (opts.stateKeyFile == "") {
		return opts, errors.New("--state-file or --state-postgres and --state-key-file must be supplied together")
	}
	if opts.hosted && (opts.transport != "streamable-http" || opts.statePostgres == "" || opts.onboarding != "browser") {
		return opts, errors.New("--hosted needs --transport streamable-http, --state-postgres and browser onboarding")
	}
	if opts.transport == "stdio" && (opts.adminSocket != "" || opts.onboarding != "browser" || opts.stateFile != "" || opts.statePostgres != "" || opts.resourceURL != "" || len(opts.redirects) > 0 || opts.listen != "127.0.0.1:8787") {
		return opts, errors.New("remote OAuth and listen options require --transport streamable-http")
	}
	if opts.adminSocket != "" {
		if err := mcpmanagement.ValidateAdminSocketPath(opts.adminSocket); err != nil {
			return opts, err
		}
	}
	if err := validateMCPMetricsListen(opts.metricsListen); err != nil {
		return opts, err
	}
	return opts, nil
}

func cmdMCPServe(args []string) int {
	opts, err := parseMCPServeOptions(args)
	if err != nil {
		return printErr("MCP serve", err)
	}
	if opts.metricsListen != "" {
		opts.telemetry = mcpmanagement.NewTelemetry()
		closeMetrics, err := startMCPMetrics(opts.metricsListen, opts.telemetry)
		if err != nil {
			return printErr("MCP metrics", err)
		}
		defer closeMetrics()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if opts.transport == "streamable-http" {
		err = serveMCPHTTP(ctx, opts)
	} else {
		err = serveMCPStdio(ctx, opts)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return printErr("MCP serve", err)
	}
	return 0
}

func serveMCPStdio(ctx context.Context, opts mcpServeOptions) error {
	if opts.lifecycle == nil {
		opts.lifecycle = mcpmanagement.NewLifecycle()
	}
	c, err := authedClient()
	if err != nil {
		return err
	}
	connection := effectiveConnectionContext()
	s, err := mcpmanagement.NewServer(c, mcpmanagement.Options{
		Lifecycle: opts.lifecycle, Telemetry: opts.telemetry, Profile: connection.Profile, APIBase: connection.APIBase,
		Version: wire.Version, Token: c.Token(), Timeout: opts.timeout, AllowWrites: opts.allowWrites,
		LocalCLI: mcpLocalCLI(connection.Profile, connection.APIBase, c.Token()), Toolsets: opts.toolsets,
	})
	if err != nil {
		return err
	}
	return runMCPStdio(ctx, s, opts.lifecycle)
}

func serveMCPHTTP(ctx context.Context, opts mcpServeOptions) error {
	if opts.lifecycle == nil {
		opts.lifecycle = mcpmanagement.NewLifecycle()
	}
	connection := effectiveConnectionContext()
	handlerOptions := mcpmanagement.HTTPOptions{Lifecycle: opts.lifecycle, Telemetry: opts.telemetry, AuditLogger: slog.New(slog.NewJSONHandler(osStderr, nil)), Onboarding: opts.onboarding, APIBase: connection.APIBase, ResourceURL: opts.resourceURL, RedirectURIs: opts.redirects, Version: wire.Version, Timeout: opts.timeout, AllowWrites: opts.allowWrites, Toolsets: opts.toolsets, Hosted: opts.hosted}
	if opts.stateFile != "" {
		store, err := mcpmanagement.OpenStateStore(opts.stateFile, opts.stateKeyFile)
		if err != nil {
			return err
		}
		defer func() {
			opts.lifecycle.BeginDrain()
			if opts.lifecycle.Drained() {
				_ = store.Close()
			}
		}()
		handlerOptions.StateStore = store
	}
	if opts.statePostgres != "" {
		// Losing the lease stops this process; the successor holds the state
		// (ADR-20261026195952).
		var cancel context.CancelCauseFunc
		ctx, cancel = context.WithCancelCause(ctx)
		defer cancel(nil)
		pool, err := db.OpenWithAppName(ctx, "", "faas-mcp-bridge")
		if err != nil {
			return err
		}
		defer pool.Close()
		store, err := mcpmanagement.OpenPostgresStateStore(ctx, mcpmanagement.PostgresStateOptions{Pool: pool, Bridge: opts.statePostgres, KeyPath: opts.stateKeyFile, OnLeaseLost: func(err error) { cancel(err) }})
		if err != nil {
			return err
		}
		defer func() {
			opts.lifecycle.BeginDrain()
			if opts.lifecycle.Drained() {
				_ = store.Close()
			}
		}()
		handlerOptions.StateStore = store
	}
	service, err := mcpmanagement.NewHTTPService(handlerOptions)
	if err != nil {
		return err
	}
	var adminServer *http.Server
	if opts.adminSocket != "" {
		adminServer, err = startMCPAdmin(opts.adminSocket, service.AdminHandler())
		if err != nil {
			return err
		}
		defer func() { _ = adminServer.Close() }()
	}
	listener, err := net.Listen("tcp", opts.listen)
	if err != nil {
		return fmt.Errorf("listen for management MCP: %w", err)
	}
	defer func() { _ = listener.Close() }()
	server := &http.Server{Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 2*opts.timeout + 15*time.Second, IdleTimeout: time.Minute}
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	_, _ = fmt.Fprintf(osStderr, "Gregale management MCP listening on %s; public resource %s\n", listener.Addr(), opts.resourceURL)
	select {
	case err := <-result:
		if cleanup := shutdownMCPHTTP(ctx, server, opts.lifecycle, adminServer); cleanup != nil {
			return cleanup
		}
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		if err := shutdownMCPHTTP(ctx, server, opts.lifecycle, adminServer); err != nil {
			return err
		}
		return context.Cause(ctx)
	}
}
