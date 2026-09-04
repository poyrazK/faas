// Command githubd — GitHub App integration daemon (spec §14 M7.5, ADR-012).
//
// githubd owns: push-webhook receiver, Checks-API status writer, OAuth
// callback handler, per-repo install-token cache. It is the SOLE outbound
// caller to api.github.com (Checks + install-token exchange); its inbound
// public surface is gatewayd-public at /webhooks/github (HMAC-verified at the
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
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"filippo.io/age"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/capdecl/runtimecheck"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gitfetch"
	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trace"
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
	// capCheck: DEPLOY-1 / ADR-075 capdecl gate seam (review
	// finding M2). nil → runtimecheck.MustCheckOnBoot(capsDecl,
	// log, nil) which exits on violation in production. Tests
	// inject func() error { return nil } to bypass the live
	// /proc/self/status check.
	capCheck func() error
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
	// DEPLOY-1 / ADR-075 capdecl gate. githubd is unprivileged —
	// no Allow, no Deny. The webhook receiver, the OAuth
	// callback, the install-token cache, the Checks API
	// writer, and the age-sealed install-token reads all run
	// inside the unit's systemd hardening (NoNewPrivileges,
	// ProtectSystem, PrivateTmp, ReadWritePaths=/run/faas).
	// Any future cap_ add lands here, not in the unit file. The
	// capCheck seam (review finding M2) lets tests stub the live
	// /proc/self/status check.
	capCheck := deps.capCheck
	if capCheck == nil {
		capCheck = func() error { return runtimecheck.MustCheckOnBoot(capsDecl, log, nil) }
	}
	if err := capCheck(); err != nil {
		return err
	}
	traceShutdown, traceErr := trace.InitTracer(ctx, "githubd", wire.Version, log)
	if traceErr != nil {
		return fmt.Errorf("githubd: init tracing: %w", traceErr)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := traceShutdown(shutdownCtx); err != nil {
			log.Warn("githubd: trace shutdown failed", "err", err)
		}
	}()

	cfg, err := LoadConfig(deps.configPath)
	if err != nil {
		return fmt.Errorf("githubd: config: %w", err)
	}
	// Gate-B box-role gate. githubd is a control-plane daemon —
	// it refuses to start under RoleComputeOnly. The role is
	// set from TOML or FAAS_GITHUBD_ROLE at deploy time; default
	// is RoleSingleBox so single-box dev boots unmoved.
	if err := role.Require("githubd", cfg.Role, role.RoleSingleBox, role.RoleControlPlane); err != nil {
		return err
	}
	// Mega-PR-A (issue #911 / ADR-110 PR-1): boot log carrying the
	// multi-box identity. Mirrors schedd/apid/meterd/gatewayd-public
	// so the playbook shape is identical across daemons.
	if cfg.NodeName != "" {
		log.Info("githubd owner node", "node_name", cfg.NodeName)
	} else {
		log.Info("githubd: legacy single-box (cfg.NodeName empty)")
	}

	pool, err := deps.openDB(ctx, "")
	if err != nil {
		return fmt.Errorf("githubd: open db: %w", err)
	}
	defer pool.Close()

	// PR-H (Phase 5, mega-PR-GH): the host age identity is
	// needed for two distinct paths:
	//
	//   1. RealService.SealOne / Open in the OAuth branch (slice 8
	//      scope). The cold-start rehydrate path unseals the
	//      stored install token at boot.
	//   2. installationSourceFetcher.Open in the push-dispatch
	//      path (this PR). Every inbound webhook needs an
	//      unsealed install token to fetch the codeload archive.
	//
	// Loading the identity unconditionally (before the OAuth
	// branch) keeps a single failure path: an operator who
	// hasn't provisioned host.age yet gets a fatal startup error
	// regardless of which daemon mode they want to run. The
	// 0o400 perm check inside LoadHostKey trips first.
	identity, err := secretbox.LoadHostKey(hostKeyPath())
	if err != nil {
		return fmt.Errorf("githubd: load host age identity: %w", err)
	}

	// PR-H: per-daemon audit + reconcile services. The auditor
	// carries actor="githubd" so every project.reconcile.* /
	// auth.install.* audit row the reconcile package emits
	// through this Auditor is correctly attributed. The
	// reconcile service shares the same state.Store as the
	// apid-side reconcile (one PgStore per daemon — no
	// cross-process shared state).
	store := state.NewPgStore(pool)
	ops := wire.NewOpsMetrics("githubd")
	wire.BootStamps(ctx, "githubd", ops)
	wire.RegisterDefaultOps(ops)
	ghAud := audit.New(store, log, ops, "githubd")
	ghReconcile := buildGithubdReconcileService(store, ghAud, log)

	// PR-H: source fetcher for the push-dispatch path. The
	// work dir is operator-configurable (default
	// /var/lib/faas/githubd) so the temp dirs created by
	// gitfetch don't accumulate on the root partition. The
	// installsAdapter is the same one the OAuth branch wires
	// into RealService.
	workDir := githubdWorkDir()
	if mkErr := os.MkdirAll(workDir, 0o750); mkErr != nil {
		return fmt.Errorf("githubd: create work dir: %w", mkErr)
	}
	gitFetcher := gitfetch.NewHTTPWithLimits(workDir, api.MustLimitsFor(api.PlanScale))
	installsAdapter := newStateInstallsAdapter(pool)
	storeAdapter := newStateBindingsAdapter(pool)
	source := newInstallationSourceFetcher(installsAdapter, gitFetcher, identity, log)

	// PR-D / ADR-012 §7 — per-tenant webhook secret resolver. The
	// pool is the same one state.Store wires through; the adapter
	// exposes the bytea Get/Upsert query shape pkg/githubd
	// expects. 60s TTL is the load-bearing default — short enough
	// that a misconfigured box recovers on its own, long enough
	// that the webhook hot path is dominated by cache hits.
	//
	// The janitor (1m tick) evicts expired entries so the map
	// stays bounded across the daemon's lifetime. Mirrors
	// TokenCache.StartJanitor.
	secretResolver := githubd.NewPGWebhookSecretResolver(newStateSecretStoreAdapter(pool), log, 60*time.Second)
	stopJanitor := secretResolver.StartJanitor(ctx)
	defer stopJanitor()

	// pg_notify bridge — apid emits on every
	// UpsertGithubWebhookSecret; we drop the cached entry so the
	// next webhook rebuilds from the DB (without waiting for the
	// 60s TTL). The trigger lives in
	// migrations/00212_github_webhook_secrets.sql. The
	// SubscribeWithReconnect boundary handles transient LISTEN
	// drops on its own; we just translate the payload into a
	// Invalidate call. Plain-text payload (the install_id is not
	// sensitive — it's the same id the wire decorator exposes).
	notifCh, err := db.SubscribeWithReconnect(ctx, pool, []string{db.NotifyGithubWebhookSecretChanged}, log)
	if err != nil {
		return fmt.Errorf("githubd: subscribe to %s: %w", db.NotifyGithubWebhookSecretChanged, err)
	}
	go func() {
		for n := range notifCh {
			if n.Channel != db.NotifyGithubWebhookSecretChanged {
				continue
			}
			// Payload is JSON {"installation_id":<bigint>}. Try a
			// parse first; on malformed payload, fall back to a
			// wholesale Invalidate-by-string so an upstream bug
			// doesn't permanently desync the cache.
			var p struct {
				InstallationID int64 `json:"installation_id"`
			}
			id := parseInstallationIDFromPayload(n.Payload, &p)
			if id == 0 {
				log.Warn("githubd: webhook secret: malformed notify payload, skipping Invalidate",
					"payload", n.Payload)
				continue
			}
			log.Info("githubd: webhook secret: invalidating cache on notify",
				"installation_id", id)
			secretResolver.Invalidate(id)
		}
	}()

	// Slice 7 Service skeleton (inbound webhook path).
	webhookSvc := githubd.NewService(log)
	// PR-GH.6 flip: wire Ops unconditionally so the
	// githubd_path_filter_total counter ticks on ALL push-dispatch
	// paths — including the credentials-missing branches below
	// that wire NewUnavailableChangedFiles. The previous
	// credentialed-only wiring at the success branch left Ops
	// nil on unprovisioned boxes, which silently dropped the
	// mode=error / mode=breaker_open increments and made the
	// FaasGithubdPathFilterDegraded alert unreachable in
	// production. Hoisting here means the operator's §12
	// dashboard reflects reality on every box.
	webhookSvc.Ops = ops
	webhookSvc.Bindings = storeAdapter
	webhookSvc.Installs = installsAdapter
	webhookSvc.Source = source
	webhookSvc.Reconcile = ghReconcile
	// Issue #432 phase 5: workDir is the root under which
	// the per-app source tarballs land during the dispatch
	// loop (pkg/githubd/staging.go). Same value as the
	// gitfetch temp dir hoisted at line 123-127; both
	// producers share the operator-controlled dir tree.
	webhookSvc.WorkDir = workDir
	// Issue #432 phase 5: build fan-out. The production
	// enqueuer dials the apid gRPC bridge via the
	// ApidBridgeClient (cmd/githubd/apid_bridge.go). The
	// apid handler (cmd/apid/githubd_bridge.go) creates
	// the deployment + build rows and emits the
	// build_queued pg_notify. Pre-PR-GH.5 the wiring was
	// a noopEnqueuer (synthetic buildIDs); the swap is
	// the load-bearing close-out for phase 5.
	//
	// The apidClient is the same one the bridge dial
	// returns below — apidBridgeClient returns the stub
	// when FAAS_APID_GITHUBD_BRIDGE_SOCK is empty, so
	// the dispatcher stays safe on a dev box where the
	// apid daemon isn't running.
	webhookSvc.Enqueuer = NewApidEnqueuer(newApidBridgeClient(ctx, os.Getenv("FAAS_APID_GITHUBD_BRIDGE_SOCK"), nil, log), log)

	// Slice 8 RealService (OAuth + Checks). Auth may be nil if
	// the GitHub App credentials aren't provisioned — the daemon
	// stays up but every OAuth / Checks call returns an error.
	// This is "fail-closed but stay-up": the webhook path
	// continues to work for any installation that's already
	// configured its webhook out-of-band.
	//
	// storeAdapter / installsAdapter / identity are already loaded
	// above (PR-H hoisted them so the webhook dispatch path can
	// reach them even when OAuth + Checks are disabled).
	var realSvc *githubd.RealService
	if appID := deps.readAppID(); appID != "" {
		keyPEM, kerr := deps.readKeyPEM()
		if kerr != nil {
			log.Warn("githubd: read app private key", "err", kerr)
			// PR-GH.6 flip: wrap the stub in the breaker so
			// operators see the natural error → breaker_open
			// progression after 3 pushes (matches the runbook
			// claim and avoids burning metric cardinality on
			// a static configuration error).
			webhookSvc.ChangedFiles = githubd.NewBreakerChangedFiles(githubd.NewUnavailableChangedFiles(), deps.now)
			log.Warn("githubd: GitHub App credentials not provisioned; wiring unavailable ChangedFiles stub (path-filter falls back to full rebuild with error-mode metric)")
		} else {
			clientID := os.Getenv("FAAS_GITHUB_APP_CLIENT_ID")
			clientSecret := os.Getenv("FAAS_GITHUB_APP_CLIENT_SECRET")
			auth, aerr := githubd.NewAppAuth(appID, keyPEM, deps.httpClient(), clientID, clientSecret)
			if aerr != nil {
				log.Warn("githubd: app auth init", "err", aerr)
				webhookSvc.ChangedFiles = githubd.NewBreakerChangedFiles(githubd.NewUnavailableChangedFiles(), deps.now)
				log.Warn("githubd: GitHub App credentials not provisioned; wiring unavailable ChangedFiles stub (path-filter falls back to full rebuild with error-mode metric)")
			} else {
				tokens := githubd.NewTokenCache(auth, 5*time.Minute)
				checks, cerr := githubd.NewChecksAPI(tokens, deps.httpClient(), storeAdapter)
				if cerr != nil {
					return fmt.Errorf("githubd: new checks api: %w", cerr)
				}
				// PR-C: load the host age keypair so the install
				// token can be sealed at rest (SealOne at mint)
				// and unsealed at cold-start rehydrate (Open).
				// LoadHostKey enforces 0o400 perms (strict
				// equality, MEMORY.md/host-age-0400-loadcredential-decouple);
				// failure here is fatal — without the identity
				// we can't unseal existing rows. PR-H hoisted
				// this to the top of runWithDeps; the OAuth branch
				// here just consumes the loaded identity.
				//
				// Issue #316 / ADR-057: also load the rotation-aware
				// multi-identity slice from the same dir so install
				// tokens sealed under the previous host.age remain
				// unsealed during the 30-day overlap window.
				// Degrade to single-identity (with a Warn) if
				// LoadHostKeys fails — the box still unseals
				// current-keyed envelopes, just not previous-keyed ones.
				var identities []*age.X25519Identity
				if dir := filepath.Dir(hostKeyPath()); dir != "" {
					if ids, loadErr := secretbox.LoadHostKeys(dir); loadErr != nil {
						log.Warn("githubd: LoadHostKeys (rotation overlap) failed; install-token unseal will work only for envelopes sealed under the current host.age",
							"dir", dir, "err", loadErr.Error())
					} else {
						identities = ids
						if len(identities) > 1 {
							log.Info("githubd: rotation overlap active — install-token unseal falls back across current + previous host.age")
						}
					}
				}
				recipient, rerr := loadHostPubKey()
				if rerr != nil {
					return fmt.Errorf("githubd: load host age recipient: %w", rerr)
				}
				auditFn := newGithubdAuditFn(log)
				realSvc = githubd.NewRealService(auth, tokens, checks, storeAdapter, installsAdapter, recipient, identity, auditFn).
					WithStreamer(newSourceRefStreamer(installsAdapter, &tokenCacheAdapter{cache: tokens}, nil, log))
				if identities != nil {
					realSvc.Identities = identities
				}
				// Path-filtered build fan-out (ADR-050 §103-109).
				// Token resolution is per-call via the TokenCache
				// (Option A from the plan review) so the daemon
				// doesn't need to know about a specific install row
				// at boot. If the AppAuth init failed above the
				// field stays nil and service.go falls back to
				// full fan-out.
				//
				// Issue #432 phase 5: the inner client is wrapped
				// in a circuit breaker (NewBreakerChangedFiles)
				// so 3 consecutive compare-API failures trip
				// the breaker for 10 min and the dispatcher
				// falls back to full fan-out without further
				// load on the upstream. The breaker's mode label
				// (githubd_path_filter_total{breaker_open})
				// surfaces the trip in the §12 dashboard.
				inner := githubd.NewHTTPChangedFiles(tokens, deps.httpClient())
				webhookSvc.ChangedFiles = githubd.NewBreakerChangedFiles(inner, deps.now)
				// PR-D / ADR-012 §6 closure: wire the Checks
				// API writer to the post-enqueue hook at
				// pkg/githubd/service.go::HandlePushRequest.
				// The seam takes (ctx, repo, sha, phase); the
				// underlying ChecksAPI takes (logsURL,
				// summary) too — both are empty at the
				// queued hook (the build fan-out fills them
				// in when it lands via the gRPC path).
				webhookSvc.WriteCheck = func(ctx context.Context, repoFullName, commitSHA string, phase githubdgrpc.CheckPhase) error {
					return githubd.WriteCheckCoalesced(ctx, checks, repoFullName, commitSHA, phase, "", "")
				}
				// PR-A's preview Check Run seams were
				// declared on Service but never wired to the
				// live ChecksAPI — every preview event
				// posted `status=queued` but the Check Run
				// never reached GitHub. Issue #961 Mega-C
				// PR-1 leaf 3 closes the gap (and adds the
				// new destroy-comment seam alongside).
				webhookSvc.WritePreviewCheck = checks.WritePreviewCheck
				webhookSvc.WritePreviewCheckForkRefused = checks.WritePreviewCheckForkRefused
				webhookSvc.WritePreviewDestroyComment = checks.WritePreviewDestroyComment
				log.Info("githubd: OAuth + Checks wired", "app_id", appID)
			}
		}
	} else {
		log.Info("githubd: FAAS_GITHUB_APP_ID unset; OAuth + Checks disabled (webhook path only)")
		// PR-GH.6 flip: wrap the stub in the breaker so
		// operators see the natural error → breaker_open
		// progression after 3 pushes (matches the runbook
		// claim and avoids burning metric cardinality on
		// a static configuration error).
		webhookSvc.ChangedFiles = githubd.NewBreakerChangedFiles(githubd.NewUnavailableChangedFiles(), deps.now)
		log.Warn("githubd: GitHub App credentials not provisioned; wiring unavailable ChangedFiles stub (path-filter falls back to full rebuild with error-mode metric)")
	}

	// The gRPC server hands out the RealService (full slice 8
	// surface) when available, else falls back to a Unimplemented
	// stub so the gRPC plumbing stays healthy even without OAuth.
	gRPCImpl := githubdgrpc.Service(githubdgrpc.UnimplementedService{})
	if realSvc != nil {
		gRPCImpl = realSvc
	}

	// ops: hoisted above (PR-H moved it next to the Auditor
	// construction so the per-daemon registry is shared by
	// audit + reconcile + sync.Mutex-free observer paths).
	//
	// Issue #571 / PR-A2: githubd /readyz probe. Three signals —
	// PG ping (binding store / secret resolver both round-trip
	// the pool on every push), GitHub App credentials loaded
	// (realSvc != nil; nil means OAuth + Checks are unavailable
	// and the dispatcher falls back to full fan-out with the
	// breaker_open metric label), and the webhook secret
	// resolver wired (always set in production; nil = deploy
	// misconfig where the per-tenant path fell back to the
	// platform-wide env).
	githubdProbe := githubd.BuildReadinessProbe(ctx, pool,
		func() bool { return realSvc != nil },
		func() bool { return secretResolver != nil },
	)
	githubdProbe.SetReadyObserver(func(ready bool, reason string) {
		ops.MarkReady("githubd", ready, reason)
	})

	srv := &githubd.Server{
		Service:        webhookSvc,
		Log:            log,
		Ops:            ops,
		GRPCServer:     githubdgrpc.New(gRPCImpl, ops, log),
		HTTPAddr:       cfg.HTTPAddr,
		SocketPath:     cfg.SocketPath,
		ListenAddr:     cfg.ListenAddr,
		TLSCertPath:    cfg.TLSCertPath,
		TLSKeyPath:     cfg.TLSKeyPath,
		TLSCAPath:      cfg.TLSCAPath,
		SecretResolver: secretResolver,
		ReadyFunc:      githubdProbe.ReadyFunc(),
		ReasonFunc:     githubdProbe.ReasonFunc(),
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

// buildGithubdReconcileService constructs the githubd-side
// reconcile.Service using the per-daemon *audit.Auditor. Actor
// is "githubd" so every project.reconcile.* / auth.install.*
// audit row emitted through this service is correctly
// attributed. The same store powers both the DAO and the
// audit reader — no cross-process shared state.
func buildGithubdReconcileService(store state.Store, aud *audit.Auditor, log *slog.Logger) *reconcile.Service {
	return reconcile.NewService(store, aud, log)
}

// githubdWorkDir returns the root directory under which
// gitfetch creates per-push temp dirs. Override via
// FAAS_GITHUBD_WORK_DIR. The default matches the spec §11 disk
// layout. The directory is created at startup (mode 0750)
// so the gitfetch.NewHTTP(workDir) call doesn't fail with
// ENOENT on the first push.
func githubdWorkDir() string {
	if p := os.Getenv("FAAS_GITHUBD_WORK_DIR"); p != "" {
		return p
	}
	return "/var/lib/faas/githubd"
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
	// Issue #603: gate on the file mode BEFORE reading. bootstrap
	// writes this file, but nothing enforced its mode afterwards —
	// on a stray umask the GitHub App private key lands 0644 and
	// every user on the box can mint installation tokens for every
	// customer repo. Fail-loud at startup instead: a daemon that
	// refuses to start is a page; a world-readable private key is
	// silent until it is someone else's incident.
	if err := secretbox.AssertSecretFileMode("githubd", path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is operator-controlled
	if err != nil {
		return nil, fmt.Errorf("githubd: read app key %q: %w", path, err)
	}
	return data, nil
}

// hostKeyPath returns the path to the host age private key. Used
// by ensureInstallToken's cold-start rehydrate path to unseal
// stored install tokens. The default matches the rest of the
// secrets tree (spec §11: /etc/faas/secrets/host.age, mode 0400
// root:root); respects FAAS_HOST_AGE_IDENTITY_PATH (systemd
// LoadCredential indirection per spec §11) and FAAS_HOST_AGE_KEY.
//
// Issue #316 / ADR-057: the previous default had a stray `.key`
// suffix (/etc/faas/secrets/host.age.key) that didn't match any
// other component's path. After a host.age rotation the rename
// would have moved the canonical file to host.age.previous, but
// githubd would have continued looking for host.age.key and
// silently failed every unseal. Reconciled to host.age here so
// LoadHostKeys(dir) (current + previous) returns the same pair
// every daemon consumes.
func hostKeyPath() string {
	if p := os.Getenv("FAAS_HOST_AGE_IDENTITY_PATH"); p != "" {
		return p
	}
	if p := os.Getenv("FAAS_HOST_AGE_KEY"); p != "" {
		return p
	}
	return secretbox.DefaultHostKeyPath
}

// hostPubKeyPath returns the path to the host age public key. Used
// by ExchangeOAuthCode's seal-at-mint path. Mode 0444 expected.
func hostPubKeyPath() string {
	if p := os.Getenv("FAAS_HOST_AGE_PUB"); p != "" {
		return p
	}
	return "/etc/faas/secrets/host.age.pub"
}

// loadHostPubKey reads the host age public key from disk and
// parses it as an X25519 recipient. The public half is
// world-readable, so no perm check beyond the file being readable.
// (Strict 0o400 enforcement is on the PRIVATE half via LoadHostKey;
// the public half just needs to be readable to the daemon.)
func loadHostPubKey() (*age.X25519Recipient, error) {
	path := hostPubKeyPath()
	data, err := os.ReadFile(path) //nolint:gosec // path is operator-controlled
	if err != nil {
		return nil, fmt.Errorf("githubd: read host age pub %q: %w", path, err)
	}
	id, err := age.ParseX25519Recipient(string(data))
	if err != nil {
		return nil, fmt.Errorf("githubd: parse host age pub %q: %w", path, err)
	}
	return id, nil
}

// newGithubdAuditFn returns the AuditEvent callback RealService
// invokes to emit auth.install.* events. Today it just JSON-logs
// at info level; a future wiring can forward to apid's audit
// event emitter (PR-D scope — the inbound-webhook path needs the
// same audit sink so the §11 paper trail is unified).
//
// The event names match the apid-side audit taxonomy
// (auth.install.verified / .token_sealed / .takeover_rejected /
// .unauthenticated from PR-A + PR-B + PR-C).
func newGithubdAuditFn(log *slog.Logger) githubd.AuditEvent {
	return func(event string, accountID string, payload map[string]any) {
		log.Info("githubd audit",
			"event", event,
			"account_id", accountID,
			"payload", payload)
	}
}

// Compile-time guards: keep imports stable for tests / future slices.
var (
	_ = rsa.PrivateKey{}
	_ = depsAdapter{}
)

// depsAdapter is reserved for the test seam in pkg/githubd tests
// that import cmd/githubd internals.
type depsAdapter struct{}

// parseInstallationIDFromPayload decodes the JSON
// {"installation_id":<bigint>} payload from the
// github_webhook_secret_changed pg_notify channel. Returns 0 on
// malformed input — the caller logs and skips the Invalidate so
// a corrupt payload never poisons the cache.
//
// The payload is treated as untrusted input (any daemon with
// pg_notify write access can craft strings; per the
// notify.go convention we treat the payload as adversarial).
// We do NOT cross-validate the id against the row before
// Invalidate — the resolver's Resolve() will re-read on the next
// miss and the worst case is a spurious DB read.
func parseInstallationIDFromPayload(payload string, p *struct {
	InstallationID int64 `json:"installation_id"`
}) int64 {
	if err := json.Unmarshal([]byte(payload), p); err == nil && p.InstallationID > 0 {
		return p.InstallationID
	}
	// Fallback: the payload MIGHT be a bare integer (e.g. a
	// hand-rolled debug NOTIFY). Defensive parse so an upstream
	// rewrite doesn't permanently desync.
	if id, err := strconv.ParseInt(payload, 10, 64); err == nil && id > 0 {
		return id
	}
	return 0
}
