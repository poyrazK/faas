// Command githubd — GitHub App integration daemon (spec §14 M7.5, ADR-012).
//
// githubd owns: push-webhook receiver, Checks-API status writer, OAuth
// callback handler, per-repo install-token cache. It is the SOLE outbound
// caller to api.github.com (Checks + install-token exchange); its inbound
// public surface is gatewayd at /webhooks/github (HMAC-verified at the
// edge). It talks to apid over gRPC on /run/faas/githubd.sock
// (ADR-015 unix-socket DAC; apid is the only caller in v1.0).
//
// Slice 7 wires the daemon body (gRPC + HTTP listeners). Slice 8
// arms the OAuth + token-cache + Checks path: builds an AppAuth
// from /etc/faas/secrets/github-app.{id,pem}, a TokenCache for
// installation access tokens, a ChecksAPI for the Checks writer,
// and a RealService that implements the full gRPC contract.
package main

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// runDeps is the DI seam so tests can swap openDB / configPath /
// AppAuth / readKeyPEM without touching Postgres, /run/faas, or
// /etc/faas/secrets.
type runDeps struct {
	configPath string
	openDB     func(context.Context, string) (*pgxpool.Pool, error)
	readAppID  func() string
	readKeyPEM func() ([]byte, error)
	httpClient func() githubd.HTTPClient
	now        func() time.Time
}

func defaultDeps() runDeps {
	return runDeps{
		configPath: "/etc/faas/githubd.toml",
		openDB:     db.Open,
		readAppID:  func() string { return os.Getenv("FAAS_GITHUB_APP_ID") },
		readKeyPEM: readKeyPEMDefault,
		httpClient: func() githubd.HTTPClient { return http.DefaultClient },
		now:        time.Now,
	}
}

func main() {
	wire.Daemon("githubd", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	return runWithDeps(ctx, log, defaultDeps())
}

func runWithDeps(ctx context.Context, log *slog.Logger, deps runDeps) error {
	cfg, err := LoadConfig(deps.configPath)
	if err != nil {
		return fmt.Errorf("githubd: config: %w", err)
	}

	pool, err := deps.openDB(ctx, "")
	if err != nil {
		return fmt.Errorf("githubd: open db: %w", err)
	}
	defer pool.Close()

	// Slice 7 Service skeleton (inbound webhook path).
	webhookSvc := githubd.NewService(log)
	webhookSvc.Bindings = noopBindings{}

	// Slice 8 RealService (OAuth + Checks). Auth may be nil if
	// the GitHub App credentials aren't provisioned — the daemon
	// stays up but every OAuth / Checks call returns an error.
	// This is "fail-closed but stay-up": the webhook path
	// continues to work for any installation that's already
	// configured its webhook out-of-band.
	var realSvc *githubd.RealService
	if appID := deps.readAppID(); appID != "" {
		keyPEM, kerr := deps.readKeyPEM()
		if kerr != nil {
			log.Warn("githubd: read app private key", "err", kerr)
		} else {
			auth, aerr := githubd.NewAppAuth(appID, keyPEM, deps.httpClient())
			if aerr != nil {
				log.Warn("githubd: app auth init", "err", aerr)
			} else {
				tokens := githubd.NewTokenCache(auth, 5*time.Minute)
				// BindingsLookup is the seam that closes review
				// finding #1+#2: pkg/state.Store owns the binding
				// table (migration 00007), and githubd's Checks
				// writer threads the right installation_id per
				// repo through it instead of hardcoding install=1.
				checks, cerr := githubd.NewChecksAPI(tokens, deps.httpClient(), &pgBindingsLookup{pool: pool})
				if cerr != nil {
					return fmt.Errorf("githubd: new checks api: %w", cerr)
				}
				realSvc = githubd.NewRealService(auth, tokens, checks)
				log.Info("githubd: OAuth + Checks wired", "app_id", appID)
			}
		}
	} else {
		log.Info("githubd: FAAS_GITHUB_APP_ID unset; OAuth + Checks disabled (webhook path only)")
	}

	// The gRPC server hands out the RealService (full slice 8
	// surface) when available, else falls back to a Unimplemented
	// stub so the gRPC plumbing stays healthy even without OAuth.
	gRPCImpl := githubdgrpc.Service(githubdgrpc.UnimplementedService{})
	if realSvc != nil {
		gRPCImpl = realSvc
	}

	// ops: one per-daemon Prometheus registry shared by every
	// observer in githubd (gRPC handlers + the inbound webhook
	// push). WebhookLoopbackHandler mounts it at GET /metrics on
	// the loopback :8083 mux (§11 loopback-only invariant; gatewayd
	// only forwards POST /webhooks/github, so GET /metrics can't
	// leak externally).
	ops := wire.NewOpsMetrics("githubd")
	srv := &githubd.Server{
		Service:     webhookSvc,
		Log:         log,
		Ops:         ops,
		GRPCServer:  githubdgrpc.New(gRPCImpl, ops, log),
		HTTPAddr:    cfg.HTTPAddr,
		SocketPath:  cfg.SocketPath,
		ListenAddr:  cfg.ListenAddr,
		TLSCertPath: cfg.TLSCertPath,
		TLSKeyPath:  cfg.TLSKeyPath,
		TLSCAPath:   cfg.TLSCAPath,
	}
	cleanup, errc, err := srv.Start(ctx)
	if err != nil {
		return fmt.Errorf("githubd: start: %w", err)
	}
	//nolint:contextcheck // shutdown ctx must outlive caller ctx.
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = cleanup(shutdownCtx)
	}()

	// Start the token-cache janitor if RealService is armed.
	if realSvc != nil && realSvc.Tokens != nil {
		stopJanitor := realSvc.Tokens.StartJanitor(ctx)
		defer stopJanitor()
	}

	select {
	case err := <-errc:
		return fmt.Errorf("githubd: listener: %w", err)
	case <-ctx.Done():
		log.Info("githubd stopping")
		return nil
	}
}

// noopBindings is a placeholder until slice 8 introduces the
// bindings table. Every GetAppBinding returns an empty struct (the
// service treats empty BindingID as "no binding").
type noopBindings struct{}

func (noopBindings) GetAppBinding(_ context.Context, _, _ string) (githubdgrpc.AppBinding, error) {
	return githubdgrpc.AppBinding{}, nil
}

// readKeyPEMDefault reads the GitHub App private key from
// FAAS_GITHUB_APP_KEY_PATH (default /etc/faas/secrets/github-app.pem,
// mode 0400 per spec §11). Returns an error if the file is missing
// or unreadable.
func readKeyPEMDefault() ([]byte, error) {
	path := os.Getenv("FAAS_GITHUB_APP_KEY_PATH")
	if path == "" {
		path = "/etc/faas/secrets/github-app.pem"
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is operator-controlled
	if err != nil {
		return nil, fmt.Errorf("githubd: read app key %q: %w", path, err)
	}
	return data, nil
}

// Compile-time guards: keep imports stable for tests / future slices.
var (
	_ = rsa.PrivateKey{}
	_ = depsAdapter{}
)

// depsAdapter is reserved for the test seam in pkg/githubd tests
// that import cmd/githubd internals.
type depsAdapter struct{}

// pgBindingsLookup bridges pgxpool.Pool to the githubd.BindingsLookup
// interface so ChecksAPI can resolve repoFullName → installation_id
// without importing pkg/state directly (slice 8 architectural seam:
// githubd stays persistence-agnostic; apid owns the bindings table).
//
// Lives in cmd/githubd because that's where pgxpool is already
// in scope; the actual SQL lives in pkg/state.PgStore.InstallationIDForRepo
// (used by apid). githubd would otherwise need a parallel query or
// a new pkg/state dependency, neither of which is justified for a
// single read-only lookup. When apid's HTTP-side OAuth work lands
// (commit 1), this adapter can be replaced with an HTTP call to
// apid instead, removing the duplicate query.
type pgBindingsLookup struct {
	pool *pgxpool.Pool
}

func (p *pgBindingsLookup) InstallationIDForRepo(ctx context.Context, repoFullName string) (int64, error) {
	if repoFullName == "" {
		return 0, fmt.Errorf("githubd: repoFullName required")
	}
	var installID int64
	err := p.pool.QueryRow(ctx,
		`select github_install_id
		 from apps
		 where github_repo_full_name = $1
		   and github_install_id is not null
		 limit 1`, repoFullName,
	).Scan(&installID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, githubd.ErrNoBinding
		}
		return 0, err
	}
	return installID, nil
}
