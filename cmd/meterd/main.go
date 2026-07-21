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
	"net"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/mail"
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
	// a recording stub. apiKey + webhookSecret are passed in (not read
	// from os.Getenv inside the closure) so a test that stubs getenv
	// sees the same credential values flow into the Client — matches
	// the test-double pattern at cmd/apid/main.go.
	newStripeClient func(apiKey, webhookSecret string, store state.Store, dedupe stripex.PushDedupe, log *slog.Logger) stripePusher
	// The two collaborators are wired in production by runWithDeps
	// after the pool is open; tests can pre-populate via the fields.
	parker parkInstanceParker
	stripe stripePusher
	// mailer is the dunning-timer's outbound email. Wired via
	// mail.SenderFromEnv in defaultDeps so the FAAS_MAIL_TRANSPORT
	// knob is honored (default: log). Tests can inject a noop.
	mailer mail.Sender
	now    func() time.Time
	// metricsListenAndServe binds addr and serves h on a goroutine; the
	// returned shutdown func is called during graceful drain. Mirrors the
	// pattern at cmd/schedd/main.go:143-158. Tests inject a recorder that
	// captures the handler without binding a real socket.
	metricsListenAndServe func(addr string, h http.Handler) (net.Listener, func(context.Context) error, error)
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
		newStripeClient: func(apiKey, webhookSecret string, store state.Store, dedupe stripex.PushDedupe, log *slog.Logger) stripePusher {
			return stripex.NewClient(store, dedupe, apiKey, webhookSecret, log)
		},
		mailer: nil, // populated lazily in runWithDeps via mail.SenderFromEnv
		now:    time.Now,
		metricsListenAndServe: func(addr string, h http.Handler) (net.Listener, func(context.Context) error, error) {
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return nil, nil, err
			}
			srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
			return ln, srv.Shutdown, nil
		},
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	return runWithDeps(ctx, log, defaultDeps())
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
	// e2e harness can dial a per-test socket without rewriting the unit
	// file. Both empty is the strict-exit failure case (issue #52
	// acceptance — refuse to start rather than run unbounded).
	scheddAddr := deps.getenv("FAAS_SCHEDD_ADDR")
	if scheddAddr == "" {
		scheddAddr = cfg.SocketPath
	}
	if scheddAddr == "" {
		return fmt.Errorf("meterd: FAAS_SCHEDD_ADDR (or socket_path in meterd.toml) is required")
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
			log.Warn("STRIPE_API_KEY is empty — hourly Stripe push will no-op (pushUsageRecordSDK returns an error without a key)")
		}
		webhookSecret := deps.getenv("STRIPE_WEBHOOK_SECRET")
		stripe = deps.newStripeClient(apiKey, webhookSecret, store, store, log)
		// Best-effort product/price cache: runs once at boot so the
		// Stripe pusher has PlanPriceIDs populated. Failure logs +
		// continues — the push path is the source of truth, this is
		// only a cache. Gated on apiKey so dev boxes without a key
		// skip the call entirely.
		if apiKey != "" {
			if sc, ok := stripe.(*stripex.Client); ok {
				if err := sc.EnsurePlanProducts(ctx); err != nil {
					log.Warn("meterd: EnsurePlanProducts failed (continuing)", "err", err)
				}
			}
		}
	}

	// Mailer: defaults to mail.SenderFromEnv so FAAS_MAIL_TRANSPORT
	// selects the transport (resend/postmark/log/noop). The dunning
	// timer needs this for its transition emails.
	mailer := deps.mailer
	if mailer == nil {
		mailer = mail.SenderFromEnv(deps.getenv, log)
	}

	// FAAS_QUOTA_INTERVAL / FAAS_SAMPLE_INTERVAL / FAAS_STRIPE_INTERVAL /
	// FAAS_DUNNING_INTERVAL let the e2e test shrink the timer cadences
	// to sub-second for the "transition within one tick" acceptance. A
	// bad parse logs and falls through to mc.Defaults() rather than
	// crashing the daemon.
	applyEnvTick("FAAS_SAMPLE_INTERVAL", &mc.SampleInterval, deps.getenv, log)
	applyEnvTick("FAAS_QUOTA_INTERVAL", &mc.QuotaInterval, deps.getenv, log)
	applyEnvTick("FAAS_STRIPE_INTERVAL", &mc.StripeInterval, deps.getenv, log)
	applyEnvTick("FAAS_DUNNING_INTERVAL", &mc.DunningInterval, deps.getenv, log)

	// Dunning timer: drives the 7-day past_due → suspended and 21-day
	// suspended → deleted_pending transitions (spec §4.7, §17). Wired
	// into the loop alongside sample/quota/stripe so all four timers
	// share the same ctx-cancel lifecycle.
	dunning := meter.NewDunning(meter.DunningParams{
		Store:  store,
		Parker: parker,
		Mailer: mailer,
		Notif:  pn,
		Log:    log,
	})

	// The four timers run in goroutines; the cancel-watcher below picks
	// up the first error and returns. meterd has no inbound gRPC — the
	// public listener is gatewayd's (spec §Component ownership).
	loop := meter.NewLoop(store, parker, stripe, pn, dunning, deps.now, log, mc)
	errc := make(chan error, 1)
	go func() { errc <- loop.Run(ctx) }()

	// Metrics + healthz listener. Mirrors cmd/schedd/main.go:143-158 —
	// per-daemon Prometheus registry (ADR-015), mux at /metrics +
	// /healthz, 5s graceful shutdown on drain. Empty cfg.MetricsAddr
	// disables both endpoints (the production default in
	// deploy/etc/meterd.toml.example).
	const metricsPath = "/metrics"
	var httpShutdown func(context.Context) error
	if cfg.MetricsAddr != "" {
		if deps.metricsListenAndServe == nil {
			return fmt.Errorf("meterd: nil metricsListenAndServe (refusing to start with MetricsAddr set)")
		}
		ops := wire.NewOpsMetrics("meterd")
		mux := http.NewServeMux()
		mux.Handle(metricsPath, ops.Handler())
		// /healthz — unconditional 200 for now. Follow-up PR will cache
		// the last successful sample / quota / stripe / dunning tick and
		// return 503 if any is older than 3× its interval (spec §14 M7
		// acceptance — "meterd healthy iff sampled within 3 minutes").
		// Matches the one-liner at pkg/gateway/synth.go:115-117.
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})
		ln, shutdown, err := deps.metricsListenAndServe(cfg.MetricsAddr, mux)
		if err != nil {
			return fmt.Errorf("meterd: metrics listen %q: %w", cfg.MetricsAddr, err)
		}
		httpShutdown = shutdown
		go func() {
			srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("meterd: metrics http", "err", err)
			}
		}()
		log.Info("meterd metrics listening", "addr", cfg.MetricsAddr)
	}

	select {
	case <-ctx.Done():
		log.Info("meterd draining")
	case err := <-errc:
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}

	// Graceful shutdown: detach a context from the already-cancelled caller
	// ctx (net/http Shutdown requires a non-Done parent). 5s matches the
	// schedd/vmmd/builderd shutdown deadline.
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if httpShutdown != nil {
		//nolint:contextcheck // shutdown ctx must outlive the already-cancelled caller ctx per net/http contract.
		_ = httpShutdown(stopCtx)
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
