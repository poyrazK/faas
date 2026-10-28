// Command mcp-bridged serves Gregale's hosted management MCP server at
// https://api.<platform domain>/mcp (ADR-20261026195952). gatewayd-public
// routes the server's paths on the api. host to this loopback listener.
//
// Agents' provisioning and deploy tools run the gregale CLI from the active
// release as subprocesses, so a CLI release changes what they run without
// restarting this daemon: only a release of this binary restarts it.
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
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/mcpmanagement"
	"github.com/onebox-faas/faas/pkg/wire"
)

const shutdownGrace = 5 * time.Second

func main() {
	flags := registerFlags(flag.CommandLine)
	wire.Daemon("mcp-bridged", func(ctx context.Context, log *slog.Logger) error {
		cfg, err := flags.config()
		if err != nil {
			return err
		}
		return serve(ctx, log, cfg)
	})
}

type redirectList []string

func (r *redirectList) String() string { return strings.Join(*r, ",") }

func (r *redirectList) Set(value string) error {
	*r = append(*r, value)
	return nil
}

type flags struct {
	listen, apiBase, resourceURL, stateBridge, stateKeyFile, cliPath, uploadDir, tools *string
	timeout                                                                            *time.Duration
	redirects                                                                          redirectList
}

func registerFlags(fs *flag.FlagSet) *flags {
	f := &flags{
		listen:       fs.String("listen", "127.0.0.1:8787", "loopback address gatewayd-public forwards the bridge's paths to"),
		apiBase:      fs.String("api-base", "http://127.0.0.1:8081", "apid's loopback listener"),
		resourceURL:  fs.String("resource-url", "", "public resource URL; defaults to https://api.<platform domain>/mcp"),
		stateBridge:  fs.String("state-bridge", "hosted", "name of the Postgres state lease and rows"),
		stateKeyFile: fs.String("state-key-file", "", "independent private file holding the 32-byte state key"),
		cliPath:      fs.String("cli", "/opt/faas/current/bin/gregale", "gregale CLI the provisioning and deploy tools run"),
		uploadDir:    fs.String("upload-dir", filepath.Join(os.TempDir(), "gregale-mcp-uploads"), "private directory for uploads; emptied on start"),
		tools:        fs.String("tools", "core,provision", "toolsets to list, comma-separated"),
		timeout:      fs.Duration("timeout", 30*time.Second, "per-tool API timeout (positive, at most 1m)"),
	}
	fs.Var(&f.redirects, "oauth-redirect-uri", "allowed OAuth callback URI; repeat; omit a loopback port to allow any")
	return f
}

type config struct {
	listen, apiBase, resourceURL, stateBridge, stateKeyFile, cliPath, uploadDir string
	toolsets                                                                    []string
	timeout                                                                     time.Duration
	redirects                                                                   []string
}

func (f *flags) config() (config, error) {
	cfg := config{listen: *f.listen, apiBase: strings.TrimRight(*f.apiBase, "/"), resourceURL: *f.resourceURL, stateBridge: *f.stateBridge, stateKeyFile: *f.stateKeyFile, cliPath: *f.cliPath, uploadDir: *f.uploadDir, timeout: *f.timeout, redirects: append([]string(nil), f.redirects...)}
	if cfg.resourceURL == "" {
		// The platform's API host, resolved as every daemon does
		// (FAAS_PLATFORM_DOMAIN, else FAAS_APPS_DOMAIN).
		cfg.resourceURL = "https://api." + hostidentity.Configured().Platform + "/mcp"
	}
	host, _, err := net.SplitHostPort(cfg.listen)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		return cfg, errors.New("mcp-bridged: --listen must be a loopback address")
	}
	if cfg.stateKeyFile == "" {
		return cfg, errors.New("mcp-bridged: --state-key-file is required")
	}
	if !filepath.IsAbs(cfg.cliPath) || !filepath.IsAbs(cfg.uploadDir) {
		return cfg, errors.New("mcp-bridged: --cli and --upload-dir must be absolute paths")
	}
	if len(cfg.redirects) == 0 {
		return cfg, errors.New("mcp-bridged: allow at least one --oauth-redirect-uri")
	}
	if cfg.timeout <= 0 || cfg.timeout > time.Minute {
		return cfg, errors.New("mcp-bridged: --timeout must be positive and at most 1m")
	}
	if cfg.toolsets, err = mcpmanagement.ParseToolsets(*f.tools); err != nil {
		return cfg, fmt.Errorf("mcp-bridged: %w", err)
	}
	return cfg, nil
}

func serve(ctx context.Context, log *slog.Logger, cfg config) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	pool, err := db.OpenWithAppName(ctx, "", "faas-mcp-bridge")
	if err != nil {
		return fmt.Errorf("mcp-bridged database: %w", err)
	}
	defer pool.Close()
	// Losing the lease stops this process; its successor holds the state.
	// The control plane generates the state key on first converge; state a
	// rebuilt host's new key cannot open is dropped, and agents sign in again.
	store, err := mcpmanagement.OpenPostgresStateStore(ctx, mcpmanagement.PostgresStateOptions{
		Pool: pool, Bridge: cfg.stateBridge, KeyPath: cfg.stateKeyFile, OnLeaseLost: func(err error) { cancel(err) },
		DiscardUnreadable: true,
		OnDiscard: func(err error) {
			log.Warn("discarded hosted MCP state the state key cannot open; connected agents must sign in again", "err", err)
		},
	})
	if err != nil {
		return err
	}
	lifecycle := mcpmanagement.NewLifecycle()
	service, err := mcpmanagement.NewHTTPService(mcpmanagement.HTTPOptions{
		Lifecycle: lifecycle, AuditLogger: log, Onboarding: "browser", APIBase: cfg.apiBase, ResourceURL: cfg.resourceURL,
		RedirectURIs: cfg.redirects, Version: wire.Version, Timeout: cfg.timeout, AllowWrites: true, Toolsets: cfg.toolsets,
		StateStore: store, Hosted: true, UploadDir: cfg.uploadDir, CLIRunner: mcpmanagement.HostedCLIRunner(cfg.cliPath, cfg.apiBase, cfg.uploadDir),
	})
	if err != nil {
		_ = store.Close()
		return err
	}
	listener, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		_ = store.Close()
		return fmt.Errorf("listen for the hosted MCP server: %w", err)
	}
	server := &http.Server{Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 2*cfg.timeout + 15*time.Second, IdleTimeout: time.Minute}
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	log.Info("serving hosted management MCP", "listen", listener.Addr().String(), "resource", cfg.resourceURL)

	var serveErr error
	select {
	case serveErr = <-result:
	case <-ctx.Done():
	}
	grace, stop := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer stop()
	lifecycle.BeginDrain()
	if server.Shutdown(grace) != nil {
		_ = server.Close()
	}
	cleanup := lifecycle.Shutdown(grace)
	if lifecycle.Drained() {
		_ = store.Close()
	}
	switch {
	case serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed):
		return serveErr
	case cleanup != nil:
		return fmt.Errorf("management MCP handlers did not finish cleanup: %w", cleanup)
	case errors.Is(context.Cause(ctx), mcpmanagement.ErrStateLeaseLost):
		return context.Cause(ctx)
	}
	return nil
}
