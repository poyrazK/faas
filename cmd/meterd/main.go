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
//   - stripe tick: every 24 h, pushes the past day's billable
//     mb_seconds to Stripe as a metered usage record with an
//     integer-arithmetic wire quantity (spec §4.7, ADR-010). The
//     per-day aggregate is the M7 §14 fix for the per-hour fractional
//     truncation that accumulated to ~0.3 % of the customer's bill —
//     above the spec's 0.1 % acceptance delta.
//
// meterd is the ONLY writer that triggers Free-tier hard stops — apid's
// auth gate and schedd's ledger just observe the resulting status.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// parkInstanceParker is the slice of scheddgrpc.Client meterd actually
// uses. Slice 4 adds ParkInstance to scheddgrpc; in tests we inject a
// recording stub. Defining the interface here keeps meterd independent
// of pkg/scheddgrpc until the surface exists (ADR-019).
type parkInstanceParker interface {
	ParkInstance(ctx context.Context, instanceID, reason string) error
}

// stripePusher is the Slice-2 stripe.Client interface meterd uses.
// We don't import pkg/billing/stripe here — the daemon is testable against
// a recorder, and the wire-up in main.go is one line.
//
// PushUsageRecordSum is the integer-mb-seconds path; it receives the
// pusher's summed usage_minutes.mb_seconds across the billing window
// (a full day under the production cadence, cfg.StripeInterval = 24h).
// The SDK converts to wire units in int64 arithmetic — no float, no
// per-hour fractional truncation loss on the wire. Duplicated from
// pkg/meter.StripePusher rather than imported so the test fake
// (cmd/meterd/main_test.go::nopStripe) stays a single-method struct
// scoped to this package.
type stripePusher interface {
	PushUsageRecordSum(ctx context.Context, account state.Account, hour time.Time, mbSeconds int64) error
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
	// production (defaultDeps wires scheddgrpc.DialContext); tests
	// inject a fake to avoid touching the unix socket. Issue #95:
	// signature takes ctx + tls config so the dial participates in the
	// daemon's lifecycle cancellation and can dial a TLS-wrapped remote
	// schedd once the control plane is decoupled.
	dialSchedd func(ctx context.Context, target string, tlsCfg *tls.Config) (parkInstanceParker, error)
	// newStripeClient is the constructor for the stripe facade. nil
	// in production (defaultDeps wires stripe.NewClient); tests inject
	// a recording stub. apiKey + webhookSecret are passed in (not read
	// from os.Getenv inside the closure) so a test that stubs getenv
	// sees the same credential values flow into the Client — matches
	// the test-double pattern at cmd/apid/main.go.
	newStripeClient func(apiKey, webhookSecret string, store state.Store, dedupe stripe.PushDedupe, log *slog.Logger) stripePusher
	// The two collaborators are wired in production by runWithDeps
	// after the pool is open; tests can pre-populate via the fields.
	parker parkInstanceParker
	stripe stripePusher
	// mailer is the dunning-timer's outbound email. Wired via
	// mail.SenderFromEnv in defaultDeps so the FAAS_MAIL_TRANSPORT
	// knob is honored (default: log). Tests can inject a noop.
	mailer mail.Sender
	now    func() time.Time
	// metricsListenAndServe returns a fully-built *http.Server bound to a
	// fresh net.Listener on addr (or the error from net.Listen). The caller
	// invokes `srv.Serve(ln)` on a goroutine and `srv.Shutdown(stopCtx)`
	// during graceful drain — the same server owns both halves, so the
	// pair stays in lockstep (no possibility of one server's Serve
	// outliving another's Shutdown). Mirrors cmd/schedd/main.go:151-158.
	// Tests inject a stub that returns a nop server (without binding).
	metricsListenAndServe func(addr string, h http.Handler) (*http.Server, error)
}

func defaultDeps() runDeps {
	return runDeps{
		configPath: "/etc/faas/meterd.toml",
		openDB:     db.Open,
		migrate:    db.MigrateUp,
		loadMeter:  func(c *Config) (*meter.Config, error) { return c.Meter, nil },
		getenv:     os.Getenv,
		dialSchedd: func(ctx context.Context, target string, tlsCfg *tls.Config) (parkInstanceParker, error) {
			c, err := scheddgrpc.DialContext(ctx, target, tlsCfg)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		newStripeClient: func(apiKey, webhookSecret string, store state.Store, dedupe stripe.PushDedupe, log *slog.Logger) stripePusher {
			return stripe.NewClient(store, dedupe, apiKey, webhookSecret, log)
		},
		mailer: nil, // populated lazily in runWithDeps via mail.SenderFromEnv
		now:    time.Now,
		metricsListenAndServe: func(addr string, h http.Handler) (*http.Server, error) {
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return nil, err
			}
			srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
			// Serve in a goroutine; the daemon keeps `srv` and calls
			// Shutdown on it during drain. Pairing Serve/Shutdown on the
			// same *http.Server avoids the dual-server asymmetry the
			// factory's previous shape allowed (PR #75 review finding).
			// Errors are logged via the package-level slog.Default here
			// because defaultDeps is built before runWithDeps wires the
			// daemon's *slog.Logger.
			go func() {
				if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
					slog.Default().Error("meterd: metrics http", "err", err)
				}
			}()
			return srv, nil
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
		c, err := deps.dialSchedd(ctx, scheddAddr, nil)
		if err != nil {
			return fmt.Errorf("meterd: dial schedd %q: %w", scheddAddr, err)
		}
		parker = c
	}

	pusher := deps.stripe
	if pusher == nil {
		if deps.newStripeClient == nil {
			return fmt.Errorf("meterd: nil newStripeClient and nil stripe")
		}
		apiKey := deps.getenv("STRIPE_API_KEY")
		if apiKey == "" {
			log.Warn("STRIPE_API_KEY is empty — daily Stripe push will no-op (pushUsageRecordSDKSum returns an error without a key)")
		}
		webhookSecret := deps.getenv("STRIPE_WEBHOOK_SECRET")
		pusher = deps.newStripeClient(apiKey, webhookSecret, store, store, log)
		// Best-effort product/price cache: runs once at boot so the
		// Stripe pusher has PlanPriceIDs populated. Failure logs +
		// continues — the push path is the source of truth, this is
		// only a cache. Gated on apiKey so dev boxes without a key
		// skip the call entirely.
		if apiKey != "" {
			if sc, ok := pusher.(*stripe.Client); ok {
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
	// FAAS_DUNNING_INTERVAL / FAAS_RESIDENCY_INTERVAL let the e2e test
	// shrink the timer cadences to sub-second for the "transition
	// within one tick" acceptance. A bad parse logs and falls through
	// to mc.Defaults() rather than crashing the daemon.
	applyEnvTick("FAAS_SAMPLE_INTERVAL", &mc.SampleInterval, deps.getenv, log)
	applyEnvTick("FAAS_QUOTA_INTERVAL", &mc.QuotaInterval, deps.getenv, log)
	applyEnvTick("FAAS_STRIPE_INTERVAL", &mc.StripeInterval, deps.getenv, log)
	applyEnvTick("FAAS_DUNNING_INTERVAL", &mc.DunningInterval, deps.getenv, log)
	applyEnvTick("FAAS_RESIDENCY_INTERVAL", &mc.ResidencyInterval, deps.getenv, log)

	// Dunning timer: drives the 7-day past_due → suspended and 21-day
	// suspended → deleted_pending transitions (spec §4.7, §17). Wired
	// into the loop alongside sample/quota/stripe so all five timers
	// share the same ctx-cancel lifecycle.
	dunning := meter.NewDunning(meter.DunningParams{
		Store:  store,
		Parker: parker,
		Mailer: mailer,
		Notif:  pn,
		Log:    log,
	})

	// Per-daemon Prometheus registry (ADR-015) — built unconditionally
	// so the Loop has it from the first tick. meter.NewLoop accepts nil
	// and coerces to a fresh test registry; here we hand it the real one.
	ops := wire.NewOpsMetrics("meterd")

	// Residency timer: emits the §12 "Resident GB per paying customer"
	// gauge (ADR-031, PR #141). Wired into the loop alongside
	// sample/quota/stripe/dunning so all five timers share the same
	// ctx-cancel lifecycle. ops is the per-daemon registry above;
	// residency.SetResidentGBPerCustomer is nil-safe so a later ops
	// swap doesn't take the gauge down with it.
	residency := meter.NewResidency(store, deps.now, log, ops)

	// The five timers run in goroutines; the cancel-watcher below picks
	// up the first error and returns. meterd has no inbound gRPC — the
	// public listener is gatewayd's (spec §Component ownership).
	loop := meter.NewLoop(store, parker, pusher, pn, mailer, dunning, residency, deps.now, log, mc, ops)
	errc := make(chan error, 1)
	go func() { errc <- loop.Run(ctx) }()

	// Metrics + healthz listener. Mirrors cmd/schedd/main.go:143-158 —
	// per-daemon Prometheus registry (ADR-015), mux at /metrics +
	// /healthz, 5s graceful shutdown on drain. Empty cfg.MetricsAddr
	// disables both endpoints (the production default in
	// deploy/etc/meterd.toml.example).
	const metricsPath = "/metrics"
	var metricsSrv *http.Server
	if cfg.MetricsAddr != "" {
		if deps.metricsListenAndServe == nil {
			return fmt.Errorf("meterd: nil metricsListenAndServe (refusing to start with MetricsAddr set)")
		}
		mux := http.NewServeMux()
		mux.Handle(metricsPath, ops.Handler())
		// /healthz — 200 when every tracked timer (sample / quota /
		// stripe / dunning) has fired within
		// meter.StaleAfterMultiplier × its interval (spec §14 M7,
		// "meterd healthy iff sampled within 3 minutes"); 503 with a
		// JSON body listing the stale tick names otherwise. The body
		// always includes a per-tick last-fire wall clock so an
		// operator can diagnose without grepping journald.
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			status := loop.Health(time.Now())
			w.Header().Set("Content-Type", "application/json")
			code := http.StatusOK
			if !status.Healthy {
				code = http.StatusServiceUnavailable
			}
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(status)
		})
		srv, err := deps.metricsListenAndServe(cfg.MetricsAddr, mux)
		if err != nil {
			return fmt.Errorf("meterd: metrics listen %q: %w", cfg.MetricsAddr, err)
		}
		metricsSrv = srv
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
	if metricsSrv != nil {
		//nolint:contextcheck // shutdown ctx must outlive the already-cancelled caller ctx per net/http contract.
		_ = metricsSrv.Shutdown(stopCtx)
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
