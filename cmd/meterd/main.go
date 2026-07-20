// Command meterd — metering, billing, and quota enforcement (spec §4.7).
//
// meterd owns three timers that share one Postgres-backed state.Store:
//
//   - sample tick: every 60 s, walks every app's live instances and writes
//     one minute of billable usage (plan RAM + 8 MB) to usage_minutes.
//     The billable unit is the admission-time RAM, not the cgroup RSS —
//     spec §4.7 / CLAUDE.md invariant.
//   - quota tick: every 60 s, walks every account and applies the
//     per-plan ladder: Free at ≥100 % flips the account to suspended
//     and parks every live instance; paid plans emit a one-shot
//     quota_warning and accrue overage.
//   - stripe tick: every 60 m, pushes the past hour's GB-hours to
//     Stripe as a metered usage record (spec §4.7, ADR-010).
//
// meterd is the ONLY writer that triggers Free-tier hard stops — apid's
// auth gate and schedd's ledger just observe the resulting status.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/stripex"
	"github.com/onebox-faas/faas/pkg/wire"
)

// parkInstanceParker is the slice of scheddgrpc.Client meterd actually
// uses. Slice 4 adds ParkInstance to scheddgrpc; in tests we inject a
// recording stub. Defining the interface here keeps meterd independent
// of pkg/scheddgrpc until the surface exists (ADR-019).
type parkInstanceParker interface {
	ParkInstance(ctx context.Context, instanceID, reason string) error
}

// stripePusher is the Slice-2 stripex.Client interface meterd uses.
// We don't import pkg/stripex here — the daemon is testable against
// a recorder, and the wire-up in main.go is one line.
type stripePusher interface {
	PushUsageRecord(ctx context.Context, account state.Account, hour time.Time, gbHours float64) error
}

func main() {
	wire.Daemon("meterd", run)
}

// runDeps is the dependency-injection seam for tests.
type runDeps struct {
	configPath string
	openDB     func(context.Context, string) (*pgxpool.Pool, error)
	migrate    func(context.Context, *pgxpool.Pool) error
	loadMeter  func(*Config) (*meter.Config, error)
	// getenv is the env reader the wire-up uses (FAAS_SCHEDD_ADDR,
	// STRIPE_API_KEY, FAAS_QUOTA_INTERVAL, ...). Tests can stub it.
	// Mirrors cmd/apid/main.go's getenv on its runDeps.
	getenv func(string) string
	// dialSchedd is the constructor for the schedd gRPC client. nil in
	// production (defaultDeps wires scheddgrpc.Dial); tests inject a
	// fake to avoid touching the unix socket.
	dialSchedd func(socketPath string) (parkInstanceParker, error)
	// newStripeClient is the constructor for the stripex facade. nil
	// in production (defaultDeps wires stripex.NewClient); tests inject
	// a recording stub.
	newStripeClient func(store state.Store, dedupe stripex.PushDedupe, log *slog.Logger) stripePusher
	// The two collaborators are wired in production by runWithDeps
	// after the pool is open; tests can pre-populate via the fields.
	parker parkInstanceParker
	stripe stripePusher
	now    func() time.Time
}

func defaultDeps() runDeps {
	return runDeps{
		configPath: "/etc/faas/meterd.toml",
		openDB:     db.Open,
		migrate:    db.MigrateUp,
		loadMeter:  func(c *Config) (*meter.Config, error) { return c.Meter, nil },
		getenv:     os.Getenv,
		dialSchedd: func(socketPath string) (parkInstanceParker, error) {
			c, err := scheddgrpc.Dial(socketPath)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		newStripeClient: func(store state.Store, dedupe stripex.PushDedupe, log *slog.Logger) stripePusher {
			return stripex.NewClient(store, dedupe, envOr("STRIPE_API_KEY", ""), envOr("STRIPE_WEBHOOK_SECRET", ""), log)
		},
		now: time.Now,
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	return runWithDeps(ctx, log, defaultDeps())
}

// envOr mirrors cmd/apid/main.go::envOr. Kept local so meterd's wiring
// stays in one file.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// meterdRequireEnv returns the value of key if non-empty, or a wrapped
// error otherwise. Issue #52's strict-exit acceptance: production
// meterd refuses to start without FAAS_SCHEDD_ADDR rather than silently
// running with a nil parker (every Free-tier app runs unbounded).
func meterdRequireEnv(getenv func(string) string, key string) (string, error) {
	v := getenv(key)
	if v == "" {
		return "", fmt.Errorf("meterd: %s is required", key)
	}
	return v, nil
}

func runWithDeps(ctx context.Context, log *slog.Logger, deps runDeps) error {
	cfg, err := LoadConfig(deps.configPath)
	if err != nil {
		return err
	}
	mc, err := deps.loadMeter(cfg)
	if err != nil {
		return err
	}
	mc.Defaults()

	pool, err := deps.openDB(ctx, cfg.DBURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := deps.migrate(ctx, pool); err != nil {
		return err
	}

	store := state.NewPgStore(pool)
	pn := db.PoolNotifier{Pool: pool}

	// Resolve the schedd socket: env wins over the TOML default so the
	// e2e harness can dial a per-test dial socket without rewriting
	// the unit file. Both empty is the strict-exit failure case
	// (issue #52 acceptance).
	scheddAddr := envOr("FAAS_SCHEDD_ADDR", cfg.SocketPath)
	if scheddAddr == "" {
		if v, err := meterdRequireEnv(deps.getenv, "FAAS_SCHEDD_ADDR"); err != nil {
			return err
		} else {
			scheddAddr = v
		}
	}
	parker := deps.parker
	if parker == nil {
		if deps.dialSchedd == nil {
			return fmt.Errorf("meterd: nil dialSchedd and nil parker (refusing to start unbounded)")
		}
		c, err := deps.dialSchedd(scheddAddr)
		if err != nil {
			return fmt.Errorf("meterd: dial schedd %q: %w", scheddAddr, err)
		}
		parker = c
	}

	stripe := deps.stripe
	if stripe == nil {
		if deps.newStripeClient == nil {
			return fmt.Errorf("meterd: nil newStripeClient and nil stripe")
		}
		apiKey := deps.getenv("STRIPE_API_KEY")
		if apiKey == "" {
			log.Warn("STRIPE_API_KEY is empty — hourly Stripe push will no-op (pushUsageRecordSDK skips without a key)")
		}
		stripe = deps.newStripeClient(store, store, log)
	}

	// FAAS_QUOTA_INTERVAL / FAAS_SAMPLE_INTERVAL / FAAS_STRIPE_INTERVAL let
	// the e2e test shrink the timer cadences to sub-second for the
	// "parked within one tick" acceptance. A bad parse logs and falls
	// through to mc.Defaults() rather than crashing the daemon.
	applyEnvTick("FAAS_SAMPLE_INTERVAL", &mc.SampleInterval, deps.getenv, log)
	applyEnvTick("FAAS_QUOTA_INTERVAL", &mc.QuotaInterval, deps.getenv, log)
	applyEnvTick("FAAS_STRIPE_INTERVAL", &mc.StripeInterval, deps.getenv, log)

	// The three timers run in goroutines; the cancel-watcher below picks
	// up the first error and returns. meterd has no inbound gRPC — the
	// public listener is gatewayd's (spec §Component ownership).
	loop := meter.NewLoop(store, parker, stripe, pn, deps.now, log, mc)
	errc := make(chan error, 1)
	go func() { errc <- loop.Run(ctx) }()

	select {
	case <-ctx.Done():
		log.Info("meterd draining")
	case err := <-errc:
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return nil
}

// applyEnvTick parses FAAS_*_INTERVAL on top of mc.Defaults(). Mirrors
// cmd/apid/main.go::graceIntervalFromEnv; kept local so meterd stays
// in one file.
func applyEnvTick(key string, dst *time.Duration, getenv func(string) string, log *slog.Logger) {
	v := getenv(key)
	if v == "" {
		return
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Warn("unparseable interval; using default", "env", key, "got", v, "err", err)
		return
	}
	*dst = d
}
