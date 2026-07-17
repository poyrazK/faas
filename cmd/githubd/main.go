// Command githubd — GitHub App integration daemon (spec §14 M7.5, ADR-012).
//
// githubd owns: push-webhook receiver, Checks-API status writer, OAuth
// callback handler, per-repo install-token cache. It is the SOLE outbound
// caller to api.github.com (Checks + install-token exchange); its inbound
// public surface is gatewayd at /webhooks/github (HMAC-verified at the
// edge). It talks to apid over gRPC on /run/faas/githubd.sock
// (ADR-015 unix-socket DAC; apid is the only caller in v1.0).
//
// Slice 7 wires the daemon body: opens Postgres (read-only via the
// pgx pool, for the bindings table that slice 8 adds), starts the
// gRPC server on /run/faas/githubd.sock, starts the loopback HTTP
// webhook listener on 127.0.0.1:8083, and serves both until ctx
// cancels. OAuth + token-cache + Checks writer land in slice 8.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// runDeps is the DI seam so tests can swap openDB / listen without
// touching Postgres or /run/faas.
type runDeps struct {
	openDB func(context.Context, string) (*pgxpool.Pool, error)
}

func defaultDeps() runDeps {
	return runDeps{openDB: db.Open}
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

	// The bindings store is read-only in slice 7 — slice 8 adds
	// the table that backs it. The noop placeholder keeps the
	// daemon healthy even before slice 8 lands so deployment of
	// slice 7 doesn't have to wait for schema work.
	svc := githubd.NewService(log)
	svc.Bindings = noopBindings{}
	// CreateDeployment and WriteCheck are nil in slice 7 — the
	// HTTP handler refuses every webhook today (secret is nil
	// until slice 8 wires the per-account secret store). This is
	// "closed by default" — slice 7 ships the wiring, slice 8
	// arms it.

	srv := &githubd.Server{
		Service: svc,
		Log:     log,
		GRPCServer: githubdgrpc.New(
			grpcAdapter(svc),
			wire.NewOpsMetrics("githubd"),
			log,
		),
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

// grpcAdapter builds the gRPC Service implementation. Slice 7
// returns Unimplemented for every method except CreateDeploymentFromPush
// and WriteCheck (which the inbound webhook path doesn't use; they
// are no-op stubs so the gRPC surface stays compilable).
func grpcAdapter(svc *githubd.Service) githubdgrpc.Service {
	return &grpcServiceAdapter{svc: svc}
}

type grpcServiceAdapter struct {
	githubdgrpc.UnimplementedService

	svc *githubd.Service
}

func (a *grpcServiceAdapter) CreateDeploymentFromPush(repoFullName, ref, commitSHA, pusher string) (string, string, error) {
	a.svc.Log.Info("githubd grpc CreateDeploymentFromPush (slice-7 stub)",
		"repo", repoFullName, "ref", ref, "sha", commitSHA, "pusher", pusher)
	return "", "", nil
}

func (a *grpcServiceAdapter) WriteCheck(repoFullName, commitSHA string, phase githubdgrpc.CheckPhase, _, summary string) error {
	a.svc.Log.Info("githubd grpc WriteCheck (slice-7 stub)",
		"repo", repoFullName, "sha", commitSHA, "phase", phase, "summary", summary)
	return nil
}

// errNoop is reserved for the closed-by-default secret path.
var errNoop = errors.New("githubd: not configured")

var _ = errNoop // reserved for slice 8 wiring
