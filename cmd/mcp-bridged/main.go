// Command mcp-bridged serves Gregale's hosted management MCP server at
// https://api.<platform domain>/mcp (ADR-20261026195952). gatewayd-public
// routes the server's paths on the api. host to this loopback listener.
//
// Agents' provisioning and deploy tools run the gregale CLI from the active
// release as subprocesses, so a CLI release changes what they run without
// restarting this daemon: only a release of this binary restarts it.
//
// Operators read its metrics on --metrics-listen and list or revoke agent
// connections through the owner-only --admin-socket with gregale mcp
// connections, run as the service user.
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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	metricsListen, adminSocket                                                         *string
	timeout                                                                            *time.Duration
	redirects                                                                          redirectList
}

func registerFlags(fs *flag.FlagSet) *flags {
	f := &flags{
		listen:        fs.String("listen", "127.0.0.1:8787", "loopback address gatewayd-public forwards the bridge's paths to"),
		apiBase:       fs.String("api-base", "http://127.0.0.1:8081", "apid's loopback listener"),
		resourceURL:   fs.String("resource-url", "", "public resource URL; defaults to https://api.<platform domain>/mcp"),
		stateBridge:   fs.String("state-bridge", "hosted", "name of the Postgres state lease and rows"),
		stateKeyFile:  fs.String("state-key-file", "", "independent private file holding the 32-byte state key"),
		cliPath:       fs.String("cli", "/opt/faas/current/bin/gregale", "gregale CLI the provisioning and deploy tools run"),
		uploadDir:     fs.String("upload-dir", filepath.Join(os.TempDir(), "gregale-mcp-uploads"), "private directory for uploads; emptied on start"),
		tools:         fs.String("tools", "core,provision", "toolsets to list, comma-separated"),
		timeout:       fs.Duration("timeout", 30*time.Second, "per-tool API timeout (positive, at most 1m)"),
		metricsListen: fs.String("metrics-listen", "127.0.0.1:9110", "loopback address for Prometheus metrics; empty disables them"),
		adminSocket:   fs.String("admin-socket", "", "owner-only Unix socket for listing and revoking connections; its directory must be the service user's, mode 0700"),
	}
	fs.Var(&f.redirects, "oauth-redirect-uri", "allowed OAuth callback URI; repeat; omit a loopback port to allow any")
	return f
}

type config struct {
	listen, apiBase, resourceURL, stateBridge, stateKeyFile, cliPath, uploadDir string
	metricsListen, adminSocket                                                  string
	toolsets                                                                    []string
	timeout                                                                     time.Duration
	redirects                                                                   []string
}

func (f *flags) config() (config, error) {
	cfg := config{listen: *f.listen, apiBase: strings.TrimRight(*f.apiBase, "/"), resourceURL: *f.resourceURL, stateBridge: *f.stateBridge, stateKeyFile: *f.stateKeyFile, cliPath: *f.cliPath, uploadDir: *f.uploadDir, metricsListen: *f.metricsListen, adminSocket: *f.adminSocket, timeout: *f.timeout, redirects: append([]string(nil), f.redirects...)}
	if cfg.resourceURL == "" {
		// The platform's API host, resolved as every daemon does
		// (FAAS_PLATFORM_DOMAIN, else FAAS_APPS_DOMAIN).
		cfg.resourceURL = "https://api." + hostidentity.Configured().Platform + "/mcp"
	}
	if !loopback(cfg.listen) {
		return cfg, errors.New("mcp-bridged: --listen must be a loopback address")
	}
	if cfg.metricsListen != "" && !loopback(cfg.metricsListen) {
		return cfg, errors.New("mcp-bridged: --metrics-listen must be a loopback address")
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
	var err error
	if cfg.toolsets, err = mcpmanagement.ParseToolsets(*f.tools); err != nil {
		return cfg, fmt.Errorf("mcp-bridged: %w", err)
	}
	return cfg, nil
}

func loopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback()
}

func serve(ctx context.Context, log *slog.Logger, cfg config) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	pool, err := db.OpenWithAppName(ctx, "", "faas-mcp-bridge")
	if err != nil {
		return fmt.Errorf("mcp-bridged database: %w", err)
	}
	defer pool.Close()
	// Losing the lease stops this process; its successor holds the state. So
	// does a write that cannot commit: systemd restarts the bridge, which
	// reloads what committed. The control plane generates the state key on
	// first converge; state a rebuilt host's new key cannot open is dropped,
	// and agents sign in again.
	store, err := mcpmanagement.OpenPostgresStateStore(ctx, mcpmanagement.PostgresStateOptions{
		Pool: pool, Bridge: cfg.stateBridge, KeyPath: cfg.stateKeyFile, OnFailure: func(err error) { cancel(err) },
		DiscardUnreadable: true,
		OnDiscard: func(err error) {
			log.Warn("discarded hosted MCP state the state key cannot open; connected agents must sign in again", "err", err)
		},
	})
	if err != nil {
		return err
	}
	lifecycle := mcpmanagement.NewLifecycle()
	telemetry := mcpmanagement.NewTelemetry()
	service, err := mcpmanagement.NewHTTPService(mcpmanagement.HTTPOptions{
		Lifecycle: lifecycle, Telemetry: telemetry, AuditLogger: log, Onboarding: "browser", APIBase: cfg.apiBase, ResourceURL: cfg.resourceURL,
		RedirectURIs: cfg.redirects, Version: wire.Version, Timeout: cfg.timeout, AllowWrites: true, Toolsets: cfg.toolsets,
		StateStore: store, Hosted: true, UploadDir: cfg.uploadDir, CLIRunner: mcpmanagement.HostedCLIRunner(cfg.cliPath, cfg.apiBase, cfg.uploadDir),
	})
	if err != nil {
		_ = store.Close()
		return err
	}
	side, err := startSideListeners(log, cfg, telemetry, service)
	if err != nil {
		_ = store.Close()
		return err
	}
	defer side.close()
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
	case errors.Is(context.Cause(ctx), mcpmanagement.ErrStateLeaseLost), errors.Is(context.Cause(ctx), mcpmanagement.ErrStateCommitFailed):
		return context.Cause(ctx)
	}
	return nil
}

// sideListeners are the operator endpoints: metrics on loopback TCP and the
// connection admin API on an owner-only Unix socket.
type sideListeners struct {
	servers []*http.Server
	addrs   map[string]string
}

func (s *sideListeners) close() {
	for _, server := range s.servers {
		_ = server.Close()
	}
}

func startSideListeners(log *slog.Logger, cfg config, telemetry *mcpmanagement.Telemetry, service *mcpmanagement.RemoteService) (*sideListeners, error) {
	side := &sideListeners{addrs: map[string]string{}}
	if cfg.metricsListen != "" {
		listener, err := net.Listen("tcp", cfg.metricsListen)
		if err != nil {
			return nil, fmt.Errorf("listen for hosted MCP metrics: %w", err)
		}
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", metricsHandler(telemetry))
		side.serve(log, "metrics", listener, mux)
	}
	if cfg.adminSocket != "" {
		listener, err := mcpmanagement.ListenAdminSocket(cfg.adminSocket)
		if err != nil {
			side.close()
			return nil, fmt.Errorf("hosted MCP admin socket: %w", err)
		}
		side.serve(log, "admin", listener, service.AdminHandler())
	}
	return side, nil
}

func (s *sideListeners) serve(log *slog.Logger, name string, listener net.Listener, handler http.Handler) {
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	s.servers = append(s.servers, server)
	s.addrs[name] = listener.Addr().String()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("hosted MCP listener stopped", "listener", name, "err", err)
		}
	}()
	log.Info("serving hosted MCP "+name, "listen", listener.Addr().String())
}

// metricsHandler adds the process's Go and resource metrics to the bridge's
// own aggregate registry, which carries no account labels.
func metricsHandler(telemetry *mcpmanagement.Telemetry) http.Handler {
	process := prometheus.NewRegistry()
	process.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return promhttp.HandlerFor(prometheus.Gatherers{telemetry.Gatherer(), process}, promhttp.HandlerOpts{})
}
