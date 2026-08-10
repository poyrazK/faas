// Command apid — control API (spec §4.2).
//
// apid is the public REST API, the auth boundary, and the ONLY writer to
// customer-intent tables (accounts, apps, deployments, domains). It validates
// plan quotas before any work happens and never calls vmmd/builderd directly —
// it writes rows and notifies owners via pg_notify (spec §Component ownership).
//
// M5+: apid uses the pgx-backed state.PgStore against the same Postgres
// cluster schedd/imaged share; queries.sql is the SQL source of truth and
// pgstore.go adapts the result shape to the domain types. The CLI exercises
// apid through FAAS_DEV_TOKEN for local dev (memstore seed path stays for
// tests).
package main

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/authcode"
	billingloader "github.com/onebox-faas/faas/pkg/billing/loader"
	"github.com/onebox-faas/faas/pkg/capdecl/runtimecheck"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/eventretention"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/logarchive"
	"github.com/onebox-faas/faas/pkg/logintoken"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookdedupe"
	"github.com/onebox-faas/faas/pkg/wire"
)

// seedDevAccount creates (or finds) the dev@local Free account. The
// API-key mint that used to live here is REMOVED: production boxes
// never set FAAS_DEV_TOKEN (they use the real signup flow + gregale
// sign-keys init), and dev/test environments mint keys explicitly via
// pkg/e2etest.Harness.SeedAccount or their own CLI setup. Calling
// CreateAPIKey on every apid boot was a pgstore.Write at restart-loop
// frequency — the path that crash-looped apid in run 31121004495
// (post PR #633 deploy 2026-08-04). Removing it sidesteps the
// restart-cycle race entirely.
//
// Tier A7 PR-D.
func seedDevAccount(ctx context.Context, store state.Store, token string) error {
	if !api.ValidAPIKeyFormat(token) {
		return fmt.Errorf("FAAS_DEV_TOKEN is not a valid API key (want %s… format)", api.APIKeyPrefix)
	}
	acct, err := store.AccountByEmail(ctx, "dev@local")
	if errors.Is(err, state.ErrNotFound) {
		// Intentionally NOT routed through CreateAccountWithPersonalOrg — the
		// dev bootstrap is a synthetic local-only fixture. Production
		// signup paths (postSignup, OAuth callbacks, CLI activation) wrap
		// every account through the helper; the dev bootstrap is opt-out by
		// design. The PR 3 backfill (migrations/00101) and the e2e harness
		// SeedAccount cover the "every account has a personal org"
		// invariant.
		if _, err = store.CreateAccount(ctx, "dev@local", api.PlanFree); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	_ = acct // find-or-create confirmed; the row exists either way
	return nil
}

// envOr returns the value of env key, or fallback when unset/empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// firstNonEmpty returns the first non-empty string among the
// args. Used by the log archive wire-up so an explicit env
// var (FAAS_LOG_ARCHIVE_*) wins over a value in the sealed
// archive-creds.json envelope — operators expect the env to
// override defaults, never the other way around.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// archiveCreds is the on-disk shape of the unsealed
// archive-creds.json envelope (issue #562). The envelope is
// sealed by an external operator with host.age (or the
// per-deploy box-age-key — same shape as the rclone.conf
// envelope, see cmd/gregale/commands_backup.go::defaultBoxAgeKey)
// and mounted at /etc/faas/secrets/storage-box/archive-creds.json
// via systemd LoadCredential= (deploy/ansible role).
//
// The file is JSON for parse simplicity — no envelope decoder
// in the hot path, the apid boot reads it once. Fields are
// optional so the operator can stage an incomplete envelope
// while waiting on the bucket/region/endpoint values from
// the S3 vendor.
type archiveCreds struct {
	Endpoint string `json:"endpoint,omitempty"`
	Region   string `json:"region,omitempty"`
	KeyID    string `json:"key_id,omitempty"`
	Secret   string `json:"secret,omitempty"`
}

// readArchiveCreds parses the unsealed archive-creds.json
// envelope from path. The file is read once at boot — the
// shipper doesn't watch for rotation. A future PR can add a
// SIGHUP-driven reload if a credential rotation breaks the
// running daemon.
func readArchiveCreds(path string) (archiveCreds, error) {
	var c archiveCreds
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("logarchive: parse %s: %w", path, err)
	}
	return c, nil
}

// listenAddr is the bind address for apid. Behind gatewayd-internal; not a public
// listener. Overridable via FAAS_APID_LISTEN so the e2e harness can pick a
// free port without colliding with a dev daemon on 8081.
var listenAddr = envOr("FAAS_APID_LISTEN", "127.0.0.1:8081")

// metricsAddr is the bind address for the apid /metrics listener
// (separate from the main listener so a port collision can't take the
// daemon down). Defaults to 127.0.0.1:9101 so an operator typo (or
// a missing env var in prod) can't accidentally expose the internal
// registry to the public network — series like apid_ops_total{op,code}
// leak auth-rejection rates and per-route traffic shape (review
// finding #1 on PR #132). Loopback bind is safe because the local
// Prometheus scrapes from the box itself.
//
// Empty FAAS_APID_METRICS_ADDR disables the listener. This is the
// deliberately-distinct envOr path: envOr() collapses empty→unset→
// fallback (line 63), which is right for FAAS_APID_LISTEN (where
// empty means "no override, use the default port") but wrong here
// (where empty means "skip the listener entirely"). os.LookupEnv
// distinguishes "unset" (fall through to the default) from
// "explicitly empty" (skip), so the e2e harness can stamp
// `FAAS_APID_METRICS_ADDR=` and avoid the 127.0.0.1:9101 bind race
// against a sibling or zombie apid run. Mirrors cmd/builderd/main.go's
// MetricsAddr pattern (PR #124).
var metricsAddr = func() string {
	v, ok := os.LookupEnv("FAAS_APID_METRICS_ADDR")
	if ok && v == "" {
		return "" // explicit-empty = disable
	}
	if !ok || v == "" {
		return "127.0.0.1:9101"
	}
	return v
}()

// resolveAdvisorySock reads FAAS_APID_ADVISORY_SOCK via the test
// seam (deps.getenv). Empty string disables the listener. Tests
// disable by default (their getenv stub returns "" for unknown
// keys) so macOS dev boxes don't try to bind /run/faas
// (read-only on macOS). Production wires defaultDeps.getenv to
// os.Getenv; the systemd unit stamps FAAS_APID_ADVISORY_SOCK=
// /run/faas/apid.sock explicitly so the default doesn't matter
// in prod — the explicit assignment is what enables the
// listener there.
func resolveAdvisorySock(getenv func(string) string) string {
	return getenv("FAAS_APID_ADVISORY_SOCK")
}

// resolveGithubdBridgeSock reads FAAS_APID_GITHUBD_BRIDGE_SOCK via
// the test seam (deps.getenv). Empty string disables the listener —
// the same pattern as resolveAdvisorySock so macOS dev boxes
// don't try to bind /run/faas. The systemd unit stamps
// FAAS_APID_GITHUBD_BRIDGE_SOCK=/run/faas/apid-githubd.sock in
// production (issue #432 phase 5). Unlike the advisory socket,
// the bridge socket has a separate path because the consumer
// (githubd) dials it, not vmmd, and the 0660 DAC group is shared
// between the githubd user and apid user.
func resolveGithubdBridgeSock(getenv func(string) string) string {
	return getenv("FAAS_APID_GITHUBD_BRIDGE_SOCK")
}

// resolveGithubdStagingRoot reads FAAS_GITHUBD_WORK_DIR via the
// test seam (deps.getenv). The default matches the githubd-side
// githubdWorkDir() default (/var/lib/faas/githubd); the bridge
// handlers append /build-sources internally so the same env var
// that githubd reads configures the apid-side allowlist. The
// staging prefix is the directory githubd's
// pkg/githubd/staging.go:72 writes per-app tarballs into; a
// mismatch between the two sides surfaces as invalid_argument on
// the first push and is logged + retried by the dispatcher (the
// githubd-side WARN carries the staged path the gRPC call
// returned).
func resolveGithubdStagingRoot(getenv func(string) string) string {
	if p := getenv("FAAS_GITHUBD_WORK_DIR"); p != "" {
		return p
	}
	return "/var/lib/faas/githubd"
}

// runDeps is the DI seam for run — same pattern as vmmd / gatewayd-internal so we can
// exercise the listener lifecycle without binding :8081 from tests.
type runDeps struct {
	listen   func(network, addr string) (net.Listener, error)
	store    func() state.Store
	notif    func() Notifier
	getenv   func(string) string
	newSrv   func(addr string, h http.Handler) *http.Server
	bgBefore func(ctx context.Context, log *slog.Logger, srv *server) // optional pre-listen hook (e.g. DNS poller)
	loginTTL time.Duration                                            // dashboard magic-link expiry
	// mailer is the outbound email sender (gap G4). Nil means "pick
	// from env at startup" via mail.SenderFromEnv — same pattern meterd
	// uses (cmd/meterd/main.go:82-87). Tests inject a stub.
	mailer mail.Sender
	// capCheck: DEPLOY-1 / ADR-075 capdecl gate seam (review
	// finding M2). nil → runtimecheck.MustCheckOnBoot(capsDecl,
	// log, nil) which exits on violation in production. Tests
	// inject func() error { return nil } to bypass the live
	// /proc/self/status check (the test runner doesn't carry
	// the production capset, and MustCheckOnBoot calls os.Exit
	// on violation).
	capCheck func() error
	// PR-P2: TOML config wiring. configPath defaults to
	// /etc/faas/apid.toml in defaultDeps; tests override to point at
	// a temp file with a hand-rolled [billing] block.
	configPath string
	// loadBillingConfig reads the [billing] block from apid.toml.
	// nil in production (defaultDeps wires billingloader.LoadBillingConfigFromPath);
	// tests stub to return a hand-rolled *RootBillingConfig.
	loadBillingConfig func(path string) (*billingloader.RootBillingConfig, error)
}

func defaultDeps() runDeps {
	return runDeps{
		listen: net.Listen,
		store:  func() state.Store { return state.NewMemStore() },
		notif:  func() Notifier { return noopNotifier{} },
		getenv: os.Getenv,
		newSrv: func(addr string, h http.Handler) *http.Server {
			return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
		},
		loginTTL:          15 * time.Minute,
		configPath:        "/etc/faas/apid.toml",
		loadBillingConfig: billingloader.LoadBillingConfigFromPath,
	}
}

func main() {
	wire.Daemon("apid", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	// DEPLOY-1 / ADR-075 capdecl gate. apid's capsDecl is
	// cap_net_bind_service (HTTPS listener). A misconfigured
	// AmbientCapabilities line fails fast at boot. The
	// capCheck seam lets tests stub the live /proc/self/status
	// check (review finding M2 — every daemon now has this).
	capCheck := defaultDeps().capCheck
	if capCheck == nil {
		capCheck = func() error { return runtimecheck.MustCheckOnBoot(capsDecl, log, nil) }
	}
	if err := capCheck(); err != nil {
		return err
	}

	// Gate-B box-role gate. apid is a control-plane daemon — it
	// refuses to start under RoleComputeOnly. The role is set
	// from FAAS_APID_ROLE at deploy time (apid has no config.go
	// today; the env is the only source); default is
	// RoleSingleBox so single-box dev boots unmoved. The gate
	// runs before db.Open so a misconfigured boot doesn't waste
	// a Postgres connection.
	if err := role.Require("apid", role.FromConfig("", "FAAS_APID_ROLE"),
		role.RoleSingleBox, role.RoleControlPlane); err != nil {
		return err
	}

	pool, err := db.Open(ctx, "")
	if err != nil {
		return fmt.Errorf("apid: open db: %w", err)
	}
	defer pool.Close()
	if err := db.MigrateUp(ctx, pool); err != nil {
		return fmt.Errorf("apid: migrate: %w", err)
	}

	// Issue #249 / spec §11: gate Strict-Transport-Security on
	// FAAS_HSTS_ENABLED. Default true; dev mode can flip to false.
	// RFC 6797 §7.2 says UAs ignore HSTS on plain HTTP, so the knob
	// is purely cosmetic — but emitting it on a dev plaintext loop
	// back listener confuses operators reading the headers.
	httpsec.SetHSTSEnabled(httpsec.HSTSEnabledFromEnv(os.Getenv))

	deps := defaultDeps()
	deps.store = func() state.Store { return state.NewPgStore(pool) }
	deps.notif = func() Notifier { return pgNotifier{pool: pool} }
	deps.bgBefore = func(ctx context.Context, log *slog.Logger, srv *server) {
		// Move 3 (M7.5 prep): bridge pg_notify → in-process broadcaster.
		// Runs as a background goroutine for the daemon's lifetime; the
		// SubscribeWithReconnect wrapper reconnects across Postgres
		// restarts. Fails fast at boot if the initial Subscribe errors
		// — the dashboard SSE surfaces the gap rather than silently
		// producing empty frames. Lives in this closure (not runWithDeps)
		// because production-run holds the *pgxpool.Pool and the test
		// seam in runWithDeps doesn't.
		go sseFanIn(ctx, log, pool, srv.events, nil)
		startDNSPoller(ctx, srv, log)
		// G6 grace timer (spec §17 G6, ADR-021): the 30-day deletion
		// grace sweep lives in apid (not meterd) because the write
		// side (DELETE /v1/account, POST /v1/account/restore) is here
		// and meterd owns quotas/billing only. Default Interval 60s
		// matches the grace-side precision we need; sweep is a
		// ListAllAccounts walk so it stays bounded by the customer
		// count on the one box.
		graceLoop := grace.New(grace.Params{
			Store:    srv.store,
			Mailer:   graceSenderAdapter{m: srv.mailer},
			Log:      log,
			Interval: graceIntervalFromEnv(log),
			Notif: func(ctx context.Context, ch, payload string) error {
				return srv.notif.Notify(ctx, ch, payload)
			},
			Audit: srv.audit, // issue #755 / PR-5.5: emit account.deleted from the sweep
		})
		go func() { _ = graceLoop.Run(ctx) }()
		// Login-token cleanup (issue #165 PR #2, ADR-032). The
		// login_tokens table backs password-reset (15-min TTL) and
		// the legacy magic-link surface PR #1 removed. The
		// /login/forgot → POST /auth/reset pair is the only
		// production caller — we run a 24h ticker so the table
		// stays bounded by (rate of reset requests) × 15min.
		// pkg/logintoken mirrors pkg/grace (same Run / RunOnce
		// shape) so the lifecycle is consistent with the G6 grace
		// timer above.
		loginTokenCleanup := logintoken.New(logintoken.Params{
			Store: srv.store,
			Log:   log,
		})
		go func() { _ = loginTokenCleanup.Run(ctx) }()
		// ADR-075: 90-day audit retention. The events table grows
		// ~3-4 GB/year/active-tier through the auth / key / secret
		// / account / stateless audit namespaces plus the future
		// wake-timeline / sidecar surfaces. The daily loop trims
		// rows older than eventretention.DefaultCutoffDays (90d,
		// SOC 2 CC6.2 evidence-retention floor). Mirrors
		// pkg/logintoken byte-for-byte (same Run / RunOnce pattern,
		// same first-pass-error defence-in-depth).
		eventRetentionCleanup := eventretention.New(eventretention.Params{
			Store: srv.store,
			Log:   log,
		})
		go func() { _ = eventRetentionCleanup.Run(ctx) }()
		// Issue #562 / PR-A: log archive shipper. Walks the local
		// spool dir every cfg.FlushInterval (5 min default) and
		// gzip-PUTs .partial files to s3://{bucket}/faas-logs/{instance}/
		// {YYYY}/{MM}/{DD}.jsonl.gz. Disabled (returns nil on ctx
		// cancel) when FAAS_LOG_ARCHIVE_BUCKET is unset — the
		// pgAdmin-style on-box ring buffer is the only log surface
		// for Free + Hobby tiers in that mode.
		//
		// Credentials are read from the archive-creds.json envelope
		// unsealed by `gregale backup unseal-archive-creds` and
		// mounted at /etc/faas/secrets/storage-box/archive-creds.json
		// via systemd LoadCredential= (deploy/ansible role sets this
		// up). On a host that hasn't been unsealed yet the file is
		// missing and the shipper fails closed on ErrAuthMissing.
		// Future PR-C will add the unseal CLI leaf + the ansible
		// wiring.
		shipCfg, shipErr := logarchive.ConfigFromEnv(os.Getenv, log)
		switch {
		case shipErr != nil:
			log.Warn("logarchive.config_failed", "err", shipErr)
		case !shipCfg.Enabled():
			log.Info("logarchive.disabled", "reason", "FAAS_LOG_ARCHIVE_BUCKET unset")
		default:
			archiveCredsPath := "/etc/faas/secrets/storage-box/archive-creds.json"
			if creds, err := readArchiveCreds(archiveCredsPath); err != nil {
				log.Warn("logarchive.creds_unavailable", "path", archiveCredsPath, "err", err)
			} else {
				s3, err := logarchive.NewS3Client(
					firstNonEmpty(shipCfg.Endpoint, creds.Endpoint),
					firstNonEmpty(shipCfg.Region, creds.Region),
					shipCfg.Bucket,
					firstNonEmpty(shipCfg.KeyID, creds.KeyID),
					firstNonEmpty(shipCfg.Secret, creds.Secret),
				)
				if err != nil {
					log.Warn("logarchive.s3client_init_failed", "err", err)
				} else {
					spool := logarchive.NewSpool(shipCfg.SpoolRoot, shipCfg.LocalBytesMax)
					shipMetrics := logarchive.NewMetrics(srv.ops.Registry())
					sh, err := logarchive.NewShipper(shipCfg, spool, s3, log, shipMetrics)
					if err != nil {
						log.Warn("logarchive.shipper_init_failed", "err", err)
					} else {
						go func() {
							if err := sh.Run(ctx); err != nil && ctx.Err() == nil {
								log.Warn("logarchive.run_returned_error", "err", err)
							}
							_ = sh.Spool().CloseAll()
						}()
					}
				}
			}
		}
		// Issue #300: topNSampler drives the apid_top_tenant_rps
		// gauge from the rolling per-account count fed by
		// observeWrap (server.go:observeWrap). 5s tick; runs for
		// the daemon's lifetime; stops cleanly on ctx cancel.
		topNSampler := newTopNSampler(srv.ops, log)
		go topNSampler.run(ctx)
		// Issue #250: pgBackupPushedSampler drives the
		// apid_pg_backup_last_pushed_seconds gauge from the mtime
		// of the newest tarball in /var/lib/pgsql/basebackup/.
		// 60s tick (matches the PgBackupStale alert's `for: 5m`
		// window — at least 5 fresh ticks per evaluation).
		pgBackupPushedSampler := newPgBackupPushedSampler(srv.ops, log)
		go pgBackupPushedSampler.run(ctx)
		// Webhook replay-dedupe sweep (issue #294). The
		// webhook_deliveries table is written by all three ingresses
		// (GitHub via gatewayd-internal, Stripe + Paddle via apid); the TTL
		// expires_at column + the partial index keep the per-tick
		// DELETE bounded by (60s tick × ~rows added in that window).
		// 60s matches the meterd dunning sweep cadence.
		webhookSweeper := webhookdedupe.NewSweeper(webhookdedupe.DefaultSweepInterval)
		go func() { _ = webhookSweeper.Run(ctx) }()
		// Issue #472 / ADR-058: bridge the `audit_event` pg_notify
		// channel (imaged emits signature-failure events via this
		// channel since imaged is the only fire-and-forget audit
		// publisher that doesn't write the events table directly)
		// into the apid-side auditor. Subscribing via
		// SubscribeWithReconnect is the same pattern schedd's
		// deletion_subscriber uses (pkg/db/notify.go:304) so the
		// daemon survives Postgres restarts. The initial Subscribe
		// error is fatal — silent drop is the bug we're closing.
		if srv.audit != nil {
			go func() {
				// issue #517 / PR-C / ADR-064: thread the
				// events Platform through the audit
				// subscriber so verify-rejection kinds
				// (app.signature_invalid /
				// app.signature_missing) also emit the
				// typed wake.deploy_failed row.
				if err := runAuditSubscriber(ctx, pool, srv.audit, log, srv.eventsPlatform); err != nil && ctx.Err() == nil {
					log.Error("audit: subscriber exited", "err", err)
				}
			}()
		}
		// Issue #472 / ADR-058: maintain the on-disk mirror of
		// app_trusted_signers at /etc/faas/secrets/trusted-publishers
		// (the dir imaged reads at startup and on every
		// trusted_signer_changed notify). Without this writer, the
		// disk stays empty and every signed deploy fails with
		// ErrSignatureInvalid even when the operator has correctly
		// onboarded publishers via the API. The writer is the only
		// producer of the per-app PEM files; the dir is created
		// at daemon start so the first ever onboard succeeds even
		// before the first notify.
		trustedDir := os.Getenv("FAAS_TRUSTED_PUBLISHERS_DIR")
		if trustedDir != "" && srv.store != nil {
			go func() {
				if err := runTrustedPublisherWriter(ctx, pool, srv.store, trustedDir, log); err != nil && ctx.Err() == nil {
					log.Error("trusted-publisher-writer: exited", "err", err)
				}
			}()
		}
	}
	return runWithDeps(ctx, log, deps)
}

// graceSenderAdapter bridges apid's Mailer (which sends the apid
// Message struct) to pkg/grace.Sender (which takes primitive args).
// Kept inline so the production apid binary doesn't pull the apid
// Message type into pkg/grace — pkg/grace's signature is intentionally
// narrow so it has no apid dependency.
type graceSenderAdapter struct{ m Mailer }

func (g graceSenderAdapter) Send(ctx context.Context, to []string, subject, body string) error {
	return g.m.Send(ctx, Message{To: to, Subject: subject, TextBody: body})
}

// mailAdapter bridges pkg/mail.Sender (the cross-daemon outbound-email
// seam) to apid's internal Mailer interface. Same shape as
// graceSenderAdapter above but in the opposite direction: the apid
// Message type stays free of pkg/mail so daemons that link apid don't
// pull the mail deps transitively. Gap G4 closure: the production
// wire-up in runWithDeps wraps mail.SenderFromEnv(...)
// (Resend/Postmark/Log/Noop) in this adapter so magic-link + dunning +
// quota-warning + deletion-pending emails actually reach the customer.
type mailAdapter struct{ s mail.Sender }

// newMailerAdapter wraps a pkg/mail.Sender so it satisfies apid's
// Mailer interface. Returns noopMailer{} for a nil sender so callers
// never need to nil-check (matches newServerWithDeps's nil → noop
// convention).
func newMailerAdapter(s mail.Sender) Mailer {
	if s == nil {
		return noopMailer{}
	}
	return mailAdapter{s: s}
}

func (a mailAdapter) Send(ctx context.Context, m Message) error {
	return a.s.Send(ctx, mail.Message{
		To:       m.To,
		Subject:  m.Subject,
		TextBody: m.TextBody,
		HTMLBody: m.HTMLBody,
	})
}

// graceIntervalFromEnv reads FAAS_GRACE_INTERVAL to let the e2e test
// accelerate the sweep (default 60s is correct for production; a CI
// test sets it to a few hundred ms so the 30-day "grace expired"
// case runs in seconds, not minutes). Returns 0 to let pkg/grace
// fall back to its 60s default.
func graceIntervalFromEnv(log *slog.Logger) time.Duration {
	v := os.Getenv("FAAS_GRACE_INTERVAL")
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		if log != nil {
			log.Warn("FAAS_GRACE_INTERVAL unparseable, using default",
				"value", v, "err", err)
		}
		return 0
	}
	return d
}

// dpaPathFromEnv resolves the DPA template path. Production wires an
// explicit FAAS_DPA_PATH pointing at the installed /etc/faas/dpa.md;
// when that's unset, fall back to <cwd>/docs/DPA.md if that file
// exists, so `go run ./cmd/apid` from the repo root serves the dev
// template without a setup step. When neither is set the handler
// returns 503 — a misconfigured production deploy is observable
// rather than silently empty (see handlers_account.go::dpaTemplate).
func dpaPathFromEnv(getenv func(string) string) string {
	if p := getenv("FAAS_DPA_PATH"); p != "" {
		return p
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(cwd, "docs", "DPA.md")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

func runWithDeps(ctx context.Context, log *slog.Logger, deps runDeps) error {
	store := deps.store()

	// Dev-only: seed a Free account bound to $FAAS_DEV_TOKEN so the CLI can be
	// exercised end-to-end without the (browser-paste) signup flow. Never set in
	// production — the Postgres store + real login supersede this.
	if tok := deps.getenv("FAAS_DEV_TOKEN"); tok != "" {
		if err := seedDevAccount(ctx, store, tok); err != nil {
			return err
		}
		log.Warn("dev account seeded from FAAS_DEV_TOKEN — do not use in production")
	}

	// M7: pass the Stripe webhook secret (env-loaded) and the mailer
	// (log-only until gap G4 is closed). Empty secret = dev mode (the
	// webhook accepts unsigned payloads; never deploy this way).
	stripeSecret := deps.getenv("STRIPE_WEBHOOK_SECRET")
	// Gap G4 closure (PR): wire the env-driven mail factory so prod
	// boots with FAAS_MAIL_TRANSPORT=resend and emails go out for real.
	// Tests + dev can keep mailer nil and the factory returns a log
	// sender — behaviour matches the pre-PR newLogMailer(log) wiring.
	m := deps.mailer
	if m == nil {
		m = mail.SenderFromEnv(deps.getenv, log)
	}
	mailer := newMailerAdapter(m)
	// M7.5: githubd socket path (ADR-012). Empty = stub client (every
	// method returns api.Problem{Code:"githubd_not_ready"}), which is
	// fine until githubd is actually deployed on this host.
	//
	// ADR-052: multi-box deployments dial githubd over tcp:// +
	// mTLS. Load the client TLS config from env so the same
	// per-daemon TOML surface (or env-var analogue) works whether
	// the githubd lives on the same host (unix socket) or a
	// remote box (tcp/dns + leaf cert). Empty TLS cluster returns
	// (nil, nil) and the unix path keeps working.
	githubdTLS, err := wire.LoadClientTLSConfig(
		deps.getenv("FAAS_GITHUBD_TLS_CERT_PATH"),
		deps.getenv("FAAS_GITHUBD_TLS_KEY_PATH"),
		deps.getenv("FAAS_GITHUBD_TLS_CA_PATH"),
	)
	if err != nil {
		return fmt.Errorf("apid: githubd TLS: %w", err)
	}
	githubd := newGithubdClient(ctx, deps.getenv("FAAS_GITHUBD_SOCKET"), githubdTLS, log)
	// M7.5: dashboard session manager. Loads the 32-byte key from
	// FAAS_SESSION_KEY (hex-encoded); empty in dev = ephemeral key +
	// warning so the daemon still boots for local testing. Production
	// MUST set this to the contents of /etc/faas/secrets/session.key
	// (root:root 0400, spec §11).
	sessions, sessionsWarn := loadSessionManager(deps.getenv, log)
	if sessionsWarn != "" {
		log.Warn("session manager in dev mode; sessions reset on restart", "warning", sessionsWarn)
	}
	// Issue #419 / ADR-046: validate the sign-in OAuth env vars at
	// boot. Half-configured (e.g. GOOGLE_CLIENT_ID set but
	// GOOGLE_CLIENT_SECRET unset) refuses to start — that's the
	// 500-into-customer-request footgun the loader exists to close.
	// Both-unset is permitted: the operator chose not to ship OAuth
	// on this host, the handlers return 503 oauth_provider_unavailable,
	// and the dashboard's login template hides the buttons. The
	// resolved config rides on *server via WithOAuthConfig so the
	// handlers, /v1/auth/capabilities, and renderLoginForm share one
	// source of truth (no os.Getenv at request time).
	oauthCfg, err := auth.LoadSignInConfigFromEnv(deps.getenv)
	if err != nil {
		return fmt.Errorf("apid OAuth configuration: %w", err)
	}
	if !oauthCfg.Google.Enabled() && !oauthCfg.GitHub.Enabled() {
		log.Warn("OAuth disabled on this host — both providers unset; /v1/auth/{google,github} return 503 oauth_provider_unavailable, /login hides the OAuth buttons",
			"google_enabled", oauthCfg.Google.Enabled(),
			"github_enabled", oauthCfg.GitHub.Enabled())
	} else {
		log.Info("OAuth sign-in capability",
			"google_enabled", oauthCfg.Google.Enabled(),
			"github_enabled", oauthCfg.GitHub.Enabled())
	}
	srv := newServerWithDeps(store, log, deps.getenv("FAAS_APPS_DOMAIN"), deps.notif(), stripeSecret, mailer, githubd, sessions, nil, deps.loginTTL, dpaPathFromEnv(deps.getenv))
	srv.WithOAuthConfig(oauthCfg)

	// Issue #142: Stripe billing portal URL template for the changePlan
	// 402 response. Empty = 402 omits billing_portal_url; the dashboard
	// renders a generic "use the billing portal" message. Production sets
	// FAAS_BILLING_PORTAL_URL to a template containing `{account_id}`
	// (replaced at write time) so the customer lands on a Stripe-hosted
	// portal pre-bound to their account.
	//
	// SECURITY: this value is operator-controlled and rendered verbatim
	// into every blocked-upgrade response. A misconfigured value that
	// points at an attacker-controlled host (e.g. an env-var typo or a
	// wrong deploy) misroutes every blocked upgrade. Set it to the
	// operator-hosted Stripe billing portal URL, validate it before
	// deploy, and never interpolate untrusted input.
	srv.WithBillingPortalURL(deps.getenv("FAAS_BILLING_PORTAL_URL"))

	// Billing provider dispatch (ADR-025 / PR #3). When
	// FAAS_BILLING_PROVIDER=paddle the loader constructs a
	// *paddle.Provider and runs EnsurePlanProducts so the catalog is
	// populated before the first /v1/webhooks/paddle POST can land.
	// When unset (or "stripe") the loader returns (nil, "stripe", nil)
	// and the changePlan 402 path falls back to the FAAS_BILLING_PORTAL_URL
	// template above — the pre-PR-#3 Stripe path is bit-for-bit
	// unchanged.
	//
	// PR-P2: read the [billing] block from apid.toml first, then
	// overlay env on top. Missing file is non-fatal (defaults). Bad
	// TOML is fatal. The loader sees the merged cfg + the raw env
	// reader (closures re-read the secrets the overlay just wrote).
	loadBillingConfig := deps.loadBillingConfig
	if loadBillingConfig == nil {
		loadBillingConfig = billingloader.LoadBillingConfigFromPath
	}
	billingCfg, err := loadBillingConfig(deps.configPath)
	if err != nil {
		return fmt.Errorf("apid: load billing config: %w", err)
	}
	billingCfg = billingloader.ApplyBillingEnvOverlay(billingCfg, deps.getenv)
	billingProv, provName, err := billingloader.LoadProviderForAPID(ctx, billingCfg, deps.getenv, log)
	if err != nil {
		return fmt.Errorf("apid: load billing provider: %w", err)
	}
	if billingProv != nil {
		srv.WithBillingProvider(billingProv)
	}
	log.Info("billing provider loaded", "provider", provName)

	// Issue #299 / ADR-038 Phase 3: SBOM root directory. imagd's syft
	// populator writes CycloneDX JSON to <root>/sboms/<buildID>.cdx.json
	// and stores the relative path in build_provenance.sbom_storage_key.
	// apid joins the relative path against this root at GET
	// /v1/builds/{id}/sbom time. Default is the single-box deploy root
	// (/srv/fc, FAAS_STORAGE_ROOT for the local storage backend); on a
	// remote-storage deploy the operator sets FAAS_SBOM_ROOT to the
	// mirror mount. Empty disables the route — the handler returns 503
	// build_sbom_unavailable (issue #299: "may exist later, retry") so
	// the CLI/SDK can distinguish from 404 "no such build".
	srv.WithSBOMRoot(deps.getenv("FAAS_SBOM_ROOT"))

	// Issue #98 / ADR-028: admin allowlist for /v1/compute-nodes.
	// Empty in dev = all admin routes 403 with code admin_required;
	// production sets FAAS_ADMIN_EMAILS to the operator team's
	// comma-separated addresses. The allowlist is read at startup,
	// so a config change requires a restart — acceptable for the
	// tiny operator surface that exists today.
	srv.WithAdminAllowlist(deps.getenv("FAAS_ADMIN_EMAILS"))

	// Prometheus registry + ops observer middleware (this PR).
	// Built unconditionally so /metrics works even with FAAS_APID_METRICS_ADDR
	// unset (the daemon stays up; only the listener is skipped below).
	ops := wire.NewOpsMetrics("apid")
	srv.WithOpsMetrics(ctx, ops)
	// issue #517 / PR-C / ADR-064: thread the events Platform
	// into the server so the audit subscriber (which receives
	// the signature-rejection kinds from imaged's verify hook)
	// can emit the typed wake.deploy_failed row. nil opts out
	// (the unit tests don't wire it). The Platform writes the
	// events row + bumps wake_phase_emitted_total.
	eventsPlatform := events.NewPlatform("apid", store, log, ops, nil)
	srv.WithEventsPlatform(eventsPlatform)

	// Status page (spec §12 public surface). The Prometheus URL is
	// the local box's Prometheus installed by deploy/ansible/roles/
	// prometheus (default :9090 on the bridge). The HTML path defaults
	// to /etc/faas/statuspage/index.html; a dev override
	// (FAAS_STATUSPAGE_PATH) lets us point at deploy/statuspage/
	// index.html without installing.
	srv.WithStatusCache(
		deps.getenv("FAAS_PROMETHEUS_URL"),
		deps.getenv("FAAS_STATUSPAGE_PATH"),
	)

	// G2: load the host age recipient so the secrets PUT handler can seal.
	// vmmd owns the private half; we only need the public recipient string.
	// The recipient path is opt-in via FAAS_HOST_AGE_RECIPIENT_PATH — set in
	// production (and by the e2e harness) to the file vmmd writes
	// (/etc/faas/secrets/host.age.pub by default). When the env var is unset,
	// the var stays nil and PUT /secrets returns 503 — a loud, observable
	// signal that the box is misconfigured rather than a silent accept-and-
	// drop of plaintext. The unit tests don't set the var because the
	// handlers they're checking don't exercise the seal path.
	if recipientPath := deps.getenv("FAAS_HOST_AGE_RECIPIENT_PATH"); recipientPath != "" {
		r, err := secretbox.LoadRecipient(recipientPath)
		if err != nil {
			return fmt.Errorf("apid: load host age recipient %q: %w", recipientPath, err)
		}
		setSecretRecipient = func() *age.X25519Recipient { return r }
		// Issue #463 / ADR-068: the sidecar seal helper reuses the
		// same host age recipient (one age identity per host). A
		// separate getter keeps the seal helpers testable in
		// isolation without leaking the secret-handler test seam.
		setSidecarRecipient = func() *age.X25519Recipient { return r }
		log.Info("host age recipient loaded", "path", recipientPath)
	} else {
		log.Warn("FAAS_HOST_AGE_RECIPIENT_PATH unset — secrets PUT will return 503")
	}

	// MFA (IAM-2, issue #186): load the host age identity so
	// /v1/account/mfa/confirm and /verify can unseal the TOTP
	// secret. Same key file vmmd reads on wake. The identity
	// stays in-process; we never log it or write it to disk.
	// FAAS_HOST_AGE_IDENTITY_PATH is required only when MFA is
	// in use — without it, /enroll still works (recipient-only)
	// but /confirm /verify /disable /recover all 503.
	//
	// Issue #316 / ADR-057: we also load host.age.previous via
	// LoadHostKeys(dir) and wire the slice through SetMFAIdentities
	// so the 30-day rotation overlap window unseals envelopes
	// sealed under the previous key. The single-identity SetMFAIdentity
	// stays wired for backward compat with the existing tests.
	if identityPath := deps.getenv("FAAS_HOST_AGE_IDENTITY_PATH"); identityPath != "" {
		ident, err := secretbox.LoadHostKey(identityPath)
		if err != nil {
			return fmt.Errorf("apid: load host age identity %q: %w", identityPath, err)
		}
		SetMFARecipient(func() *age.X25519Recipient { return ident.Recipient() })
		SetMFAIdentity(func() *age.X25519Identity { return ident })
		log.Info("host age identity loaded for MFA", "path", identityPath)

		// Rotation-overlap wiring: load the multi-identity slice from
		// the same directory. If LoadHostKeys fails (e.g. .previous
		// mode tripwire fired mid-deploy) we keep the single-identity
		// path wired and log a Warn — the box is still unsealing
		// envelopes under the current key, just not the previous one.
		// A hard error would lock every MFA customer out, which is
		// worse than the operator-visible degraded-mode log line.
		identities, loadErr := secretbox.LoadHostKeys(filepath.Dir(identityPath))
		if loadErr != nil {
			log.Warn("apid: LoadHostKeys (rotation overlap) failed; MFA unseal will work only for envelopes sealed under the current host.age",
				"dir", filepath.Dir(identityPath), "err", loadErr.Error())
		} else {
			SetMFAIdentities(func() []*age.X25519Identity { return identities })
			if len(identities) > 1 {
				log.Info("apid: rotation overlap active — MFA unseal falls back across current + previous host.age",
					"current", identities[0].Recipient().String(),
					"previous", identities[1].Recipient().String())
			}
		}
	} else {
		log.Warn("FAAS_HOST_AGE_IDENTITY_PATH unset — MFA /confirm, /verify, /disable, /recover will return 503")
	}

	// Issue #286 / CodeQL alert #121: load (or generate + persist)
	// a per-box audit HMAC key and wire it into pkg/auth so
	// HashEmail uses HMAC-SHA256 instead of plain SHA-256. The
	// plain SHA-256 form is rainbow-table-reversible — a leaked
	// `events.data` column lets an adversary precompute hashes for
	// common emails and reverse the column. HMAC keyed by a
	// per-box secret closes that path while preserving the audit-
	// row join-key contract (deterministic for a given (email,
	// secret) pair). The key is held in-process only — never
	// written to events, never logged, never returned from any
	// HTTP handler.
	//
	// Loading precedence:
	//   1. FAAS_AUDIT_HMAC_KEY env var (hex-encoded 32 bytes) — the
	//      operator-supplied path; production uses this so the key
	//      survives container restarts via the env-var mount
	//      (Kubernetes secret, systemd EnvironmentFile=).
	//   2. /var/lib/faas/audit-hmac.key (0o600) — the auto-generated
	//      fallback. Generated once on first boot, persisted with
	//      0o600 perms so it survives daemon restart without
	//      requiring operator action. The file path is gated on
	//      FAAS_AUDIT_HMAC_KEY_FILE for tests / non-standard
	//      deployments.
	//   3. nil (zero-key fallback) — only if both above paths fail.
	//      pkg/auth logs a Warn; HashEmail still produces a stable
	//      join key, just without rainbow-table resistance.
	auditHMACKey, err := loadOrGenerateAuditHMACKey(deps.getenv, log)
	if err != nil {
		return fmt.Errorf("apid: load or generate audit HMAC key: %w", err)
	}
	auth.SetHMACSecret(auditHMACKey, log)

	// Recovery-code HMAC key — closes the SHA-256 rainbow-reversal
	// threat on a leaked PG blob (logical change 7 of the IAM-hardening
	// mega-PR, ADR-035 §"Rejected alternatives" #4). The recovery-code
	// hash column is bytea; the digest algorithm is HMAC-SHA256 keyed
	// by a per-box secret. The audit-hmac.key path above uses a
	// Warn-and-continue zero-key fallback because HashEmail joins
	// can degrade gracefully to "this row's join key is rainbow-
	// reversible" — there is no service-level outage. The recovery-
	// code path is different: the recovery code IS the only fallback
	// when the customer's TOTP device is lost. A zero-key HMAC gives
	// exactly the same threat surface as bare SHA-256, so refusing
	// to start is the correct tradeoff — "no service" beats "no
	// defence" for the recovery fall-back path.
	//
	// Loading precedence (mirrors the audit-HMAC loader, with one
	// difference: there is no precedence 3 zero-key fallback):
	//   1. FAAS_MFA_RECOVERY_HMAC_KEY env var (hex-encoded 32 bytes).
	//   2. /var/lib/faas/recovery-hmac.key (0o600) — auto-generated
	//      on first boot, persists across restarts so newly-stamped
	//      rows remain stable across boots (otherwise every restart
	//      would rotate the key and break the entire MFA cohort's
	//      recovery-code store — every customer would have to re-enroll).
	//      The path is gated on FAAS_RECOVERY_HMAC_KEY_FILE for tests
	//      and non-standard state dirs.
	//   3. FAIL — boot refuses. The audit-hmac.key loader returns nil
	//      when no key is configured; this loader returns an error
	//      instead. The error propagates as a top-level run() error and
	//      systemd brings apid back into the failed-state pattern.
	recoveryHMACKey, err := loadOrGenerateRecoveryHMACKey(deps.getenv, log)
	if err != nil {
		return fmt.Errorf("apid: load or generate recovery HMAC key: %w", err)
	}
	if err := authcode.SetHMACSecret(recoveryHMACKey); err != nil {
		// defence-in-depth: SetHMACSecret returns ErrNoHMACKey if the
		// loader above somehow handed back an empty slice (e.g. a
		// future refactor that adds a nil-tolerant branch). The
		// loader is the canonical gate, but the second check here
		// closes a foot-gun class of bugs.
		return fmt.Errorf("apid: set recovery HMAC secret: %w", err)
	}

	// Optional pre-listen hook (DNS poller in production; nil in tests).
	if deps.bgBefore != nil {
		deps.bgBefore(ctx, log, srv)
	}

	httpSrv := deps.newSrv(listenAddr, srv.handler())

	l, err := deps.listen("tcp", listenAddr)
	if err != nil {
		return err
	}

	// Optional /metrics listener (this PR). Sits on its own bind
	// address so a port collision can't take the daemon down. Empty
	// FAAS_APID_METRICS_ADDR = no listener (the scrape observer is
	// still wired into the main mux via observeWrap; only the
	// listener is skipped). Mirrors cmd/builderd/main.go:146-157.
	var metricsSrv *http.Server
	if metricsAddr != "" {
		metricsSrv = &http.Server{
			Addr:              metricsAddr,
			Handler:           ops.Handler(),
			ReadHeaderTimeout: 10 * time.Second,
		}
		mLis, err := net.Listen("tcp", metricsAddr)
		if err != nil {
			_ = l.Close()
			return fmt.Errorf("apid: metrics listen %q: %w", metricsAddr, err)
		}
		go func() {
			log.Info("apid /metrics listening", "addr", metricsAddr)
			if err := metricsSrv.Serve(mLis); err != nil && err != http.ErrServerClosed {
				log.Error("apid /metrics serve", "err", err)
			}
		}()
	}

	// Wave 0 PR-C / ADR-047: stateless-advisory gRPC listener. vmmd
	// dials /run/faas/apid.sock to forward fanotify batches from
	// guest-init. Empty FAAS_APID_ADVISORY_SOCK disables (matches the
	// metricsAddr explicit-empty pattern so the e2e harness can stamp
	// empty and avoid the bind race).
	//
	// ADR-052: when the target is tcp:// or dns:// (multi-box path),
	// the operator must also set FAAS_APID_ADVISORY_TLS_{CERT,KEY,CA}_PATH
	// to a per-daemon leaf. Single-box deployments leave the TLS
	// cluster unset and continue to use the unix socket; the
	// LoadServerTLSConfig helper returns (nil, nil) when all three
	// paths are empty.
	var advisorySrv *grpc.Server
	var advisoryLis net.Listener
	if sock := resolveAdvisorySock(deps.getenv); sock != "" {
		advisoryTLS, tlsErr := wire.LoadServerTLSConfig(
			deps.getenv("FAAS_APID_ADVISORY_TLS_CERT_PATH"),
			deps.getenv("FAAS_APID_ADVISORY_TLS_KEY_PATH"),
			deps.getenv("FAAS_APID_ADVISORY_TLS_CA_PATH"),
		)
		if tlsErr != nil {
			_ = l.Close()
			return fmt.Errorf("apid: advisory TLS: %w", tlsErr)
		}
		advisorySrv, advisoryLis, err = runAdvisoryServer(ctx, sock, advisoryTLS, srv.store, srv.audit, srv.notif, log, srv.ops)
		if err != nil {
			_ = l.Close()
			return fmt.Errorf("apid: advisory listen %q: %w", sock, err)
		}
		go func() {
			log.Info("apid advisory listening", "sock", sock)
			if err := advisorySrv.Serve(advisoryLis); err != nil {
				log.Error("apid advisory serve", "err", err)
			}
		}()
	}

	// Issue #432 phase 5: githubd → apid build-enqueue bridge
	// (separate listener from the advisory socket so an operator
	// can disable one without the other). Empty sock disables the
	// listener entirely (matches the advisory listener's pattern).
	// The bridge receiver implementation is in githubd_bridge.go;
	// it implements the githubdpb.GithubdServer interface (only
	// EnqueueBuild is wired; the rest is UnimplementedGithubdServer).
	var bridgeSrv *grpc.Server
	var bridgeLis net.Listener
	if sock := resolveGithubdBridgeSock(deps.getenv); sock != "" {
		bridgeTLS, tlsErr := wire.LoadServerTLSConfig(
			deps.getenv("FAAS_APID_GITHUBD_BRIDGE_TLS_CERT_PATH"),
			deps.getenv("FAAS_APID_GITHUBD_BRIDGE_TLS_KEY_PATH"),
			deps.getenv("FAAS_APID_GITHUBD_BRIDGE_TLS_CA_PATH"),
		)
		if tlsErr != nil {
			_ = l.Close()
			return fmt.Errorf("apid: githubd bridge TLS: %w", tlsErr)
		}
		bridgeSrv, bridgeLis, err = runGithubdBridgeServer(ctx, sock, bridgeTLS, srv.store, srv.notif, log, srv.ops, spoolRoot(), resolveGithubdStagingRoot(deps.getenv))
		if err != nil {
			_ = l.Close()
			return fmt.Errorf("apid: githubd bridge listen %q: %w", sock, err)
		}
		go func() {
			log.Info("apid githubd bridge listening", "sock", sock)
			if err := bridgeSrv.Serve(bridgeLis); err != nil {
				log.Error("apid githubd bridge serve", "err", err)
			}
		}()
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("apid listening", "addr", listenAddr)
		if err := httpSrv.Serve(l); err != nil && err != http.ErrServerClosed {
			errc <- err
		}
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		//nolint:contextcheck // shutdown context must outlive request ctx; detached from caller per net/http contract.
		_ = httpSrv.Shutdown(shutdownCtx)
		if metricsSrv != nil {
			//nolint:contextcheck // shutdown context must outlive request ctx; detached from caller per net/http contract.
			_ = metricsSrv.Shutdown(shutdownCtx)
		}
		if advisorySrv != nil {
			// GracefulStop lets in-flight ForwardStatelessAdvisory
			// calls finish writing their audit row before exit.
			advisorySrv.GracefulStop()
		}
		if bridgeSrv != nil {
			// GracefulStop lets in-flight EnqueueBuild RPCs finish
			// writing the deployment + build rows before exit.
			// A non-graceful Stop would leave the githubd dispatcher
			// with a gRPC error mid-enqueue, which the dispatcher
			// handles with log + skip — but graceful is cheaper.
			bridgeSrv.GracefulStop()
		}
		// Issue #286: drain the async failed-login audit channel
		// so in-flight rows land in the events table before the
		// daemon exits. Close is idempotent — safe to call from
		// the shutdown path even if WithOpsMetrics wasn't wired
		// (the auditor's failedCh is nil and Close is a no-op).
		if srv.audit != nil {
			srv.audit.Close()
		}
		return nil
	}
}

// pgNotifier is the production Notifier — it just delegates to db.Notify.
type pgNotifier struct {
	pool *pgxpool.Pool
}

func (p pgNotifier) Notify(ctx context.Context, channel, payload string) error {
	return db.Notify(ctx, p.pool, channel, payload)
}

// Subscribe hands the SSE handler a live channel stream from the
// Postgres pool. Returns immediately if no channels are requested.
func (p pgNotifier) Subscribe(ctx context.Context, channels []string) (<-chan db.Notification, func(), error) {
	return db.Subscribe(ctx, p.pool, channels)
}

// WaitFor is the Move 2 long-poll sibling: per-request LISTEN + predicate
// filter. Thin wrapper around db.WaitForNotification so the Notifier
// interface stays the only thing the handlers depend on.
func (p pgNotifier) WaitFor(ctx context.Context, channel string, predicate func(payload string) bool, timeout time.Duration) (string, error) {
	return db.WaitForNotification(ctx, p.pool, channel, predicate, timeout)
}

// auditHMACKeyFile is the on-disk fallback path for the audit HMAC
// key when FAAS_AUDIT_HMAC_KEY is unset (CodeQL alert #121, issue
// #286). The file is created on first boot with 0o600 perms and
// persists across daemon restarts so the audit-row email_hash
// values remain stable across boots (otherwise every restart would
// rotate the key and break the join-key contract — the same email
// would hash to a different value, fragmenting the audit table).
//
// 0o600 perms because the file IS a secret: anyone with read access
// can derive the audit HMAC key and rainbow-table the audit table.
// Mirrors the host.age identity perms (0o400 read-only; PR #237).
const auditHMACKeyFile = "/var/lib/faas/audit-hmac.key"

// auditHMACKeyEnvVar is the env var name an operator sets to
// supply the audit HMAC key explicitly (hex-encoded 32 bytes). The
// env-var path is the production-recommended route (Kubernetes
// Secret + envFromSecret, systemd EnvironmentFile=). The file
// fallback is the dev / single-node convenience.
const auditHMACKeyEnvVar = "FAAS_AUDIT_HMAC_KEY"

// auditHMACKeyFileEnvVar overrides the default auditHMACKeyFile
// path. Exists so the e2e harness and operator with non-standard
// state dirs can pin the path; tests use this to redirect to a
// tmp dir.
const auditHMACKeyFileEnvVar = "FAAS_AUDIT_HMAC_KEY_FILE"

// loadOrGenerateAuditHMACKey returns the per-daemon audit HMAC key
// per the precedence documented at the call site. Returns nil with
// no error if neither the env var nor the fallback file path yields
// a key — pkg/auth.SetHMACSecret accepts nil and logs a Warn, so the
// daemon still boots (in dev mode) with the zero-key fallback.
//
// Errors are returned only when:
//   - FAAS_AUDIT_HMAC_KEY is set but is not valid hex / wrong length.
//     A malformed key is a hard error: silently using a partial key
//     would let the operator think they have rainbow-table
//     resistance when they don't.
//   - The fallback file path is unreachable (perm denied, fs error).
//     Hard error: dev-mode auto-generation needs the file to
//     persist the key across restarts.
//
// The env var takes precedence over the file path even if the file
// is older / shorter — operator intent is explicit.
func loadOrGenerateAuditHMACKey(getenv func(string) string, log *slog.Logger) ([]byte, error) {
	// Precedence 1: env var (production path).
	if hexStr := getenv(auditHMACKeyEnvVar); hexStr != "" {
		key, err := hex.DecodeString(hexStr)
		if err != nil {
			return nil, fmt.Errorf("decode %s hex: %w", auditHMACKeyEnvVar, err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("%s must decode to exactly 32 bytes (got %d); use `openssl rand -hex 32` to generate", auditHMACKeyEnvVar, len(key))
		}
		log.Info("audit HMAC key loaded from env", "env", auditHMACKeyEnvVar)
		return key, nil
	}

	// Precedence 2: file path (dev-mode auto-generated).
	path := auditHMACKeyFile
	if override := getenv(auditHMACKeyFileEnvVar); override != "" {
		path = override
	}
	data, err := os.ReadFile(path)
	if err == nil {
		key, decErr := hex.DecodeString(strings.TrimSpace(string(data)))
		if decErr != nil {
			return nil, fmt.Errorf("decode %s hex content: %w", path, decErr)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("%s content must decode to exactly 32 bytes (got %d); delete the file to regenerate", path, len(key))
		}
		log.Info("audit HMAC key loaded from file", "path", path)
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Precedence 3: generate + persist (dev-mode first boot). Skip
	// silently if /var/lib/faas doesn't exist (typical for tests
	// running as non-root) — the zero-key fallback in pkg/auth
	// will catch it.
	dir := filepath.Dir(path)
	if _, statErr := os.Stat(dir); statErr != nil {
		log.Warn("audit HMAC key file dir unavailable; running on zero-key fallback (HashEmail is rainbow-table-reversible)",
			"path", path, "err", statErr)
		return nil, nil
	}
	key, err := auth.GenerateHMACSecret()
	if err != nil {
		return nil, fmt.Errorf("generate audit HMAC key: %w", err)
	}
	encoded := hex.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		log.Warn("audit HMAC key auto-persist failed; running on in-process key (HashEmail joins break on restart)",
			"path", path, "err", err)
		return key, nil
	}
	log.Info("audit HMAC key generated and persisted", "path", path)
	return key, nil
}

// recoveryHMACKeyFile is the on-disk fallback path for the
// recovery-code HMAC key. The loader writes to this file on first
// boot when neither the env var nor an existing file is present.
//
// 0o600 perms because the file IS a secret: anyone with read
// access can derive the recovery HMAC key and, combined with a
// leaked PG blob, can compute hashes offline. Mirrors the host.age
// identity perms (0o400 read-only; PR #237) and the audit HMAC key
// file convention.
const recoveryHMACKeyFile = "/var/lib/faas/recovery-hmac.key"

// recoveryHMACKeyEnvVar is the env var name an operator sets to
// supply the recovery HMAC key explicitly (hex-encoded 32 bytes).
// Production uses this — Kubernetes Secret + envFromSecret, or
// systemd EnvironmentFile=. The file fallback is the dev /
// single-node convenience.
const recoveryHMACKeyEnvVar = "FAAS_MFA_RECOVERY_HMAC_KEY"

// recoveryHMACKeyFileEnvVar overrides the default
// recoveryHMACKeyFile path. Exists so the e2e harness and operators
// with non-standard state dirs can pin the path; tests use this
// to redirect to a tmp dir.
const recoveryHMACKeyFileEnvVar = "FAAS_RECOVERY_HMAC_KEY_FILE"

// loadOrGenerateRecoveryHMACKey returns the per-daemon recovery
// HMAC key per the precedence documented at the call site. STRICTER
// than loadOrGenerateAuditHMACKey: returns an error if no key can
// be loaded — the audit-hmac.key path falls back to a zero-key
// (with a Warn) because HashEmail can degrade gracefully; recovery
// codes cannot, so refusing to start is the correct tradeoff.
//
// Errors are returned when:
//   - FAAS_MFA_RECOVERY_HMAC_KEY is set but is not valid hex / wrong
//     length (hard error; a partial key would silently disable the
//     rainbow-table defence).
//   - The fallback file path is set but unreadable / malformed.
//   - The file does not exist AND /var/lib/faas is writable AND
//     generation succeeds — the loader persists the key. (This is
//     the dev-mode first-boot path; failure here is a hard error
//     rather than a zero-key fallback.)
//   - The loader reaches the "no key" path: env unset, file
//     missing, /var/lib/faas missing — the loader returns an
//     explicit error so the boot fails. Test paths can supply a
//     writable tmp via FAAS_RECOVERY_HMAC_KEY_FILE.
func loadOrGenerateRecoveryHMACKey(getenv func(string) string, log *slog.Logger) ([]byte, error) {
	// Precedence 1: env var (production path).
	if hexStr := getenv(recoveryHMACKeyEnvVar); hexStr != "" {
		key, err := hex.DecodeString(hexStr)
		if err != nil {
			return nil, fmt.Errorf("decode %s hex: %w", recoveryHMACKeyEnvVar, err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("%s must decode to exactly 32 bytes (got %d); use `openssl rand -hex 32` to generate", recoveryHMACKeyEnvVar, len(key))
		}
		log.Info("recovery HMAC key loaded from env", "env", recoveryHMACKeyEnvVar)
		return key, nil
	}

	// Precedence 2: file path (dev-mode auto-generated; survives
	// daemon restart so the stored MFA recovery-code hashes remain
	// stable across boots).
	path := recoveryHMACKeyFile
	if override := getenv(recoveryHMACKeyFileEnvVar); override != "" {
		path = override
	}
	data, err := os.ReadFile(path)
	if err == nil {
		key, decErr := hex.DecodeString(strings.TrimSpace(string(data)))
		if decErr != nil {
			return nil, fmt.Errorf("decode %s hex content: %w", path, decErr)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("%s content must decode to exactly 32 bytes (got %d); delete the file to regenerate", path, len(key))
		}
		log.Info("recovery HMAC key loaded from file", "path", path)
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Precedence 3: generate + persist (dev-mode first boot). The
	// audit-HMAC loader tolerates /var/lib/faas being missing
	// (test runs as non-root); the recovery-HMAC loader does NOT
	// — refusing to start is the correct strict-mode behaviour.
	dir := filepath.Dir(path)
	if _, statErr := os.Stat(dir); statErr != nil {
		return nil, fmt.Errorf("stat %s (refusing to start without a recovery HMAC key; set %s, %s, or symlink %s into a writable dir): %w",
			dir, recoveryHMACKeyEnvVar, recoveryHMACKeyFileEnvVar, recoveryHMACKeyFile, statErr)
	}
	key, err := authcode.GenerateRandomKey()
	if err != nil {
		return nil, fmt.Errorf("generate recovery HMAC key: %w", err)
	}
	encoded := hex.EncodeToString(key)
	if err := os.WriteFile(path, []byte(encoded+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("persist %s (refusing to start with an in-process-only key — the next restart would force every MFA-enrolled customer to re-enroll): %w",
			path, err)
	}
	log.Info("recovery HMAC key generated and persisted", "path", path)
	return key, nil
}

// runAdvisoryServer binds the AdvisoryService gRPC server onto a
// fresh /run/faas/apid.sock (or wherever FAAS_APID_ADVISORY_SOCK
// points). Single-box deployments point sock at a unix:// path;
// the owner is the apid daemon user (lookup via
// pkg/wire.ListenOrRecreateByName), the group is `faas` so vmmd can
// dial without root, and the mode is 0660 — the standing repo
// convention (pkg/wire.DefaultSocketMode). Multi-box deployments
// pass a tcp:// or dns:// target + a non-nil tlsCfg loaded via
// wire.LoadServerTLSConfig (ADR-052). Empty sock disables the
// listener entirely (matches the e2e harness path).
//
// Returns the server (caller calls Serve) and the listener. Errors
// here are fatal — without the advisory listener vmmd has no way to
// forward fanotify batches and the audit loop is silently broken.
func runAdvisoryServer(ctx context.Context, target string, tlsCfg *tls.Config, store state.Store, audit *auditor, notif Notifier, log *slog.Logger, ops *wire.OpsMetrics) (*grpc.Server, net.Listener, error) {
	// Guard: a tcp/dns target without TLS would silently build an
	// insecure server (wire.Listen returns raw TCP, ServerCredsOrEmpty
	// yields zero opts). Refuse; the operator must set the
	// FAAS_APID_ADVISORY_TLS_{CERT,KEY,CA}_PATH env trio. ADR-052.
	if !isUnixSocketPath(target) && tlsCfg == nil {
		return nil, nil, fmt.Errorf(
			"advisory: target %q is non-unix but %s is empty (set FAAS_APID_ADVISORY_TLS_CERT_PATH / KEY_PATH / CA_PATH or point the target at a unix socket for single-box mode)",
			target, "FAAS_APID_ADVISORY_TLS_*_PATH")
	}
	var lis net.Listener
	var err error
	if isUnixSocketPath(target) {
		lis, err = wire.ListenOrRecreateByName(target, "faas-apid")
	} else {
		lis, err = wire.Listen(ctx, target, tlsCfg)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("advisory listen: %w", err)
	}
	srv := grpc.NewServer(append(
		wire.ServerCredsOrEmpty(tlsCfg),
		wire.TraceServerOptions()...,
	)...)
	// Mega-PR B: pass ops so the receiver can increment
	// apid_stateless_advisory_events_total on each landed advisory.
	// The accessor is nil-receiver safe so the metric stays zero
	// when ops is nil (test path).
	registerAdvisoryReceiver(srv, store, audit, notif, log, ops)
	return srv, lis, nil
}

// runGithubdBridgeServer binds the githubd → apid build-enqueue
// gRPC server onto a fresh /run/faas/apid-githubd.sock (or wherever
// FAAS_APID_GITHUBD_BRIDGE_SOCK points). The githubd daemon dials
// this listener after the dispatcher fans out the touched apps
// and stages each app's RootDir subtree into its build-sources
// dir as a per-app .tar.gz (issue #432 phase 5).
//
// The DAC contract mirrors the advisory socket (0660 group
// `faas`) so githubd can dial without root, but the listener is
// separately configurable so an operator can disable the bridge
// for a single-box deployment that doesn't run githubd. Empty
// sock disables the listener entirely (matches the e2e harness
// path + macOS dev boxes where /run/faas is read-only).
//
// The receiver implementation is in githubd_bridge.go. The set
// of state.Store methods the receiver needs is consumed through
// the githubdBridgeStore interface so unit tests can pass a stub
// without a real pgxpool. The store/notif/log/ops are passed
// through by the production wiring code in run().
//
// Returns the server (caller calls Serve) and the listener. Errors
// here are fatal — without the bridge listener githubd has no
// way to enqueue builds and the dispatch path is silently
// degraded (every push hits the noopEnqueuer path).
func runGithubdBridgeServer(ctx context.Context, target string, tlsCfg *tls.Config, store githubdBridgeStore, notif githubdBridgeNotifier, log *slog.Logger, ops *wire.OpsMetrics, spool string, stagingRoot string) (*grpc.Server, net.Listener, error) {
	// Same multi-box guard as runAdvisoryServer — a tcp/dns
	// target without TLS would silently build an insecure server.
	// ADR-052.
	if !isUnixSocketPath(target) && tlsCfg == nil {
		return nil, nil, fmt.Errorf(
			"githubd bridge: target %q is non-unix but %s is empty (set FAAS_APID_GITHUBD_BRIDGE_TLS_CERT_PATH / KEY_PATH / CA_PATH or point the target at a unix socket for single-box mode)",
			target, "FAAS_APID_GITHUBD_BRIDGE_TLS_*_PATH")
	}
	var lis net.Listener
	var err error
	if isUnixSocketPath(target) {
		lis, err = wire.ListenOrRecreateByName(target, "faas-apid")
	} else {
		lis, err = wire.Listen(ctx, target, tlsCfg)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("githubd bridge listen: %w", err)
	}
	srv := grpc.NewServer(append(
		wire.ServerCredsOrEmpty(tlsCfg),
		wire.TraceServerOptions()...,
	)...)
	// Pass ops so the receiver can increment
	// apid_githubd_bridge_enqueued_total on each landed build.
	// The accessor is nil-receiver safe so the metric stays zero
	// when ops is nil (test path).
	registerGithubdBridge(srv, store, notif, log, ops, spool, stagingRoot)
	return srv, lis, nil
}

// isUnixSocketPath detects the legacy single-box dial target by
// checking for the unix:// scheme OR a bare absolute filesystem
// path (the historical FAAS_APID_ADVISORY_SOCK default). Anything
// else — host:port, dns://authority, tcp://host:port — is treated
// as a multi-box dial target that requires a non-nil tlsCfg.
func isUnixSocketPath(target string) bool {
	if strings.HasPrefix(target, "unix://") || strings.HasPrefix(target, "/") {
		return true
	}
	return false
}
