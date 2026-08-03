// Command schedd — scheduler and instance-lifecycle owner (spec §4.3).
//
// schedd is the ONLY writer to the instances table and the sole owner of the
// state machine (spec §Component ownership, §6). It runs admission control, the
// idle reaper, eviction, and cron in one process — single writer, no distributed
// locking. It serves a gRPC Wake/ReportActivity surface to gatewayd on
// /run/faas/schedd.sock (ADR-018) and dials vmmd on /run/faas/vmmd.sock to drive
// the microVM lifecycle (ADR-014).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/sched/flowcount"
	"github.com/onebox-faas/faas/pkg/sched/instancestats"
	"github.com/onebox-faas/faas/pkg/sched/recentload"
	"github.com/onebox-faas/faas/pkg/sched/scaleup"
	"github.com/onebox-faas/faas/pkg/sched/targets"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
)

const metricsPath = "/metrics"

func main() {
	wire.Daemon("schedd", run)
}

// runDeps is the dependency-injection seam for testing. Production uses the
// defaults; tests swap fields to drive run without Postgres, KVM, or a socket.
type runDeps struct {
	configPath string
	openDB     func(context.Context, string) (*pgxpool.Pool, error)
	migrate    func(context.Context, *pgxpool.Pool) error
	detectFC   func(context.Context) (string, error)
	dialVMM    func(ctx context.Context, target string, tlsCfg *tls.Config) (sched.VMM, error)
	listen     func(ctx context.Context, target string, tlsCfg *tls.Config, owner string) (net.Listener, error)
	// subscribeDeletion is the producer-side seam for the
	// NotifyAccountDeletionPending consumer (ADR-026). nil = the
	// subscriber is not started (cmd/schedd's main wires the
	// production db.Subscribe adapter; tests inject a fake).
	subscribeDeletion func(context.Context, *pgxpool.Pool) (<-chan db.Notification, func(), error)
	// subscribeEgressDrift (tier-2 PR-B, ADR-031 + ADR-033) is the
	// producer-side seam for the app_changed consumer that fans
	// out per-app egress_allowlist updates to every vmmd that
	// owns a live instance of the app (the "live-instance drift"
	// closure). nil = the subscriber is not started; the loop's
	// existing app_changed consumer stays as the logging-only
	// fallback. Tests inject a fake channel.
	subscribeEgressDrift func(context.Context, *pgxpool.Pool) (<-chan db.Notification, func(), error)
	// subscribePlacementClaim (Phase 2 / Gate A, migration 00084)
	// is the producer-side seam for the app_changed consumer that
	// atomically stamps apps.node_id via Engine.ClaimUnplaced.
	// The original plan placed the chooser in apid; the depguard
	// rule apid-control-plane-only forbids apid from importing
	// pkg/sched, so the chooser moved here. apid writes apps with
	// node_id = NULL; schedd races to stamp the owner on
	// kind="created". nil = the subscriber is not started; the
	// cold-start sweep still runs so an unplaced app from a prior
	// run gets reconciled on boot.
	subscribePlacementClaim func(context.Context, *pgxpool.Pool) (<-chan db.Notification, func(), error)
	// subscribeRebalancer (Tier A4 / ADR-064) is the
	// producer-side seam for the compute_node_changed consumer
	// that drains orphans from a freshly-inactive compute_node
	// onto the local schedd. Mirrors subscribePlacementClaim's
	// shape; nil = subscriber not started (tests that don't
	// want the channel can leave it nil — the cold-start sweep
	// below still reconciles orphans on boot).
	subscribeRebalancer func(context.Context, *pgxpool.Pool) (<-chan db.Notification, func(), error)
	// subscribeLiveMigrator (Tier A5 / ADR-066) is the
	// producer-side seam for the compute_node_changed consumer
	// that migrates live instances (state in {WAKING,
	// COLD_BOOTING, RUNNING, SNAPSHOTTING}) from a freshly-
	// inactive compute_node onto the local schedd via the
	// four-phase handoff. Mirrors subscribeRebalancer's shape;
	// nil = subscriber not started (the cold-start sweep
	// below still reconciles live instances on boot).
	subscribeLiveMigrator func(context.Context, *pgxpool.Pool) (<-chan db.Notification, func(), error)
	// subscribeNodeKeyChanges (ADR-053) is the producer-side seam
	// for the 'compute_node_changed' pg_notify consumer that
	// refreshes the in-memory NodeKeyRegistry on every relevant
	// INSERT/UPDATE/DELETE (migration 00075's trigger fires on
	// both compute_nodes AND compute_node_keys). nil = the
	// subscriber is not started; the initial Refresh at startup
	// still runs so a slice-3 schedd with no vmmd registered
	// yet has a coherent (empty) state. Tests inject a fake
	// channel.
	subscribeNodeKeyChanges func(context.Context, *pgxpool.Pool) (<-chan db.Notification, func(), error)
	// subscribeRouterRefresh (Tier A3) is the producer-side seam
	// for the compute_node_changed consumer that drops a stale
	// dialed client and reloads target_url into VMMRouter. The
	// first subscribe is critical-path (a vmmd URL rotation that
	// arrives before the daemon registered for events will silently
	// stale-dial the old URL until restart), so the wiring below
	// fails the boot inline rather than logging-and-continuing.
	// After boot, SubscribeWithReconnect handles transient LISTEN
	// failures internally (100ms→5s backoff).
	subscribeRouterRefresh func(context.Context, *pgxpool.Pool, *slog.Logger) (<-chan db.Notification, error)
	// heartbeatInterval overrides sched.DefaultHeartbeatInterval for
	// tests that want a sub-second cadence. Zero falls back to the
	// production default (30s).
	heartbeatInterval time.Duration
	// signPubPath overrides FAAS_SIGN_PUB / cosign.DefaultSignPubPath
	// for tests. ADR-038 / Tier 3 phase 3: the verifier loads the pub
	// PEM at startup and fails loud on missing/insecure perms. A unit
	// test that boots run() needs a valid temp file here, otherwise
	// the verifier step returns before any of the test's stubbed
	// deps (listen, dialVMM, etc.) get a chance to run. nil/empty
	// means the production envOr fallback fires.
	signPubPath string
}

func defaultDeps() runDeps {
	return runDeps{
		configPath: envOr("FAAS_SCHEDD_CONFIG", "/etc/faas/schedd.toml"),
		openDB:     db.Open,
		migrate:    db.MigrateUp,
		detectFC:   fcvm.DetectFirecrackerVersion,
		dialVMM: func(ctx context.Context, target string, tlsCfg *tls.Config) (sched.VMM, error) {
			return sched.DialVMMContext(ctx, target, tlsCfg)
		},
		listen: wire.ListenAs,
		// Production wires db.Subscribe. Tests inject a fake channel
		// so the subscriber's Park path is exercised end-to-end
		// without standing up Postgres.
		subscribeDeletion: func(ctx context.Context, p *pgxpool.Pool) (<-chan db.Notification, func(), error) {
			return db.Subscribe(ctx, p, []string{db.NotifyAccountDeletionPending})
		},
		// Production wires the same db.Subscribe primitive, scoped
		// to the app_changed channel. The egress_drift subscriber
		// filters to kind="updated" internally — wider-list
		// callers are safe.
		subscribeEgressDrift: func(ctx context.Context, p *pgxpool.Pool) (<-chan db.Notification, func(), error) {
			return db.Subscribe(ctx, p, []string{db.NotifyAppChanged})
		},
		// Phase 2 / Gate A: subscribe to NotifyAppChanged and let
		// PlacementClaimSubscriber filter to kind="created". The
		// subscriber is a no-op on every other kind. The
		// EgressDriftSubscriber above shares the same channel but
		// filters to kind="updated"; widening the LISTEN across
		// multiple subscribers is the canonical pattern (see
		// subscribeEgressDrift comment).
		subscribePlacementClaim: func(ctx context.Context, p *pgxpool.Pool) (<-chan db.Notification, func(), error) {
			return db.Subscribe(ctx, p, []string{db.NotifyAppChanged})
		},
		// Tier A4 / ADR-064: subscribe to compute_node_changed
		// and let Rebalancer filter to active=false. The router
		// watcher + nodekeys + nodeVerifier above already share
		// the same channel; the rebalancer is the fourth
		// consumer. Pre-commit review in ADR-064 §"Trigger" — a
		// single LISTEN shared by N subscribers is the canonical
		// pattern (subscribers filter on the typed payload).
		subscribeRebalancer: func(ctx context.Context, p *pgxpool.Pool) (<-chan db.Notification, func(), error) {
			return db.Subscribe(ctx, p, []string{db.NotifyComputeNodeChanged})
		},
		// Tier A5 / ADR-066: live-instance migration watcher.
		// Same channel as the parked-app rebalancer — both
		// filter to active=false + valid node_id and dispatch
		// to their respective Engine method. A separate
		// subscribe keeps the watcher logic (filter shape +
		// log context) cleanly scoped per ADR-066 §"Trigger".
		subscribeLiveMigrator: func(ctx context.Context, p *pgxpool.Pool) (<-chan db.Notification, func(), error) {
			return db.Subscribe(ctx, p, []string{db.NotifyComputeNodeChanged})
		},
		// Production wires db.Subscribe on the
		// 'compute_node_changed' channel. Migration 00075's
		// trigger fires on both compute_nodes AND
		// compute_node_keys writes, so a single subscription
		// covers both lifecycles. The registry's Run method
		// reruns Refresh on every notify (idempotent on the
		// ReplaceAll map swap).
		subscribeNodeKeyChanges: func(ctx context.Context, p *pgxpool.Pool) (<-chan db.Notification, func(), error) {
			return db.Subscribe(ctx, p, []string{db.NotifyComputeNodeChanged})
		},
		// Tier A3: a second LISTEN on compute_node_changed. The
		// node-key verifier above wraps its subscribe in a
		// goroutine because a transient LISTEN failure there is
		// a degraded (not fatal) state; the router refresh
		// watcher is critical-path for vmmd URL rotation
		// visibility, so we open a separate LISTEN backed by
		// SubscribeWithReconnect and fail the daemon inline if
		// the first Subscribe fails.
		subscribeRouterRefresh: func(ctx context.Context, p *pgxpool.Pool, l *slog.Logger) (<-chan db.Notification, error) {
			return db.SubscribeWithReconnect(ctx, p, []string{db.NotifyComputeNodeChanged}, l)
		},
	}
}

// envOr returns the value of env key, or fallback when unset/empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func run(ctx context.Context, log *slog.Logger) error {
	return runWithDeps(ctx, log, defaultDeps())
}

func runWithDeps(ctx context.Context, log *slog.Logger, deps runDeps) error {
	cfg, err := LoadConfig(deps.configPath)
	if err != nil {
		return err
	}
	listenTarget := cfg.ResolveListenTarget()
	vmmTarget := cfg.ResolveVMMTarget()
	log.Info("config",
		"listen_addr", listenTarget,
		"vmmd_target", vmmTarget,
		"socket", cfg.SocketPath,
		"vmmd_socket", cfg.VMMDSocket,
		"metrics_addr", cfg.MetricsAddr)

	pool, err := deps.openDB(ctx, cfg.DBURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := deps.migrate(ctx, pool); err != nil {
		return err
	}

	// ADR-056: handshake-layer NodeVerifier. Gated on cfg.NodeName
	// (the multi-box gate, mirroring vmmd's cfg.ComputeNode.NodeName
	// at cmd/vmmd/main.go:394). When the gate is open, schedd is
	// part of a multi-box deployment and the verifier sits in
	// front of every mTLS leg (server-side on the gatewayd-facing
	// surface, client-side on the vmmd dial). Empty NodeName =
	// single-box dev / pre-slice-3 schedd keeps the verifier off
	// entirely; stdlib chain + RFC 6125 SAN + EKU alone run.
	//
	// Refresh + Run only run when the gate is open. The factory
	// variants accept a nil verifier and degrade to the stdlib
	// trust path, so the closed-gate wiring reuses the same
	// LoadServerTLSWithVerifier / LoadVMMTLSWithVerifier call
	// sites as the open-gate wiring — a single code path.
	var nodeVerifier *wire.PGNodeVerifier
	if cfg.NodeName != "" {
		nodeVerifier = wire.NewPGNodeVerifier(wire.NewPGNodeLoader(pool), log)
		if _, err := nodeVerifier.Refresh(ctx); err != nil {
			return fmt.Errorf("schedd: node verifier startup refresh: %w", err)
		}
		// Last-known-good posture: the drain loop survives
		// transient Postgres blips because Refresh keeps the
		// previous snapshot on loader failure. A de-sync to "allow
		// nothing" would brick the cluster's mTLS legs (every
		// handshake would fail), so the contract is "best effort
		// refresh on every notify; never brick".
		go func() {
			ch, err := db.SubscribeWithReconnect(ctx, pool, []string{db.NotifyComputeNodeChanged}, log)
			if err != nil {
				log.Error("schedd: node verifier LISTEN failed", "err", err)
				return
			}
			if err := nodeVerifier.Run(ctx, ch); err != nil && !errors.Is(err, context.Canceled) {
				log.Error("schedd: node verifier exited", "err", err)
			}
		}()
	}

	// Snapshots load only on the Firecracker version that made them (ADR-005);
	// detect it so the engine restores compatible snapshots and cold boots the
	// rest.
	fcVersion, err := deps.detectFC(ctx)
	if err != nil {
		log.Warn("could not detect firecracker version; treating all snapshots as stale", "err", err)
	}

	// Issue #95 / ADR-025: dial vmmd through the location-transparent
	// helper. tcp/dns targets require the vmmd_tls_* cluster; nil TLS on
	// a unix target keeps single-box behaviour unchanged.
	vmmTLS, err := cfg.LoadVMMTLSWithVerifier(nodeVerifier)
	if err != nil {
		return fmt.Errorf("schedd: load vmmd TLS: %w", err)
	}
	store := state.NewPgStore(pool)

	// Phase 2 / Gate A: resolve this schedd's owner node id at
	// startup. Empty cfg.NodeName → empty owner (legacy
	// single-box posture, ownership guard short-circuits);
	// non-empty → compute_nodes.id. Failures (DB outage, missing
	// row, default-local collision, inactive node) exit fast
	// rather than silently falling back to in-process ownership.
	ownerNodeID, err := cfg.ResolveLocalNodeID(ctx, store)
	if err != nil {
		return fmt.Errorf("schedd: resolve local node id: %w", err)
	}
	if ownerNodeID != "" {
		log.Info("schedd owner node resolved", "node_name", cfg.NodeName, "node_id", ownerNodeID)
	} else {
		log.Info("schedd owner node: legacy single-box (cfg.NodeName empty; ownership guard short-circuits)")
	}

	// Issue #97 / ADR-025 axis 3: enumerate the active compute_nodes
	// once at startup and build a VMMRouter that dials vmmd per target
	// URL on demand. The legacy single-box fleet has exactly one row
	// (the synthetic 'default-local' seeded by migration 00024) so
	// the router degenerates to "dial that one vmmd.sock on first
	// RPC" — same behaviour as pre-#97, just behind a per-node lookup
	// that the Wake / Park / KillStuck flow now plumbs through.
	nodes, err := store.ActiveComputeNodes(ctx)
	if err != nil {
		// Treat ctx-cancellation as a clean shutdown — the test
		// suite cancels during the bootstrap ActiveComputeNodes
		// call to verify a clean drain (TestRun_DrainsOnCancel,
		// PR #115 coverage gate). Returning the wrapped error
		// would surface a non-nil error on what is in fact a
		// graceful exit. Real I/O failures keep returning the
		// wrapped error unchanged.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return fmt.Errorf("schedd: list active compute_nodes: %w", err)
	}
	nodeInfos := make([]sched.ComputeNodeInfo, 0, len(nodes))
	for _, n := range nodes {
		nodeInfos = append(nodeInfos, sched.ComputeNodeInfo{ID: n.ID, TargetURL: n.TargetURL})
	}
	vmmRouter := sched.NewVMMRouter(nodeInfos, deps.dialVMM, vmmTLS)

	// Tier A3: subscribe to compute_node_changed and refresh the
	// router's (nodeID, target_url) map on every payload. The
	// first subscribe is critical-path; on failure we fail the
	// boot inline (mirrors the inline Refresh call at lines
	// ~181-183 for the node-key verifier, but where that one is
	// best-effort, this one is critical because a missed notify
	// means vmmd URL rotations stale-dial silently until restart).
	// After boot, SubscribeWithReconnect drives the channel across
	// transient LISTEN failures.
	//
	// nil-deps.test-seam: a test that exercises a deeper failure
	// path (e.g. TestRun_ListenFailurePropagates) may build a
	// runDeps without this field set. Falling back to a closed
	// channel keeps the watcher alive but dormant — the test's
	// intended failure surface (the listen call further down)
	// still drives the assertion.
	subscribeRouterRefresh := deps.subscribeRouterRefresh
	if subscribeRouterRefresh == nil {
		subscribeRouterRefresh = func(context.Context, *pgxpool.Pool, *slog.Logger) (<-chan db.Notification, error) {
			ch := make(chan db.Notification)
			close(ch)
			return ch, nil
		}
	}
	routerRefreshCh, err := subscribeRouterRefresh(ctx, pool, log)
	if err != nil {
		return fmt.Errorf("schedd: router refresh subscribe: %w", err)
	}
	// The watcher only needs the (nodeID, target_url) pair from
	// each compute_nodes row; pkg/sched does not import
	// pkg/state today, so the lookup lives here and the watcher
	// takes a RouterRefreshFunc closure. ErrNotFound means the row
	// was soft-deleted between NOTIFY and the SELECT — write "" so
	// the next resolveFor returns ErrCapacity (the same path an
	// unknown node takes).
	routerRefreshFn := func(rctx context.Context, nodeID string) error {
		row, err := store.ComputeNodeByID(rctx, nodeID)
		switch {
		case err == nil:
			vmmRouter.Refresh(nodeID, row.TargetURL)
			return nil
		case errors.Is(err, state.ErrNotFound):
			vmmRouter.Refresh(nodeID, "")
			return nil
		default:
			return err
		}
	}
	go func() {
		sched.RunRouterRefreshWatcher(ctx, log, routerRefreshCh, routerRefreshFn)
	}()

	ledger := sched.NewNodeLedger()
	ops := wire.NewOpsMetrics("schedd")
	// Dashboard gauges (spec §12): schedd owns the snapshots table and the
	// admission ledger, so the four fcvm_* gauges live here, not in vmmd.
	// The DashboardMetrics callbacks close over `store` (PG) and `ledger`
	// (in-memory resident accounting). The lv-fc percentage shells out to
	// `lvs`; on dev boxes where lvs is missing, the closure returns 0 and
	// the gauge degrades to "no data" (no error, no spike).
	dashGauges := fcvm.NewDashboardGauges(fcvm.DashboardMetrics{
		ListSnapshotStats: func(ctx context.Context) ([]fcvm.SnapshotStat, error) {
			rows, err := store.ListLiveSnapshotStats(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]fcvm.SnapshotStat, len(rows))
			for i, r := range rows {
				out[i] = fcvm.SnapshotStat{MemBytes: r.MemBytes, DiskBytes: r.DiskBytes}
			}
			return out, nil
		},
		ResidentBytes: func(_ context.Context) (int64, error) {
			return int64(ledger.ResidentRAM()) * 1024 * 1024, nil
		},
		LvFcUsedPct: fcvm.DefaultLvFcUsedPct(api.LvFcName),
	})
	engine, err := sched.NewEngine(ctx, store, ledger, vmmRouter, sched.PoolNotifier{Pool: pool}, fcVersion, log)
	if err != nil {
		// A bootstrap failure caused by a cancelled ctx is the
		// normal "test cancelled runWithDeps before startup
		// completed" path; not an error worth reporting. Anything
		// else (missing migration 00024, dropped Postgres
		// connection, etc.) is the loud failure F-2 added.
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("schedd: init engine: %w", err)
	}
	engine.WithOpsMetrics(ops)

	// ADR-053 — slice-3 signature verification. Construct the
	// in-memory (key_id → *ecdsa.PublicKey) registry, load the
	// initial snapshot from compute_node_keys (migration 00075),
	// then subscribe to the 'compute_node_changed' pg_notify
	// channel so a vmmd registering (or rotating) its key
	// lands on the next listener tick. The handler
	// (pkg/scheddgrpc.Server.ReportCapacity) reads
	// engine.NodeKeyRegistry() — the engine's accessor is
	// nil-safe so pre-slice-3 fixtures keep working, but
	// production always wires a non-nil registry here.
	//
	// The initial Refresh is best-effort: a transient loader
	// failure is logged at Warn and the daemon keeps running
	// with the empty registry. The first successful
	// 'compute_node_changed' notify populates the map; until
	// then the handler rejects every report with
	// ErrUnknownNodeKey, which is the safer default (silent
	// unsigned-accept is the failure mode slice-3 closes).
	keys := sched.NewNodeKeyRegistry(pgNodeKeyLoader{pool: pool}, log)
	engine.WithNodeKeyRegistry(keys)
	if n, err := keys.Refresh(ctx); err != nil {
		log.Warn("schedd: initial node key registry refresh failed; first notify will populate",
			"err", err)
	} else {
		log.Info("schedd: node key registry loaded", "keys", n)
	}
	if deps.subscribeNodeKeyChanges != nil {
		go subscribeWithReconnect(ctx, "node keys", log,
			deps.subscribeNodeKeyChanges, pool, keys.Run)
	}

	// ADR-038 / Tier 3 phase 3: load the build-attestation
	// verification pub key at startup. Defaults to
	// /etc/faas/secrets/sign-pub.pem (root:root 0444);
	// FAAS_SIGN_PUB overrides for test harnesses. Fail-loud on
	// missing/insecure perms — silent insecure boots are the
	// failure mode ADR-038 §Consequences Compatibility calls out.
	//
	// The verifier also needs the storage backend (to read the
	// layer + sig blobs during verification). schedd resolves
	// the same way imaged does — the env-driven fork
	// (FAAS_STORAGE_BACKEND) keeps a remote OCI distribution
	// backend transparent. vmmd does the storage.Get on the
	// chroot mount path; schedd reads the sig blob directly via
	// the same key the imaged signer wrote under.
	signPubPath := deps.signPubPath
	if signPubPath == "" {
		signPubPath = envOr("FAAS_SIGN_PUB", cosign.DefaultSignPubPath)
	}
	storageBackend, err := storage.BackendFromEnv()
	if err != nil {
		return fmt.Errorf("schedd: storage backend: %w", err)
	}
	verifier, err := cosign.NewLocalVerifier(signPubPath, storageBackend)
	if err != nil {
		return fmt.Errorf("schedd: load sign pub %q: %w (run `faas sign-keys init` on imaged's host if missing)", signPubPath, err)
	}
	log.Info("schedd: build attestation verifier ready", "pub", signPubPath)
	engine.WithVerifier(verifier)
	// ADR-051 PR-D review finding #6: app.characterized audit row
	// emission on the cold-boot wake path. Shares the same
	// `audit.New(store, log, ops, "schedd")` instance Loop.WithAudit
	// uses — the Auditor is stateless and idempotent across callers
	// (pkg/audit/audit.go). Best-effort per ADR-035: never blocks
	// the RUNNING transition.
	engine.WithAudit(audit.New(store, log, ops, "schedd"))

	// issue #517 / PR-C / ADR-064 — wire the wake-timeline fan-out
	// (pkg/events.Platform) on the engine. Schedd is the canonical
	// emit site for wake.queue_accepted / wake.admitted /
	// wake.boot_started / wake.boot_completed / wake.boot_failed /
	// wake.park_started / wake.park_completed / wake.stalled
	// (vmmd / gatewayd / builderd / apid mirror corroborating
	// observations). nil broadcaster is allowed — the Platform
	// skips the in-process SSE fan-out and just writes the events
	// row. Production SSE delivery for the /v1/apps/{slug}/wakes/
	// {wake_id}/timeline endpoint uses pg_notify (cross-process),
	// not the in-process Broadcaster.
	engine.WithEvents(events.NewPlatform("schedd", store, log, ops, nil))

	// Rebuild admission accounting from any instances still live from a prior
	// run before we start admitting new wakes.
	if err := engine.SeedLedger(ctx); err != nil {
		log.Warn("seed ledger", "err", err)
	}

	// gRPC surface for gatewayd (ADR-018): unix socket by default;
	// tcp requires the tls_* cluster and is issue #95.
	serverTLS, err := cfg.LoadServerTLSWithVerifier(nodeVerifier)
	if err != nil {
		return fmt.Errorf("schedd: load server TLS: %w", err)
	}
	lis, err := deps.listen(ctx, listenTarget, serverTLS, cfg.OwnerUser)
	if err != nil {
		return fmt.Errorf("schedd: listen %s: %w", listenTarget, err)
	}
	gsrv := grpc.NewServer(append(
		wire.ServerCredsOrEmpty(serverTLS),
		wire.TraceServerOptions()...,
	)...)
	// scheddgrpc.New(gsrv) is called after the instancestats.Reader
	// is constructed below, so the server can serve
	// ListInstanceStats (issue #279 / PR-B). A no-stats server
	// (the legacy path) would return an empty list — the meterd
	// sampler degrades to "no CPU data" without restarting.
	// See scheddgrpc.NewWithStats.

	var httpSrv *http.Server
	if cfg.MetricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle(metricsPath, ops.Handler())
		// Mount the §12 dashboard gauges on a sibling path so a
		// `curl /metrics` scrape returns the canonical schedd ops
		// series; Prometheus hits both paths.
		mux.Handle(metricsPath+"/fcvm", dashGauges.Handler())
		httpSrv = &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("metrics http", "err", err)
			}
		}()
		log.Info("metrics listening", "addr", cfg.MetricsAddr)
	}

	// The gRPC server is registered further down (after the
	// instancestats.Reader is constructed — issue #279 / PR-B). The
	// actual Serve(lis) call must run AFTER Register has been
	// invoked, otherwise grpc.NewServer fatals with
	// "Server.RegisterService after Server.Serve". We therefore
	// defer the Serve goroutine to just after the Register call
	// further below. Tests that look for "grpc listening" still
	// find the message — it's emitted from that goroutine.

	log.Info("schedd ready",
		"ram_ceiling_mb", api.RAMAdmissionCeilingMB,
		"eviction_threshold_mb", sched.EvictionThresholdMB,
		"vcpu_slots", api.VCPUSlots,
		"fc_version", fcVersion)

	// ADR-026 deletion subscriber. Long-lived goroutine under the
	// same ctx as loop.Run. The subscriber is purely a drain (PR #83
	// review #6 collapsed the SubFn reconnect path); the production
	// schedule is "Subscribe once at startup, dial again on transient
	// errors". Linear 1s → 30s backoff lives here in cmd/schedd, not
	// inside pkg/sched, so the package stays a thin adapter. nil seam
	// = skip in tests that don't want a fake channel.
	if deps.subscribeDeletion != nil {
		sub := sched.NewDeletionSubscriber(engine, log)
		go subscribeWithReconnect(ctx, "deletion", log, deps.subscribeDeletion, pool, sub.Run)
	}

	// tier-2 PR-B (ADR-031 + ADR-033): egress drift subscriber.
	// Same dial-loop shape as the deletion subscriber above
	// (see subscribeWithReconnect for the shared backoff shape).
	// The production channel is NotifyAppChanged; the subscriber
	// filters to kind="updated" internally. nil seam = skip in
	// tests that don't want a fake channel.
	if deps.subscribeEgressDrift != nil {
		driftSub := sched.NewEgressDriftSubscriber(engine, vmmRouter, log)
		go subscribeWithReconnect(ctx, "egress drift", log, deps.subscribeEgressDrift, pool, driftSub.Run)
	}

	// Phase 2 / Gate A (migration 00084): placement claim
	// subscriber. The post-00084 schema lets apid insert apps
	// with node_id = NULL; schedd races to stamp the owner on
	// kind="created" via Engine.ClaimUnplaced. The
	// EgressDriftSubscriber above shares the same channel but
	// filters to kind="updated"; both run side-by-side. nil seam
	// = skip in tests that don't want a fake channel; the
	// cold-start sweep below still runs.
	if deps.subscribePlacementClaim != nil {
		claimSub := sched.NewPlacementClaimSubscriber(engine, log)
		go subscribeWithReconnect(ctx, "placement claim", log, deps.subscribePlacementClaim, pool, claimSub.Run)
	}

	// Tier A4 / ADR-064: rebalancer tunables. Read once at
	// startup; panic on a malformed env (operator typo must
	// surface at boot, not as silent api.* defaults that
	// mask the typo at the next drain). Schedd main is the
	// only place an operator-facing env override lives;
	// every other limit stays a pkg/api/limits.go
	// constant (CLAUDE.md hard limits policy). This block
	// runs BEFORE the rebalancer goroutine + cold-start
	// sweep below so an early drain event observes the
	// tuned values, not the api.* defaults.
	if v := os.Getenv("FAAS_REBALANCE_COOLDOWN_SECONDS"); v != "" {
		n, parseErr := strconv.Atoi(v)
		if parseErr != nil || n <= 0 {
			log.Error("FAAS_REBALANCE_COOLDOWN_SECONDS must be a positive integer",
				"value", v)
			return fmt.Errorf("FAAS_REBALANCE_COOLDOWN_SECONDS: %s", v)
		}
		if v2 := os.Getenv("FAAS_REBALANCE_MAX_PER_TICK"); v2 != "" {
			m, parseErr2 := strconv.Atoi(v2)
			if parseErr2 != nil || m <= 0 {
				return fmt.Errorf("FAAS_REBALANCE_MAX_PER_TICK: %s", v2)
			}
			engine.WithRebalanceConfig(n, m)
		} else {
			engine.WithRebalanceConfig(n, api.RebalanceMaxPerTickPerNode)
		}
	} else if v := os.Getenv("FAAS_REBALANCE_MAX_PER_TICK"); v != "" {
		m, parseErr := strconv.Atoi(v)
		if parseErr != nil || m <= 0 {
			return fmt.Errorf("FAAS_REBALANCE_MAX_PER_TICK: %s", v)
		}
		engine.WithRebalanceConfig(api.RebalanceCooldownSeconds, m)
	}

	// Tier A5 / ADR-066: live-instance migration cap. Same
	// rationale as the A4 rebalance block above — the
	// per-engine override exists so an operator can tune
	// without restarting schedd; a bad env panics via
	// WithMigrateLiveConfig so a typo doesn't silently fall
	// back to the api.* default.
	if v := os.Getenv("FAAS_MIGRATE_LIVE_MAX_PER_TICK"); v != "" {
		n, parseErr := strconv.Atoi(v)
		if parseErr != nil || n <= 0 {
			log.Error("FAAS_MIGRATE_LIVE_MAX_PER_TICK must be a positive integer",
				"value", v)
			return fmt.Errorf("FAAS_MIGRATE_LIVE_MAX_PER_TICK: %s", v)
		}
		engine.WithMigrateLiveConfig(n)
	}

	// Tier A5 / ADR-066: live-instance migration lease
	// window. Same env-override rationale as the
	// MAX_PER_TICK above. The default lives in
	// pkg/api/limits.go (MigrateLiveLeaseSeconds); an operator
	// tunes this on the OCIRegistry backend's snapshot-pull
	// latency. A bad env returns a typed error so the daemon
	// fails fast at boot rather than silently falling back.
	if v := os.Getenv("FAAS_MIGRATE_LIVE_LEASE_SECONDS"); v != "" {
		n, parseErr := strconv.Atoi(v)
		if parseErr != nil || n <= 0 {
			log.Error("FAAS_MIGRATE_LIVE_LEASE_SECONDS must be a positive integer",
				"value", v)
			return fmt.Errorf("FAAS_MIGRATE_LIVE_LEASE_SECONDS: %s", v)
		}
		engine.WithMigrateLiveLeaseSeconds(n)
	}

	// Tier A6 / ADR-067: migrating-instance watchdog. Self-heal
	// stuck state='migrating' rows that never committed (the new
	// owner vmmd died mid-handoff, etc.). The watchdog is the
	// only writer that can move a row out of 'migrating' without
	// a peer commit. Same env-override rationale as the A5
	// blocks above — explicit fail-fast on bad input.
	if v := os.Getenv("FAAS_MIGRATING_WATCHDOG_TICK_LIMIT"); v != "" {
		n, parseErr := strconv.Atoi(v)
		if parseErr != nil || n <= 0 {
			log.Error("FAAS_MIGRATING_WATCHDOG_TICK_LIMIT must be a positive integer",
				"value", v)
			return fmt.Errorf("FAAS_MIGRATING_WATCHDOG_TICK_LIMIT: %s", v)
		}
		engine.WithMigratingWatchdogTickLimit(n)
	}
	if v := os.Getenv("FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS"); v != "" {
		n, parseErr := strconv.Atoi(v)
		if parseErr != nil || n <= 0 {
			log.Error("FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS must be a positive integer",
				"value", v)
			return fmt.Errorf("FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS: %s", v)
		}
		engine.WithMigratingWatchdogIntervalSeconds(n)
	}

	// Tier A4 / ADR-064: rebalancer subscriber. Watches
	// compute_node_changed for active=false events and hands
	// the dead node id to Engine.RebalanceOrphanedApps.
	if deps.subscribeRebalancer != nil && ownerNodeID != "" {
		reb := sched.NewRebalancer(
			func(ctx context.Context, deadNodeID string) error {
				return engine.RebalanceOrphanedApps(ctx, deadNodeID)
			},
			log,
		)
		go subscribeWithReconnect(ctx, "rebalancer", log, deps.subscribeRebalancer, pool, reb.Run)
	}

	// Tier A5 / ADR-066: live-instance migration subscriber.
	// Same channel + filter as the rebalancer, but the per-
	// instance path is the four-phase handoff (Tier A5). The
	// parked-app rebalancer (Tier A4) handles apps in
	// 'parked'/'stopped' state; this one handles instances in
	// {WAKING, COLD_BOOTING, RUNNING, SNAPSHOTTING}. The two
	// must remain distinct watchers — they have different
	// retry loops, different metric labels, and different
	// per-tick caps.
	if deps.subscribeLiveMigrator != nil && ownerNodeID != "" {
		lvm := sched.NewLiveMigrator(
			func(ctx context.Context, deadNodeID string) (int, error) {
				return engine.MigrateLiveInstances(ctx, deadNodeID)
			},
			log,
		)
		go subscribeWithReconnect(ctx, "live_migrator", log, deps.subscribeLiveMigrator, pool, lvm.Run)
	}

	// Cold-start sweep: pg_notify is fire-and-forget, so a schedd
	// that was down while an apid createApp landed missed the
	// kind="created" notify. ListUnplacedApps at boot closes that
	// gap (one tx per unplaced app, sub-second in steady state).
	// Runs once, not per tick — the subscriber handles the live
	// path. Errors are logged and dropped; the next notify (or the
	// next schedd restart) is the next opportunity.
	go func() {
		apps, err := store.ListUnplacedApps(ctx)
		if err != nil {
			log.Warn("schedd: cold-start sweep: list unplaced apps", "err", err)
			return
		}
		for _, a := range apps {
			if ctx.Err() != nil {
				return
			}
			if err := engine.ClaimUnplaced(ctx, a.ID); err != nil {
				log.Warn("schedd: cold-start sweep: claim", "app_id", a.ID, "err", err)
				continue
			}
		}
		if len(apps) > 0 {
			log.Info("schedd: cold-start sweep: reconciled unplaced apps", "count", len(apps))
		}
	}()

	// Tier A4 / ADR-064: rebalance cold-start sweep. Same
	// fire-and-forget-notify reasoning as the unplaced
	// sweep above — a schedd that was down while a drain
	// event landed missed the compute_node_changed active=
	// false notify. RebalanceOrphanedApps with
	// deadNodeID="" reconciles every orphaned app
	// regardless of which dead node owned it. Runs once.
	// Errors are logged and dropped; the next notify (or
	// the next schedd restart) is the next opportunity.
	if ownerNodeID != "" {
		go func() {
			if err := engine.RebalanceOrphanedApps(ctx, ""); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Warn("schedd: cold-start sweep: rebalance orphans", "err", err)
			}
		}()
	}

	// Tier A5 / ADR-066: live-instance migration cold-start
	// sweep. Same fire-and-forget-notify reasoning as the
	// rebalance cold-start sweep above — a schedd that was
	// down while a drain event landed missed the
	// compute_node_changed active=false notify, so any live
	// instance still owned by an inactive compute_node would
	// be pinned until the next notify. MigrateLiveInstances
	// with deadNodeID="" reconciles every dead-node-owned
	// live instance regardless of which dead node. Runs once.
	if ownerNodeID != "" {
		go func() {
			attempted, err := engine.MigrateLiveInstances(ctx, "")
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Warn("schedd: cold-start sweep: live migrate", "err", err)
				return
			}
			if attempted > 0 {
				log.Info("schedd: cold-start sweep: live migrate reconciled", "attempted", attempted)
			}
		}()
	}

	// PR #114 / ADR-025 axis 3: per-node liveness sweep. Every
	// `HeartbeatInterval` (default 30s) the heartbeat goroutine
	// dials a fresh *VMMClient per active node via deps.dialVMM
	// (issue #120), calls Ping, then Close — bypassing the
	// VMMRouter cache so every heartbeat pays the dial cost and
	// sees a fresh transport. deps.dialVMM already routes through
	// sched.DialVMMContext → pkg/overlay (issue #120), so the
	// heartbeat dial shares the same cross-box dial primitive as
	// the engine without an extra adapter. On success we stamp
	// last_heartbeat_at, on failure we flip active=false so
	// placement skips the dead node and the alertmanager rule
	// (PR #115) fires. Production cadence is overridable via
	// FAAS_HEARTBEAT_INTERVAL; tests inject a sub-second interval
	// through runDeps.heartbeatInterval to exercise the wiring.
	hb := sched.NewHeartbeat(store, sched.HeartbeatDialerFunc(deps.dialVMM), vmmTLS, log).
		WithOwnerNodeID(ownerNodeID)
	hb.Interval = cfg.HeartbeatInterval
	hb.Staleness = cfg.HeartbeatStaleness
	if deps.heartbeatInterval > 0 {
		// Tests inject a sub-second cadence via runDeps to exercise
		// the wiring without waiting 30s for production cadence.
		hb.Interval = deps.heartbeatInterval
	}
	// PR-A observability slice (issue #170): per-{app,node} instance
	// stats poller. Builds a Reader (the canonical seam #171 reaper
	// and #169 scale-up will read from), wires the Poller with the
	// same deps.dialVMM the heartbeat uses (so dial churn is bounded
	// by the dialer — same pattern PR #120 established), and
	// attaches it via WithInstanceStats. The Reader is intentionally
	// NOT threaded to the reaper or any policy consumer today —
	// that's #171 / #169's job. PR-A keeps the reader as the only
	// public surface.
	reader := instancestats.NewReader()
	statsPoller := instancestats.NewPoller(
		store,
		instancestats.DialerFunc(deps.dialVMM),
		vmmTLS,
		reader,
		ops,
		log,
	)
	// Register the gRPC server with the Reader wired so the
	// ListInstanceStats RPC (issue #279 / PR-B) can serve the
	// per-instance CPU-µs snapshot to meterd. The reader is
	// populated by the poller above (200 ms cadence); a meterd
	// call before the first tick returns an empty list.
	scheddgrpc.NewWithStats(engine, reader, ops, log).
		WithOwner(scheddgrpc.OwnerNodeID(ownerNodeID), store).
		Register(gsrv)

	// Serve goroutine — must run AFTER Register or grpc fatals.
	serveErr := make(chan error, 1)
	go func() {
		log.Info("grpc listening", "addr", listenTarget, "service", scheddpb.Schedd_ServiceDesc.ServiceName)
		serveErr <- gsrv.Serve(lis)
	}()

	// Issue #169 / #172: per-app reactive scale-up trigger.
	// Reads apps.autoscale_target_* + Ledger.Concurrency every
	// cfg.ScaleUpInterval (default 1s); admits another instance
	// when measured per-instance RPS or CPU exceeds the target
	// and headroom is available. The trigger is nil-safe on every
	// dep; an empty GatewayMetricsURL disables the RPS path (and
	// the trigger still fires on CPU when PR #205's
	// instancestats.Reader is wired). The engine adapter converts
	// sched.WakeResult → scaleup.AdmitResult (a small subset —
	// the trigger only inspects AtCapacity).
	loop := sched.NewLoop(pool, engine, log).
		WithFlowCounter(flowcount.NewReader(wire.ExecRunner{})).
		WithWatchdog(sched.NewWatchdog(store, engine, log)).
		// PR #74: §17 retention sweep — DELETEs STOPPED/FAILED rows older
		// than cfg.RetentionDuration (defaults to api.DefaultInstanceRetention
		// when zero). Ticker fires at api.DefaultRetentionInterval (1h).
		WithRetention(sched.NewRetention(store, log).WithRetention(time.Duration(cfg.RetentionDuration))).
		WithHeartbeat(hb).
		WithInstanceStats(statsPoller).
		// Issue #171: shared Prometheus registry (same instance the
		// engine got) — needed by the aggressive-reaper scale-down
		// counter (ObserveScaleDown) and the audit-row emission.
		WithOpsMetrics(ops).
		// PR scale-out readiness #3: read-only /srv/fc/snap vs DB drift
		// sweep. Hourly cadence (api.DefaultDiskDriftInterval = 1h).
		// Diagnostic only — never writes, never follows symlinks,
		// never repairs. Ops reads rate(snapshot_disk_drift_total[5m])
		// and alerts on a non-zero rate. Shares the OpsMetrics
		// receiver the engine + retention + watchdog already use.
		WithDiskDrift(sched.NewDiskDrift(store, log).WithMetrics(ops).
			// ADR-054 §3: enumerate the snap/ prefix via the
			// production storage backend instead of os.ReadDir on
			// /srv/fc/snap. The byte-comparison path stays in
			// place for the local backend; a remote backend (OCI)
			// degrades to a presence check.
			WithStorage(storageBackend)).
		// Audit seam (lifted from cmd/apid/audit.go into pkg/audit
		// for cross-daemon reuse). schedd uses actor="schedd" so the
		// cron-fire path can emit a `cron.fired` events row after
		// MarkCronFired. Best-effort failure semantics from
		// pkg/audit/audit.go — never rolls back the fire.
		WithAudit(audit.New(store, log, ops, "schedd")).
		// Issue #171: aggressive reaper toggle + per-tick park cap.
		// cfg.ReaperAggressive defaults ON; FAAS_REAPER_AGGRESSIVE=false
		// disables in-place. cfg.ReaperAggressiveParkCap=0 → default
		// (sched.MaxParksPerTickPerApp = 8).
		WithReaperAggressive(cfg.ReaperAggressive).
		WithReaperParkCap(cfg.ReaperAggressiveParkCap)

	// ADR-067: Tier A6 migrating-instance watchdog — self-heals
	// rows stuck in state='migrating' after a new-owner vmmd dies
	// mid-handoff. 1 s tick (api.MigratingWatchdogIntervalSeconds);
	// per-tick cap = cfg.MigratingWatchdogTickLimit (default
	// api.MigratingWatchdogTickLimit = 50). Active owners are
	// reinvited (state→running); dead owners are parked
	// (state→parked, node_id→migrated_from_node_id).
	//
	// cfg.*=0 means "use the api.* default" — cmd/schedd fills
	// them in here so an unset TOML and an unset env var both
	// resolve to the spec defaults (mirrors the existing
	// ReaperAggressive / ReaperAggressiveParkCap zero-handling).
	mwdInterval := cfg.MigratingWatchdogIntervalSeconds
	if mwdInterval <= 0 {
		mwdInterval = api.MigratingWatchdogIntervalSeconds
	}
	mwdTickLimit := cfg.MigratingWatchdogTickLimit
	if mwdTickLimit <= 0 {
		mwdTickLimit = api.MigratingWatchdogTickLimit
	}
	engine.WithMigratingWatchdogTickLimit(mwdTickLimit)
	loop.WithMigratingWatchdog(sched.NewMigratingWatchdog(
		engine.ReconcileExpiredMigrations,
		time.Duration(mwdInterval)*time.Second,
		log))
	if cfg.GatewayMetricsURL != "" {
		// Issue #171: share a single HTTPPromScraper between the
		// scale-up trigger and the aggressive-reaper signal mirror.
		// The scraper surface is stateless (one Scrape call → one
		// HTTP GET + parse); two callers are safe and a shared
		// connection keeps gatewayd's listener traffic to ~1
		// req/sec instead of ~2.
		var scraper scaleup.PromScraper = scaleup.NewHTTPPromScraper(cfg.GatewayMetricsURL)
		trigger := scaleup.New(
			// PR-C (issue #462): thread the *instancestats.Reader
			// into the scale-up trigger so InstatsReader.MaxCPU
			// is callable. The reader was nil before PR-B / PR-C;
			// PR-B added the accessor; PR-C wires the dependency
			// so the trigger can consult live CPU values alongside
			// the RPS scrape. MaxInflightForApp is exposed by the
			// same interface but the scaleup trigger itself does
			// not read it (the concurrent_requests axis lives in
			// pkg/sched/targets — see loop.WithTargets below).
			store, reader, scraper,
			schedScaleUpEngine{engine: engine},
			engine.Ledger(),
			scaleup.Options{
				Logger:   log,
				Metrics:  ops,
				Interval: cfg.ScaleUpInterval,
			},
		)
		loop.WithScaleUp(trigger)
		// PR-C (issue #462): concurrent_requests target scale-up
		// trigger. Reads InstatsReader.MaxInflightForApp and
		// compares against ScalingPolicy.Target.Value per app.
		// Distinct from the RPS/CPU scale-up trigger (above) which
		// is unchanged. The instats reader is the same one already
		// constructed for the scaleup trigger; both triggers share
		// the reader (it's read-only). nil reader ⇒ targets
		// construction is skipped (test wiring without Instats).
		if reader != nil {
			targetsTrigger := targets.New(
				store, reader,
				schedTargetsEngine{engine: engine},
				schedTargetsLedger{ledger: engine.Ledger()},
				targets.Options{
					Logger:   log,
					Metrics:  ops,
					Interval: cfg.ScaleUpInterval,
				},
			)
			loop.WithTargets(targetsTrigger)
			log.Info("concurrent_requests target trigger enabled",
				"interval", cfg.ScaleUpInterval)
		}
		// Issue #171: wire the recent-load mirror off the same
		// scraper so the reaper sees per-app RPS without duplicating
		// the scraping wiring. nil scraper ⇒ mirror is a no-op
		// (recentload.New handles nil); the loop's WithRecentLoad
		// nil-check keeps the ticker + runReaper block disabled.
		mirror := recentload.New(scraper, api.ScaleUpWindowSeconds, time.Second)
		loop.WithRecentLoad(mirror)
		log.Info("autoscale signal mirror enabled",
			"metrics_url", cfg.GatewayMetricsURL,
			"window_s", api.ScaleUpWindowSeconds,
			"aggressive", cfg.ReaperAggressive)
	}
	// Cron dispatch path: route synthetic requests through gatewayd's
	// internal listener so metering + rate limits apply identically
	// to user traffic (spec §4.4, M7). Multi-box schedd uses the
	// GatewaySynthTarget URL (placement scheduler PR, ADR-025 axis 3
	// Q8); the legacy GatewaySynthSocket field stays as a fallback
	// for one-box deploys + the e2e harness. A failure to dial is
	// logged but non-fatal — the cron loop tolerates a missing
	// gateway (Wake still runs, the synth step is best-effort).
	synthTarget := cfg.GatewaySynthTarget
	if synthTarget == "" {
		synthTarget = "unix://" + cfg.GatewaySynthSocket
	}
	if synthTarget != "" {
		synth, dialErr := sched.DialGatewaySynthTarget(synthTarget, nil, log)
		if dialErr != nil {
			log.Warn("gateway synth dial: cron traffic will not flow until gatewayd is up",
				"target", synthTarget, "err", dialErr)
		} else {
			loop.WithGatewaySynth(synth)
		}
	}
	loopErr := make(chan error, 1)
	go func() { loopErr <- loop.Run(ctx) }()

	// Move 1 drain: a second goroutine inside schedd that drains the
	// unified invocations table on a 1s safety tick + invocation_due
	// pg_notify channel. Shares the engine + store with the cron
	// loop; the synth client is the same one the cron loop uses so
	// the wake path is one consistent admission gate.
	if synthTarget != "" {
		synth, dialErr := sched.DialGatewaySynthTarget(synthTarget, nil, log)
		if dialErr != nil {
			// A failed dial disables the entire drain — async /
			// queue / delayed-task rows would still arrive via the
			// 1s safety ticker (no notify) but every dispatch
			// would 502. Surface loud so the operator notices
			// before customers start timing out.
			log.Error("drain: synth dial failed; event-shaped dispatch is disabled",
				"target", synthTarget, "err", dialErr)
		} else {
			drain := sched.NewDrain(engine.Store(), engine,
				sched.WithDrainGatewaySynth(synth),
				sched.WithDrainNotifier(engine.Notifier()),
				sched.WithDrainLogger(log))
			notifC, subErr := db.SubscribeWithReconnect(ctx, pool,
				[]string{db.NotifyInvocationDue}, log)
			if subErr != nil {
				log.Error("drain: subscribe invocation_due failed; safety ticker still runs",
					"err", subErr)
			} else {
				go func() {
					if err := drain.Run(ctx, notifC); err != nil && !errors.Is(err, context.Canceled) {
						log.Warn("drain", "err", err)
					}
				}()
			}
		}
	}

	select {
	case <-ctx.Done():
		log.Info("draining")
	case err := <-serveErr:
		if err != nil {
			return err
		}
	case err := <-loopErr:
		if err != nil {
			return err
		}
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gsrv.GracefulStop()
	if httpSrv != nil {
		//nolint:contextcheck // shutdown context is intentionally detached from the already-cancelled caller ctx.
		_ = httpSrv.Shutdown(stopCtx)
	}
	_ = lis.Close()
	return nil
}

// subscribeWithReconnect drains a pg_notify-style feed via the
// Shared subscriber Drain loop. Shared by the deletion
// subscriber (ADR-026) and the egress drift subscriber (PR-B).
//
// contract: ctx is the long-lived daemon context; subscribe is
// the producer-side seam (already opened channel + cancel +
// error); run is the subscriber's drain (run blocks until the
// channel closes or ctx fires).
//
// Backoff schedule: linear 1s → 30s on dial failure or drain
// exit. Reset to 1s after a successful drain that exited
// cleanly (channel-closed from the producer side, which
// db.Subscribe uses as a reconnect signal).
func subscribeWithReconnect(
	ctx context.Context,
	name string,
	log *slog.Logger,
	subscribe func(context.Context, *pgxpool.Pool) (<-chan db.Notification, func(), error),
	pool *pgxpool.Pool,
	run func(context.Context, <-chan db.Notification) error,
) {
	delay := 1 * time.Second
	const maxDelay = 30 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		feed, cancel, err := subscribe(ctx, pool)
		if err != nil {
			log.Warn("schedd: "+name+" subscriber dial failed",
				"err", err, "retry_in", delay.String())
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			if delay < maxDelay {
				delay *= 2
				if delay > maxDelay {
					delay = maxDelay
				}
			}
			continue
		}
		// Dial succeeded — run the drain on this channel
		// until it closes (a reconnect signal from
		// db.Subscribe) or ctx fires.
		err = run(ctx, feed)
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Warn("schedd: "+name+" subscriber exited; retrying dial",
				"err", err, "retry_in", delay.String())
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
		if ctx.Err() != nil {
			return
		}
		// Reset backoff after a successful drain that we
		// voluntarily tore down (rare in practice, but
		// keeps the curve sane after a partial outage).
		if err == nil || errors.Is(err, context.Canceled) {
			delay = 1 * time.Second
		}
	}
}

// schedScaleUpEngine adapts *sched.Engine to the scaleup.Engine
// interface (issue #169 / #172). The trigger only needs the
// AtCapacity flag and the InstanceID echo — the rest of
// WakeResult's surface (Method, WakeID, NodeID) is internal to
// the gateway's wake path. The adapter is a thin closure stored
// as a value so the trigger never pays an indirection cost on the
// hot path.
type schedScaleUpEngine struct {
	engine *sched.Engine
}

// AdmitInstance implements scaleup.Engine: delegates to the wrapped
// engine and lifts the relevant fields into the thinned
// scaleup.AdmitResult.
func (s schedScaleUpEngine) AdmitInstance(ctx context.Context, appID string) (scaleup.AdmitResult, error) {
	r, err := s.engine.AdmitInstance(ctx, appID)
	if err != nil {
		return scaleup.AdmitResult{}, err
	}
	return scaleup.AdmitResult{InstanceID: r.InstanceID, AtCapacity: r.AtCapacity}, nil
}

// schedTargetsEngine (PR-C, issue #462) adapts *sched.Engine to
// the targets.Engine interface. Mirrors schedScaleUpEngine above
// but lifts into the thinned targets.AdmitResult shape (which only
// carries the InstanceID echo — the targets trigger never reads
// AtCapacity itself; the engine's internal admitGate handles the
// cap rejection).
type schedTargetsEngine struct {
	engine *sched.Engine
}

// AdmitInstance implements targets.Engine: delegates to the wrapped
// engine and lifts InstanceID + AtCapacity into targets.AdmitResult.
// AtCapacity MUST be forwarded — pkg/sched/targets.Trigger.Tick
// consults result.AtCapacity on the admit path and re-observes
// the wake-gate outcome metric as reject_at_cap when the engine
// itself refused the admit (the race between decide()'s cap check
// and engine.AdmitInstance's ledger call). Without forwarding,
// the re-observe branch is dead code and the metric loses the
// per-tick AtCapacity signal that the dashboard's "would have
// scaled but cap reached" pane depends on.
func (s schedTargetsEngine) AdmitInstance(ctx context.Context, appID string) (targets.AdmitResult, error) {
	r, err := s.engine.AdmitInstance(ctx, appID)
	if err != nil {
		return targets.AdmitResult{}, err
	}
	return targets.AdmitResult{InstanceID: r.InstanceID, AtCapacity: r.AtCapacity}, nil
}

// schedTargetsLedger (PR-C, issue #462) adapts *sched.NodeLedger
// to the targets.Ledger interface. Read-only — the trigger never
// mutates the ledger; AdmitInstance inside engine.admitGate is the
// sole mutation path.
type schedTargetsLedger struct {
	ledger *sched.NodeLedger
}

// Concurrency implements targets.Ledger: delegates to the wrapped
// ledger's Concurrency accessor (pkg/sched/admission.go).
func (s schedTargetsLedger) Concurrency(appID string) int {
	return s.ledger.Concurrency(appID)
}
