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
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// runDeps is the DI seam so tests can swap openDB / listen /
// AppAuth / readKeyPEM without touching Postgres, /run/faas, or
// /etc/faas/secrets.
type runDeps struct {
	openDB     func(context.Context, string) (*pgxpool.Pool, error)
	readAppID  func() string
	readKeyPEM func() ([]byte, error)
	httpClient func() githubd.HTTPClient
	now        func() time.Time
}

func defaultDeps() runDeps {
	return runDeps{
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
				checks := githubd.NewChecksAPI(tokens, deps.httpClient())
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

	srv := &githubd.Server{
		Service:    webhookSvc,
		Log:        log,
		GRPCServer: githubdgrpc.New(gRPCImpl, wire.NewOpsMetrics("githubd"), log),
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
