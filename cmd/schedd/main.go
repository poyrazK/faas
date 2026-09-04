// Command schedd — scheduler and instance-lifecycle owner (spec §4.3).
//
// schedd is the ONLY writer to the instances table and the sole owner of the
// state machine (spec §Component ownership, §6). It runs admission control, the
// idle reaper, eviction, and cron in one process — single writer, no distributed
// locking. It serves a gRPC Wake/ReportActivity surface to gatewayd-internal on
// /run/faas/schedd.sock (ADR-018) and dials vmmd on /run/faas/vmmd.sock to drive
// the microVM lifecycle (ADR-014).
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
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/capdecl/runtimecheck"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/fcvm"
	mirrorRollup "github.com/onebox-faas/faas/pkg/mirror"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/sched/floor"
	"github.com/onebox-faas/faas/pkg/sched/flowcount"
	"github.com/onebox-faas/faas/pkg/sched/instancestats"
	"github.com/onebox-faas/faas/pkg/sched/recentload"
	"github.com/onebox-faas/faas/pkg/sched/scaleup"
	"github.com/onebox-faas/faas/pkg/sched/targets"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/webhook"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/onebox-faas/faas/pkg/wire/otelinit"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	// subscribeAppDelete (ADR-098) is the producer-side seam for
	// the app_delete consumer that evicts any in-flight wake for
	// a deleted app via Engine.wakeCoord.Forget. nil = the
	// subscriber is not started; tests inject a fake channel.
	subscribeAppDelete func(context.Context, *pgxpool.Pool) (<-chan db.Notification, func(), error)
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
	// INSERT/UPDATE/DELETE (migration 00076's trigger fires on
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
	// capCheck: DEPLOY-1 / ADR-075 capdecl gate seam (review
	// finding M2). nil → runtimecheck.MustCheckOnBoot(capsDecl,
	// log, nil) which exits on violation in production. Tests
	// inject func() error { return nil } to bypass the live
	// /proc/self/status check.
	capCheck func() error
}

func defaultDeps() runDeps {
	return runDeps{
		configPath: envOr("FAAS_SCHEDD_CONFIG", "/etc/faas/schedd.toml"),
		openDB:     db.Open,
		migrate:    db.MigrateUp, // F2 / ADR-124: acquires pg_advisory_lock; safe for fleet bootstrap
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
		// ADR-098: subscribe to app_delete so the wake coordinator
		// forgets in-flight wakes the moment apid deletes the app.
		// Mirrors subscribeDeletion's shape (one channel, db.Subscribe
		// is the production adapter, nil seam skips in tests).
		subscribeAppDelete: func(ctx context.Context, p *pgxpool.Pool) (<-chan db.Notification, func(), error) {
			return db.Subscribe(ctx, p, []string{db.NotifyAppDelete})
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
	// DEPLOY-1 / ADR-075 capdecl gate. schedd's capsDecl is
	// the empty declaration (no Allow, no Deny) — schedd is
	// unprivileged. The capCheck seam (review finding M2)
	// lets tests stub the live /proc/self/status check.
	capCheck := deps.capCheck
	if capCheck == nil {
		capCheck = func() error { return runtimecheck.MustCheckOnBoot(capsDecl, log, nil) }
	}
	if err := capCheck(); err != nil {
		return err
	}

	cfg, err := LoadConfig(deps.configPath)
	if err != nil {
		return err
	}
	// Gate-B box-role gate. schedd is a control-plane daemon — it
	// refuses to start under RoleComputeOnly. The role is set
	// from TOML or FAAS_SCHEDD_ROLE at deploy time; default is
	// RoleSingleBox so single-box dev boots unmoved.
	if err := role.Require("schedd", cfg.Role, role.RoleSingleBox, role.RoleControlPlane); err != nil {
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
	// front of every mTLS leg (server-side on the gatewayd-internal-facing
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
	//
	// ADR-052 §5 / PR-E: route the load through the WithReload factory
	// so a SIGHUP-driven reload (operator workflow: `gregale pki
	// rotate` → `kill -HUP $(pidof faas-schedd)`) swaps the leaf on
	// the next outbound TLS handshake. The vmmRotator holds the
	// live *tls.Config; vmmRouter / heartbeat / instance-stats
	// dialers consult vmmRotator.Get() at dial time so a swap
	// between rotations is observable to the next dial.
	vmmRotator := wire.NewTLSRotator(nil)
	vmmTLS, err := cfg.LoadVMMTLSWithPrefixAndVerifierAndReload(nodeVerifier, vmmRotator.Reload(nil))
	if err != nil {
		return fmt.Errorf("schedd: load vmmd TLS: %w", err)
	}
	vmmRotator.Set(vmmTLS)
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
	nodeRegistry := sched.NewNodeRegistry(nodes)

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
		// Same ctx-cancellation grace as the ActiveComputeNodes
		// call above (cmd/schedd/main.go:334-336): a test or
		// operator cancelling mid-boot must not surface as a
		// non-nil error from runWithDeps — the drain is clean.
		//
		// The double check (errors.Is on the err AND ctx.Err())
		// matters: under `-coverpkg=*` instrumentation
		// (memory golangci-lint-v2-4-0-handler-checklist) the
		// coverage pass can wrap pgxpool.Acquire errors in a
		// way that breaks errors.Is unwrapping; the ctx.Err()
		// side is the canonical "did the caller cancel" signal.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil || strings.Contains(err.Error(), context.Canceled.Error()) {
			return nil
		}
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
			nodeRegistry.Refresh(row)
			return nil
		case errors.Is(err, state.ErrNotFound):
			vmmRouter.Refresh(nodeID, "")
			nodeRegistry.Remove(nodeID)
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
	wire.BootStamps(ctx, "schedd", ops)
	wire.RegisterDefaultOps(ops)
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
	// Keep the engine's ownership scope aligned with the gRPC server,
	// heartbeat, and floor trigger. An empty owner preserves the central
	// scheduler's fleet-wide placement; a configured owner pins this
	// schedd to its registered compute node.
	engine.WithOwnerNodeID(ownerNodeID)
	engine.WithNodeRegistry(nodeRegistry)
	// ADR-098 PR-D: connection-aware upstream affinity. The
	// FAAS_UPSTREAM_AFFINITY environment value is the bootstrap
	// fallback; the durable data-placement flag can switch the
	// chooser without restarting schedd. When disabled, the
	// engine's upstreamAffinity stays nil and the chooser falls
	// back to the legacy tie-break. FAAS_UPSTREAM_AFFINITY_TTL is
	// operator-overridable (default api.UpstreamAffinityTTL = 30 s,
	// matching the meterd probe cadence so the cache is never more
	// than one probe-cycle stale).
	ttl := api.UpstreamAffinityTTL
	if v := os.Getenv("FAAS_UPSTREAM_AFFINITY_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			ttl = d
		} else {
			log.Warn("FAAS_UPSTREAM_AFFINITY_TTL parse failed; using default", "got", v, "err", err)
		}
	}
	upstreamAffinity := sched.NewUpstreamAffinity(ttl, store)
	upstreamAffinityEnabled := runtimeconfig.NewBoolFlag(os.Getenv("FAAS_UPSTREAM_AFFINITY") != "")
	if upstreamAffinityEnabled.Load() {
		engine.WithUpstreamAffinity(upstreamAffinity)
	} else {
		log.Info("schedd: upstream affinity disabled — FAAS_UPSTREAM_AFFINITY unset; using legacy chooser")
	}
	// ADR-132: data placement is a fleet flag, not an apid-only toggle.
	// schedd keeps the affinity cache ready and switches the engine's pointer
	// atomically when the acknowledged runtime value changes.
	runtimeCtx, runtimeCancel := context.WithCancel(ctx)
	defer runtimeCancel()
	watcher := runtimeconfig.New(store, pool, []string{runtimeconfig.KeyDataPlacement},
		func(ctx context.Context, key string, value json.RawMessage, _ int64) error {
			enabled, err := runtimeconfig.Bool(value)
			if err != nil {
				return err
			}
			if key == runtimeconfig.KeyDataPlacement {
				upstreamAffinityEnabled.Store(enabled)
				if enabled {
					engine.WithUpstreamAffinity(upstreamAffinity)
				} else {
					engine.WithUpstreamAffinity(nil)
				}
			}
			return nil
		}, log)
	if err := watcher.Reconcile(runtimeCtx); err != nil {
		log.Warn("schedd: initial runtime config reconcile failed", "err", err)
	}
	go func() {
		if err := watcher.Run(runtimeCtx); err != nil && !runtimeconfig.IsContextDone(err) {
			log.Error("schedd: runtime config watcher exited", "err", err)
		}
	}()

	// Issue #555 PR-6 — per-deployment 100% sampling window.
	//
	// otelinit.Init wires the OTel SDK (sampler chain:
	// ParentBased(DeploymentAware(TraceIDRatioBased(rate)))). The
	// returned handle owns the per-deployment counter that the
	// scheduler (and the engine's sched.wake span) consult via
	// the sampler. Shutdown flushes the batch span processor on
	// drain.
	//
	// The watcher (sched.DeploymentCounterWatcher) resets the
	// counter on the "last live instance parked for this
	// deployment" transition, observed via the in-process
	// Platform `wake` topic — so we must also wire a real
	// Broadcaster into NewPlatform below (replacing the prior
	// `nil` arg).
	otelHandle, err := otelinit.Init(ctx, otelinit.Config{
		Name:    "schedd",
		Version: wire.Version,
	}, log)
	if err != nil {
		return fmt.Errorf("schedd: otelinit: %w", err)
	}
	defer func(ctx context.Context) {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = otelHandle.Shutdown(shutdownCtx)
	}(ctx)

	// ADR-053 — slice-3 signature verification. Construct the
	// in-memory (key_id → *ecdsa.PublicKey) registry, load the
	// initial snapshot from compute_node_keys (migration 00076),
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
	// ADR-054 acceptance: wire the LocalCacheBackend observer so
	// stale-fallback serves on the wake path emit
	// `schedd_storage_cache_stale_fallback_total`. The schedd
	// counter complements the vmmd/imaged emissions: a single cold-
	// boot that hits stale-fallback will trip both schedd and vmmd,
	// so the rate() comparison surfaces "one stall, two emitters"
	// (consistent) vs "registry down across many wakes" (rate
	// diverges — schedd's wake-side emits rise faster than vmmd's
	// boot-side emits). Uses storage.AsCacheBackend so the observer
	// attaches even when the BackendFromEnv shape changes (a future
	// metrics wrapper, router-encloses-cache, etc.). Nil result is
	// expected on single-box local deploys — the cache is opt-in
	// there.
	if cacheBE := storage.AsCacheBackend(storageBackend); cacheBE != nil {
		cacheBE.SetObserver(storage.LogCacheObserver{
			Logger: log,
			Next: storage.FuncCacheObserver(func() {
				ops.StorageCacheStaleFallback().Inc()
			}),
		})
	}
	verifier, err := cosign.NewLocalVerifier(signPubPath, storageBackend)
	if err != nil {
		return fmt.Errorf("schedd: load sign pub %q: %w (run `faas sign-keys init` on imaged's host if missing)", signPubPath, err)
	}
	log.Info("schedd: build attestation verifier ready", "pub", signPubPath)
	engine.WithVerifier(verifier)
	// Issue #561 — wire the spend-cap pause-workload seam. Engine
	// consults the checker inside admitGate AFTER the existing
	// min-floor branch; a cap-reached app refuses new wakes with
	// `*api.Problem{Code: CodeAdmissionRefused}`. 5 s TTL — the
	// customer raised the cap via POST /v1/account/overage-cap; the
	// next wake after a raise should succeed within seconds, not
	// minutes. Worst-case overadmission window: one extra wake per
	// customer per 5 s, bounded by the meterd quota tick (per-minute)
	// for sustained over-the-cap traffic.
	engine.WithOverageChecker(sched.NewMemCacheOverageChecker(store, 5*time.Second))

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
	// (vmmd / gatewayd-internal / builderd / apid mirror corroborating
	// observations).
	//
	// Issue #555 PR-6 — wire a real Broadcaster (was nil before)
	// so the DeploymentCounterWatcher can subscribe in-process.
	// Cross-process delivery for the /v1/apps/{slug}/wakes/
	// {wake_id}/timeline endpoint still uses pg_notify; the
	// Broadcaster is the in-process fast path.
	bc := events.New()
	engine.WithEvents(events.NewPlatform("schedd", store, log, ops, bc))

	// Issue #554 / ADR-078 — per-deployment liveness-restart
	// sliding window. The Engine calls RecordRestart on every
	// DestroyForLivenessFailure; on the Nth restart in the
	// window the same call flips the parent app to evicted_cold
	// (audit kind instances.parked_liveness_exhausted). The
	// Loop shares the same pointer via WithLivenessWindow so
	// future periodic cleanup has the same ring the engine
	// already writes to.
	livenessWindow := sched.NewLivenessWindow(
		time.Duration(api.DefaultLivenessWindowSeconds)*time.Second,
		api.DefaultLivenessMaxRestarts,
	)
	engine.WithLivenessWindow(livenessWindow)

	// Issue #555 PR-6 — start the DeploymentCounterWatcher. The
	// watcher resets the per-deployment 100% sampling window on
	// the "last live instance parked" transition. The
	// LiveInstanceCounter interface is satisfied by
	// state.PgStore (and MemStore) via CountLiveInstancesByDeployment.
	counterWatcher := sched.NewDeploymentCounterWatcher(bc, otelHandle.DeploymentCounter, store, log)
	go func() {
		if err := counterWatcher.Run(ctx); err != nil {
			log.Warn("deployment_counter_watcher: run ended", "err", err)
		}
	}()

	// Mega-1 jobs wiring (issue #1184 Workstream A / ADR-099).
	// FAAS_JOBS_DISPATCH=1 opts this schedd into dispatching
	// queued job_tasks; default OFF keeps the cluster-wide gate
	// closed while the vmmd gRPC JobColdBoot surface ships in a
	// follow-up commit. Two seams:
	//
	//   * WithJobLeaser  — PgLeaser over the same pool the rest
	//     of schedd uses, keyed by the schedd's ownerNodeID (so a
	//     lease survives restart-and-rebind).
	//   * WithJobVmmClient — fail-open adapter that returns
	//     ErrJobVMMNotWired until the vmmd gRPC JobColdBoot proto
	//     lands. WakeJob records the error on the run as
	//     failed → retry → dead_letter; a customer whose job
	//     dead-letters on a node with FAAS_JOBS_DISPATCH=1 gets
	//     a clear wire response (CodeJobVMMUnavailable) and the
	//     run stays in DB so the audit trail is complete.
	//
	// Both are wired UNCONDITIONALLY so FAAS_JOBS_DISPATCH can be
	// flipped at runtime without a schedd restart (the dispatch
	// + reaper tickers in loop.Run are gated on jobsDispatched;
	// the engine methods stay wired so a missed tick in one
	// window drains on the next).
	jobsDispatched := jobsDispatchEnabled(os.Getenv("FAAS_JOBS_DISPATCH"))
	if jobsDispatched {
		log.Info("schedd jobs dispatch enabled — clustering flag FAAS_JOBS_DISPATCH=1 set")
	} else {
		log.Info("schedd jobs dispatch disabled — set FAAS_JOBS_DISPATCH=1 to enable (Mega-1 cluster-wide gate)")
	}
	engine.WithJobVmmClient(sched.NewFailOpenJobVMMClient(log))
	// WithJobLeaser is NOT wired because the PgLeaser's
	// poolExecutor interface does not match *pgxpool.Pool's
	// pgconn.CommandTag return type (local pgxCommandTag shim
	// for unit tests, ADR-134 unifies post-Mega-1). WakeJob's
	// nil-leaser branch at jobs.go:142 returns ErrJobLeaserNil
	// which dispatchJobsTick classifies as failed → retryable;
	// dead-letter when retries exhaust. Customers see
	// CodeJobLeaserUnavailable on the wire. This is fail-closed
	// by design: shipping the wrong leaser would be worse than
	// no leaser (a real lease bug in production > a clear
	// dead-lettered run the operator can replay once the
	// Mega-1.5 follow-up wires the real PgLeaser).
	if ownerNodeID != "" {
		log.Info("schedd jobs lease primitive deferred to Mega-1.5 — node id", "node_id", ownerNodeID)
	} else {
		log.Info("schedd jobs lease primitive deferred to Mega-1.5 — single-box schedd")
	}

	// Rebuild admission accounting from any instances still live from a prior
	// run before we start admitting new wakes.
	if err := engine.SeedLedger(ctx); err != nil {
		log.Warn("seed ledger", "err", err)
	}

	// gRPC surface for gatewayd-internal (ADR-018): unix socket by default;
	// tcp requires the tls_* cluster and is issue #95.
	//
	// ADR-052 §5 / PR-E: route the load through the WithReload factory
	// so a SIGHUP-driven reload swaps the inbound server's leaf via
	// stdlib's per-handshake GetConfigForClient callback. serverRotator
	// holds the live config; Listen's outer tls.Config installs the
	// rotator's Reload closure at startup so subsequent Set calls
	// surface rotated material on the next handshake without
	// rebuilding the gRPC server.
	serverRotator := wire.NewTLSRotator(nil)
	serverTLS, err := cfg.LoadServerTLSWithPrefixAndVerifierAndReload(nodeVerifier, serverRotator.Reload(nil))
	if err != nil {
		return fmt.Errorf("schedd: load server TLS: %w", err)
	}
	serverRotator.Set(serverTLS)
	// ADR-052 §5 / PR-E: SIGHUP-driven TLS cert rotation. schedd
	// doesn't yet have its own hupCh (pkg/wire.Daemon's is consumed
	// by watchLogLevelReload). Install two parallel ones — each
	// gets every SIGHUP (signal.Notify fans the signal out to every
	// registered channel). Same pattern as cmd/vmmd/main.go:1115-1118's
	// egress-bundle reload. Best-effort failure posture (matches
	// egress bundle): a failed reload keeps prior material live,
	// never bricks.
	serverHupCh := make(chan os.Signal, 1)
	signal.Notify(serverHupCh, syscall.SIGHUP)
	defer signal.Stop(serverHupCh)
	vmmHupCh := make(chan os.Signal, 1)
	signal.Notify(vmmHupCh, syscall.SIGHUP)
	defer signal.Stop(vmmHupCh)
	// serverReload re-runs the loader on every SIGHUP and surfaces
	// the freshly-loaded *tls.Config via rotator.Set on success.
	// The closure is goroutine-safe (the loader is stateless
	// beyond cfg).
	serverReload := func() (*tls.Config, error) {
		return cfg.LoadServerTLSWithPrefixAndVerifierAndReload(nodeVerifier, nil)
	}
	vmmReload := func() (*tls.Config, error) {
		return cfg.LoadVMMTLSWithPrefixAndVerifierAndReload(nodeVerifier, nil)
	}
	go wire.WatchTLSReload(ctx, log, serverHupCh, serverRotator, serverReload)
	go wire.WatchTLSReload(ctx, log, vmmHupCh, vmmRotator, vmmReload)

	lis, err := deps.listen(ctx, listenTarget, serverTLS, cfg.OwnerUser)
	if err != nil {
		return fmt.Errorf("schedd: listen %s: %w", listenTarget, err)
	}
	// Issue #571 PR-A2: /readyz probe (PG ping + gRPC bound).
	// Probe construction is platform-portable — pool may be nil
	// in unit tests that don't wire a real pgxpool; the probe
	// short-circuits to "pg pool nil (test path)" in that case.
	scheddProbe, scheddBound := BuildReadinessProbe(ctx, pool, 5*time.Second)
	scheddProbe.SetReadyObserver(func(ready bool, reason string) {
		ops.MarkReady("schedd", ready, reason)
	})
	// NOTE: scheddBound.MarkBound() is intentionally NOT called
	// here — see cmd/schedd/readiness.go for why. The flip must
	// fire inside the serve goroutine, just before gsrv.Serve,
	// so a panic during the ~465 lines of setup below cannot
	// leave /readyz reporting ready while no gRPC server is
	// actually running (PR #1091 review Finding 5).
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
		// The canonical scrape combines the wire metrics and the dashboard
		// gauges. Keep the sibling endpoint below for existing operators.
		mux.Handle(metricsPath, promhttp.HandlerFor(
			prometheus.Gatherers{ops.Registry(), dashGauges.Registry()},
			promhttp.HandlerOpts{Registry: ops.Registry()},
		))
		mux.Handle(metricsPath+"/fcvm", dashGauges.Handler())
		// Issue #571 PR-A2: /healthz + /readyz on the metrics mux
		// (operator-side, loopback-only). Source of truth is the
		// same BuildReadinessProbe wired at the deps.listen site
		// above — single source between /readyz body and the
		// daemon_ready gauge (issue #586 / ADR-129).
		wire.ControlMuxLite(mux, scheddProbe.ReadyFunc(), scheddProbe.ReasonFunc())
		// ADR-122: apply the canonical metrics-listener shape —
		// RT/WT/IT/MHB from cfg.MetricsListener (cfg → constant
		// fallback). ReadHeaderTimeout=10s stays from before ADR-122.
		readTimeout, writeTimeout, idleTimeout, maxHeaderBytes := cfg.MetricsListener()
		httpSrv = &http.Server{
			Addr:              cfg.MetricsAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    int(maxHeaderBytes),
		}
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

	// ADR-098: app-delete dispatch is folded into loop.Run's
	// existing LISTEN (see loop.go's NotifyAppDelete case in
	// handleNotification). No standalone goroutine, no extra pool
	// connection — same zero-cost multiplexing pattern as
	// NotifyCronRunNow (PR-D / issue #791). The AppDeleteSubscriber
	// is constructed and passed to the loop via
	// WithAppDeleteSubscriber below so loop.handleNotification can
	// call evictApp on every NotifyAppDelete delivery.

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

	// Tier A9 / ADR-087: pressure-rebalancer config. Same
	// fail-fast contract as the A4/A5/A6 envs above — a
	// typo in any of the three overrides must not silently
	// fall back to the api.* defaults. The pressure
	// rebalance watcher reads the threshold + cadence at
	// every tick, so an operator tweak is picked up on the
	// next sweep without a schedd restart. The migration
	// policy is closed-set: validators panic on a bad value
	// via WithPressureMigrationPolicy.
	pressureThreshold := api.PressureAtCapacityThresholdPerMin
	pressureReassess := api.PressureReassessmentIntervalSeconds
	pressurePolicy := api.PressureMigrationPolicy
	if v := os.Getenv("FAAS_PRESSURE_THRESHOLD_PER_MIN"); v != "" {
		n, parseErr := strconv.Atoi(v)
		if parseErr != nil || n <= 0 {
			log.Error("FAAS_PRESSURE_THRESHOLD_PER_MIN must be a positive integer",
				"value", v)
			return fmt.Errorf("FAAS_PRESSURE_THRESHOLD_PER_MIN: %s", v)
		}
		pressureThreshold = n
	}
	if v := os.Getenv("FAAS_PRESSURE_REASSESSMENT_SECONDS"); v != "" {
		n, parseErr := strconv.Atoi(v)
		if parseErr != nil || n <= 0 {
			log.Error("FAAS_PRESSURE_REASSESSMENT_SECONDS must be a positive integer",
				"value", v)
			return fmt.Errorf("FAAS_PRESSURE_REASSESSMENT_SECONDS: %s", v)
		}
		pressureReassess = n
	}
	if v := os.Getenv("FAAS_PRESSURE_MIGRATION_POLICY"); v != "" {
		pressurePolicy = v
	}
	engine.WithPressureConfig(pressureThreshold, pressureReassess)
	engine.WithPressureMigrationPolicy(pressurePolicy)
	pressureAgg := sched.NewPressureAggregator()
	engine.WithPressureAggregator(pressureAgg)

	// Stale-RUNNING billing-leak self-healer (issue: dead vmmd
	// leaves instances RUNNING in PG → meterd bills for VMs that
	// no longer exist). Same fail-fast contract as the
	// migrating-watchdog envs above: a typo in either override
	// must not silently fall back to the api.* default. The
	// staleness env routes through the engine setter
	// (WithDeadNodeReconcilerStalenessSeconds) because the
	// reconciler reads it at tick time — operator tweaks don't
	// require a schedd restart. The interval env patches cfg
	// directly because the loop builder owns the ticker; the env
	// is the highest-precedence source so it overrides TOML
	// without redeploy.
	if v := os.Getenv("FAAS_DEAD_NODE_RECONCILER_STALENESS_SECONDS"); v != "" {
		n, parseErr := strconv.Atoi(v)
		if parseErr != nil || n <= 0 {
			log.Error("FAAS_DEAD_NODE_RECONCILER_STALENESS_SECONDS must be a positive integer",
				"value", v)
			return fmt.Errorf("FAAS_DEAD_NODE_RECONCILER_STALENESS_SECONDS: %s", v)
		}
		engine.WithDeadNodeReconcilerStalenessSeconds(n)
	}
	if v := os.Getenv("FAAS_DEAD_NODE_RECONCILER_INTERVAL_SECONDS"); v != "" {
		n, parseErr := strconv.Atoi(v)
		if parseErr != nil || n <= 0 {
			log.Error("FAAS_DEAD_NODE_RECONCILER_INTERVAL_SECONDS must be a positive integer",
				"value", v)
			return fmt.Errorf("FAAS_DEAD_NODE_RECONCILER_INTERVAL_SECONDS: %s", v)
		}
		cfg.DeadNodeReconcilerIntervalSeconds = n
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

	// Tier A9 / ADR-087: pressure-rebalancer watcher. Polls
	// the in-process aggregator (incremented at every
	// WakeResult{AtCapacity:true} return) on a fixed cadence
	// and dispatches Engine.RebalancePressuredApps for each
	// pressured app. The beforeSweepHook bumps the per-app
	// sweep counter so the policy gate (migrate_after_2)
	// reads the current sweep count when the handle is
	// invoked. Spawned only when ownerNodeID is set — the
	// legacy single-box posture has no peer to migrate to.
	if ownerNodeID != "" {
		prReb := sched.NewPressureRebalancer(
			pressureAgg,
			pressureThreshold,
			time.Duration(pressureReassess)*time.Second,
			func(appID string) { engine.IncrementPressureSweepCounter(appID) },
			func(ctx context.Context, appID string) error {
				return engine.RebalancePressuredApps(ctx, appID)
			},
			log,
		)
		go func() {
			if err := prReb.Run(ctx); err != nil &&
				!errors.Is(err, context.Canceled) {
				log.Warn("sched: pressure rebalancer: run returned", "err", err)
			}
		}()
		// Cold-start sweep — catches apps that breached the
		// threshold while schedd was down. The aggregator
		// is in-process and survives restarts only if the
		// schedd hadn't restarted; in practice the sweep
		// is best-effort.
		go func() {
			if n := prReb.RunColdStartSweep(ctx); n > 0 {
				log.Info("sched: pressure rebalancer: cold-start sweep done",
					"apps_swept", n)
			}
		}()
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
	// probes active nodes through a bounded worker pool. Each probe still uses a
	// fresh *VMMClient via deps.dialVMM, but slow/dead nodes cannot serialize the
	// fleet sweep. On success we stamp last_heartbeat_at; on failure we flip
	// active=false so placement skips the dead node. Production cadence is
	// overridable via FAAS_HEARTBEAT_INTERVAL; tests inject a sub-second
	// interval through runDeps.heartbeatInterval.
	hb := sched.NewHeartbeat(store, sched.HeartbeatDialerFunc(deps.dialVMM), vmmTLS, log).
		WithOwnerNodeID(ownerNodeID).
		WithNodeRegistry(nodeRegistry)
	hb.Interval = cfg.HeartbeatInterval
	hb.Staleness = cfg.HeartbeatStaleness
	if deps.heartbeatInterval > 0 {
		// Tests inject a sub-second cadence via runDeps to exercise
		// the wiring without waiting 30s for production cadence.
		hb.Interval = deps.heartbeatInterval
	}
	// PR-A observability slice (issue #170): per-{app,node} instance
	// stats poller. Builds a Reader (the canonical seam for per-instance
	// policy consumers; #169 scale-up reads from it), wires the Poller with the
	// same deps.dialVMM the heartbeat uses (so dial churn is bounded
	// by the dialer — same pattern PR #120 established), and
	// attaches it via WithInstanceStats. The same Reader is passed to
	// the reactive scale-up trigger below; its cumulative activity
	// counter is the provider-independent RPS fallback when the
	// optional gateway metrics endpoint is unavailable.
	reader := instancestats.NewReader()
	statsPoller := instancestats.NewPoller(
		store,
		instancestats.DialerFunc(deps.dialVMM),
		vmmTLS,
		reader,
		ops,
		log,
	).WithTelemetry(engine.NodeTelemetryCache()).WithNodeRegistry(nodeRegistry)
	// Register the gRPC server with the Reader wired so the
	// ListInstanceStats RPC (issue #279 / PR-B) can serve the
	// per-instance CPU-µs snapshot to meterd. The reader is
	// populated by the persistent capacity stream and projected locally at the
	// poller's 200 ms cadence; a meterd call before the first stream frame
	// returns an empty list.
	scheddgrpc.NewWithStats(engine, reader, ops, log).
		WithOwner(scheddgrpc.OwnerNodeID(ownerNodeID), store).
		Register(gsrv)

	// Serve goroutine — must run AFTER Register or grpc fatals.
	serveErr := make(chan error, 1)
	go func() {
		log.Info("grpc listening", "addr", listenTarget, "service", scheddpb.Schedd_ServiceDesc.ServiceName)
		// Flip the gRPC bound signal immediately before
		// gsrv.Serve so /readyz reflects "the gRPC server is
		// actually running" — not merely "the unix socket is
		// bound" (PR #1091 review Finding 5).
		scheddBound.MarkBound()
		serveErr <- gsrv.Serve(lis)
	}()

	// Issue #169 / #172: per-app reactive scale-up trigger.
	// Reads apps.autoscale_target_* + Ledger.Concurrency every
	// cfg.ScaleUpInterval (default 1s); admits another instance
	// when measured per-instance RPS or CPU exceeds the target
	// and headroom is available. The trigger is nil-safe on every
	// dep; an empty GatewayMetricsURL disables only the optional
	// gateway scrape. The VMMD activity-counter signal from the
	// instancestats.Reader remains available for split-box and
	// bare-metal deployments. The engine adapter converts
	// sched.WakeResult → scaleup.AdmitResult (a small subset —
	// the trigger only inspects AtCapacity).
	//
	// Issue #557 / ADR-071: lift the auditor to a local so the
	// floor trigger can share the same actor="schedd" instance
	// and emit `floor.wake` audit rows on every proactive admit.
	schedulerAuditor := audit.New(store, log, ops, "schedd")
	// ADR-098: app-delete handler. Built here (not via the
	// runDeps.subscribeAppDelete seam — that seam's now a stub
	// retained only for the main_coverage_smoke_test defaultDeps
	// assertion) and dispatched from loop.Run's existing LISTEN
	// so we don't add a 7th long-term pool subscriber on top of
	// pool.MaxConns=16 (which leaves the async-invoke drain's
	// BeginTx into starvation under e2e query bursts).
	appDeleteSub := sched.NewAppDeleteSubscriber(engine, log)
	loop := sched.NewLoop(pool, engine, log).
		WithAppDeleteSubscriber(appDeleteSub).
		WithJobsDispatched(jobsDispatched).
		WithFlowCounter(sched.NewNodeAwareFlowCounter(engine.NodeTelemetryCache(), flowcount.NewReader(wire.ExecRunner{}))).
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
		//
		// Issue #557 / ADR-071: the same schedulerAuditor instance
		// is also wired into the floor trigger so floor.wake events
		// share the actor="schedd" attribution.
		WithAudit(schedulerAuditor).
		// Issue #171: aggressive reaper toggle + per-tick park cap.
		// cfg.ReaperAggressive defaults ON; FAAS_REAPER_AGGRESSIVE=false
		// disables in-place. cfg.ReaperAggressiveParkCap=0 → default
		// (sched.MaxParksPerTickPerApp = 8).
		WithReaperAggressive(cfg.ReaperAggressive).
		WithReaperParkCap(cfg.ReaperAggressiveParkCap).
		// Issue #554 / ADR-078: same LivenessWindow pointer as
		// Engine.WithLivenessWindow above (constructed earlier in
		// main). The loop doesn't tick the window — the Engine
		// calls RecordRestart synchronously inside
		// DestroyForLivenessFailure — but cmd/schedd wiring both
		// surfaces the integration point in one place.
		WithLivenessWindow(livenessWindow)

	// Issue #757 / ADR-118 (PR #993 review MED-3): attach the
	// broker-egress shaper to the dispatch loop. Env vars:
	//
	//	FAAS_BROKER_EGRESS_MBIT   positive int; 0/unset → noop
	//	FAAS_BROKER_EGRESS_IFNAME  default "faas-brokerq"
	//
	// Pre-MED-3 the BrokerEgressConfig seam existed in
	// pkg/sched/broker_egress.go and WithBrokerAccountor was on
	// the WireChain, but cmd/schedd never parsed the env vars,
	// so every dispatch tick silently fell through to the noop
	// accountor and the broker egress was effectively unbounded
	// in prod. A parse error fails boot loudly so a typo can't
	// silently disable the shaper.
	if beCfg, ok, err := brokerEgressConfigFromEnv(); err != nil {
		log.Error("broker egress config: invalid env", "err", err)
		os.Exit(1)
	} else if ok {
		loop = loop.WithBrokerAccountor(sched.NewBrokerAccountor(beCfg))
		log.Info("broker egress shaper attached",
			"mbit", beCfg.EgressMbit,
			"ifname", beCfg.InterfaceName)
	}

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

	// Stale-RUNNING billing-leak self-healer. Closes the gap
	// between heartbeat (which flips compute_nodes.active=false)
	// and meterd's sampler (which keeps billing on CountsForRAM
	// regardless of node liveness). Cadence mirrors the
	// migrating-watchdog pattern: zero cfg → api.* default. The
	// handle is the engine method directly so the reconciler
	// shares the same store / ledger / logger as the rest of the
	// loop — there is no per-call indirection that could go stale.
	dnrInterval := cfg.DeadNodeReconcilerIntervalSeconds
	if dnrInterval <= 0 {
		dnrInterval = api.DeadNodeReconcilerIntervalSeconds
	}
	loop.WithDeadNodeReconciler(sched.NewDeadNodeReconciler(
		engine.ReconcileDeadNodeInstances,
		time.Duration(dnrInterval)*time.Second,
		log))
	// Issue #171: share a single HTTPPromScraper between the gateway
	// scrape path for the RPS scale-up trigger and the aggressive-
	// reaper signal mirror.
	// The concurrent_requests and CPU triggers do not depend on the
	// optional gateway metrics endpoint, so an empty metrics URL must
	// not disable those workers.
	var scraper scaleup.PromScraper
	if cfg.GatewayMetricsURL != "" {
		scraper = scaleup.NewHTTPPromScraper(cfg.GatewayMetricsURL)
	}
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
	trigger.WithOwnerNodeID(ownerNodeID)
	loop.WithScaleUp(trigger)
	// The concurrent_requests trigger consumes the instance-stats reader
	// directly and must not depend on the optional Prometheus scrape URL.
	// Keep this wiring independent so disabling GatewayMetricsURL only
	// disables the RPS/recent-load path, not in-flight based scale-out.
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
		targetsTrigger.WithOwnerNodeID(ownerNodeID)
		loop.WithTargets(targetsTrigger)
		log.Info("concurrent_requests target trigger enabled",
			"interval", cfg.ScaleUpInterval,
			"owner_node_id", ownerNodeID)
	}
	// Issue #557 / ADR-071: proactive min-instances floor
	// reconciler. Walks every app the schedd owns each tick and
	// admits instances up to the effective floor (max of legacy
	// column + ScalingPolicy jsonb). Distinct from the scale-up
	// and targets triggers — those are reactive (RPS / CPU /
	// inflight signal); the floor trigger is proactive, the
	// customer's SLA is "min N resident at all times". Uses the
	// same engine + ledger as the other triggers so the engine's
	// wake-gate remains the single admission authority.
	//
	// FAAS_FLOOR_INTERVAL_SECONDS overrides the trigger cadence
	// (operator can dampen during incidents without restarting).
	// Default falls back to cfg.ScaleUpInterval so a single
	// shared dial governs all three triggers when no env is set;
	// api.FloorDecisionIntervalSeconds (1s) is the trigger's own
	// last-resort default. A non-positive / unparseable env
	// returns a typed error so a typo surfaces at boot rather
	// than silently damping the floor reconciler off.
	floorInterval := cfg.ScaleUpInterval
	if v := os.Getenv("FAAS_FLOOR_INTERVAL_SECONDS"); v != "" {
		n, parseErr := strconv.Atoi(v)
		if parseErr != nil || n <= 0 {
			return fmt.Errorf("FAAS_FLOOR_INTERVAL_SECONDS: %s", v)
		}
		floorInterval = time.Duration(n) * time.Second
	}
	floorTrigger := floor.New(
		store,
		store, // deploymentStore — issue #557 closure / ADR-074 (per-deployment walk)
		schedFloorLedger{ledger: engine.Ledger()},
		schedFloorEngine{engine: engine},
		floor.Options{
			Logger:       log,
			Metrics:      ops,
			Interval:     floorInterval,
			Auditor:      schedulerAuditor,
			PlanResolver: schedFloorPlanResolver{store: store},
		},
	)
	floorTrigger.WithOwnerNodeID(ownerNodeID)
	loop.WithFloor(floorTrigger)
	log.Info("min-instances floor reconciler enabled",
		"interval", floorInterval,
		"owner_node_id", ownerNodeID)
	// Issue #171: wire the recent-load mirror off the same scraper so the
	// reaper sees per-app RPS without duplicating the scraping wiring.
	// It has no signal source when the optional endpoint is not configured.
	if scraper != nil {
		mirror := recentload.New(scraper, api.ScaleUpWindowSeconds, time.Second)
		loop.WithRecentLoad(mirror)
		log.Info("autoscale signal mirror enabled",
			"metrics_url", cfg.GatewayMetricsURL,
			"window_s", api.ScaleUpWindowSeconds,
			"aggressive", cfg.ReaperAggressive)
		// Issue #72 / ADR-124 / ADR-125 PR-A3 commit 4: mirror
		// invocation_summary rollup + ledger retention sweep.
		// Runs on the same interval as the scale-up triggers so
		// a single shared dial governs the schedd's housekeeping
		// cadence. Errors are logged Warn inside RollupLoop and
		// retried on the next tick — a persistent failure
		// surfaces as a flood of WARN logs an operator can alert
		// on.
		go mirrorRollup.RollupLoop(ctx, mirrorPoolAdapter{pool: pool}, mirrorRollup.DefaultRollupInterval, log)
		log.Info("mirror rollup + ledger sweep enabled",
			"interval", mirrorRollup.DefaultRollupInterval,
			"retention", mirrorRollup.DefaultLedgerRetention)
	}
	// Cron dispatch path: route synthetic requests through gatewayd-internal's
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
			log.Warn("gateway synth dial: cron traffic will not flow until gatewayd-internal is up",
				"target", synthTarget, "err", dialErr)
		} else {
			// ADR-119 — wire the per-app public_auth_mode lookup
			// + JWT minter so cron traffic reaching an
			// internal_only app carries Authorization: Bearer.
			// The mode lookup reads the same per-app cache the
			// dispatcher already populates (no extra SQL). The
			// minter is the closure below — it loads the
			// Ed25519 keypair from cluster_signing_keys (PR-3
			// / ADR-125 fleet-wide key) with a per-host
			// FAAS_INTERNAL_SVC_KEY_PATH fallback, and mints
			// a fresh JWT per synth request (TTL 30s; the
			// §15 plan calls out replay-attack posture). The
			// minter is nil-safe: a dev box without either
			// source leaves the minter nil and SynthesizeRequest
			// logs a loud warn + internal_only requests 403.
			if minter, mErr := newSchedInternalSvcMinter(ctx, store, log); mErr != nil {
				log.Warn("schedd: internal-svc minter not wired; internal_only cron requests will 403 until corrected",
					"err", mErr.Error())
			} else {
				modeLookup := sched.PublicAuthModeFromStore(store.AppByID)
				sched.ConfigureInternalSvcAuth(synth, modeLookup, minter.AsFunc())
				// The trigger batch path (postBatch) does NOT
				// route through httpGatewaySynth — it uses
				// l.gatewayHTTPClient directly. Wire the same
				// lookup + minter on the Loop so the batch
				// endpoint carries the JWT for internal_only
				// apps. Without this, the gate at
				// synth.go::handleInvocationDispatchBatch would
				// 403 every internal_only batch the schedd posts.
				loop.WithAppPublicAuthModeLookup(modeLookup)
				loop.WithMintInternalSvcToken(minter.AsFunc())

				// PR-3 / ADR-125 rotation: subscribe to the
				// cluster_signing_keys_changed channel and
				// atomic-swap the minter on every delivery.
				// Best-effort — if the subscribe fails the
				// boot-time key keeps working; rotation
				// just requires a daemon restart until the
				// operator fixes the channel/subscribe path.
				if subErr := SubscribeClusterKeyChanges(ctx, pool, store, minter, log); subErr != nil {
					log.Warn("schedd: cluster key rotation subscribe failed; minter frozen at boot key",
						"err", subErr.Error())
				}
			}
			loop.WithGatewaySynth(synth)
		}
	}

	// Issue #757 / ADR-100 (commits #13/#14): wire the HTTP transport
	// the trigger dispatch tick uses to post batch envelopes to
	// /v1/invocations:dispatch_batch. The gateway side serves that
	// route on the same unix socket as the cron dispatch path
	// (cmd/gatewayd-internal/run.go::gatewaydInternalSocket);
	// postBatch() strips the unix:// scheme and dials that path
	// directly. We keep a separate http.Client (rather than sharing
	// the synth RPC) because the cron surface uses gRPC semantics
	// over a unix socket transport, while the batch endpoint is
	// plain HTTP/1.1 JSON — different idle-pool shapes, different
	// request timeouts.
	//
	// Mirror the cron path above: a failed dial is warn-and-skip,
	// not boot-fatal. Otherwise a schedd-only e2e (no gatewayd-internal
	// in the harness, so cfg.GatewaySynthSocket="") crashes schedd on
	// PR #910 even when the test never exercises the trigger batch
	// path. The trigger tick's `WithGatewayHTTPClient(nil, "")` shape
	// makes `l.runTriggerTick` short-circuit on every tick (it returns
	// at the top when the http client is nil; see runTriggerTick at
	// pkg/sched/dispatch_triggers.go). Production schedd whose
	// gatewayd-internal is genuinely down still gets caught by the
	// systemd unit restart loop — the cron dial failure above is
	// already a journal signal in that case.
	//
	// Audit finding #5 (PR #910 boot-fatal stance): the rationale was
	// "every trigger wake funnels through dispatch_batch, so a missed
	// dial silently loses wakes". The mitigation here preserves that
	// visibility (a `trigger batch dispatch dial failed` warn line
	// appears at every boot) without bricking the daemon when the
	// trigger primitive isn't being exercised. Future work (PR-B):
	// schedule a 30s reconnect ticker that retries the dial so a
	// transient gatewayd-internal outage self-heals.
	if synthTarget != "" {
		triggerClient, triggerBase, triggerDialErr := sched.HTTPClientForGatewaySynthTarget(synthTarget)
		if triggerDialErr != nil {
			log.Warn("trigger batch dispatch dial: trigger tick will idle until gatewayd-internal is up",
				"target", synthTarget, "err", triggerDialErr)
		} else {
			loop.WithGatewayHTTPClient(triggerClient, triggerBase)
		}
	} else {
		log.Warn("trigger batch dispatch dial skipped: synthTarget empty (gatewayd-internal not wired in this schedd)")
	}
	loopErr := make(chan error, 1)
	go func() { loopErr <- loop.Run(ctx) }()

	// Issue #757 / ADR-0NN (commit #16): trigger dispatch
	// wakeups. Subscribe to the trigger_ready + trigger_changed
	// channels and forward every payload as a single
	// Loop.WakeupTriggers() nudge so an idle broker doesn't sit
	// for a full 1s tick before the first batch. The 1s ticker
	// remains the safety net (PR-C pattern).
	//
	// Audit finding #9: a failed first Subscribe was previously
	// logged and the daemon kept running — the 1s ticker caught
	// every trigger_ready notify that arrived during the outage,
	// but a missed delivery on a stream-of-record entry stays
	// missed until the trigger re-fires. Pair the boot-fatal
	// (sibling to the dial above) with a 5s retry ticker so a
	// transient Postgres blip recovers without a daemon restart.
	triggerNotifC, triggerSubErr := db.SubscribeWithReconnect(ctx, pool,
		[]string{db.NotifyTriggerReady, db.NotifyTriggerChanged}, log)
	if triggerSubErr != nil {
		log.Error("schedd: trigger notify first-subscribe failed; safety ticker + retry-loop running",
			"err", triggerSubErr)
		// Pair #5 + #9: spawn a retry loop so a transient blip
		// doesn't strand the dispatch tick until next restart.
		// 5s cadence balances (operator wants visibility) vs
		// (Postgres LISTEN recovery is usually < 1s). On
		// success we install the notifier exactly like the
		// happy path.
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					ch, sErr := db.SubscribeWithReconnect(ctx, pool,
						[]string{db.NotifyTriggerReady, db.NotifyTriggerChanged}, log)
					if sErr != nil {
						log.Warn("schedd: trigger notify subscribe retry failed",
							"err", sErr)
						continue
					}
					log.Info("schedd: trigger notify subscribe recovered")
					for {
						select {
						case <-ctx.Done():
							return
						case _, ok := <-ch:
							if !ok {
								break
							}
							loop.WakeupTriggers()
						}
					}
				}
			}
		}()
	} else {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-triggerNotifC:
					if !ok {
						return
					}
					loop.WakeupTriggers()
				}
			}
		}()
	}

	// Issue #791 PR-D: fire-now is folded into loop.Run's existing
	// LISTEN (db.NotifyCronRunNow is multiplexed onto the same
	// long-term connection as NotifyAppChanged / NotifyDeploymentChanged
	// / NotifySnapshotPrime — see pkg/sched/loop.go:348-352) plus a
	// 60s safety ticker (fireNowT). Zero extra pool connections vs
	// the pre-rebase PR-D posture; the standalone FireNowRun
	// goroutine added a 7th long-term subscriber that tipped
	// pool.MaxConns=16 over the edge and starved the async-invoke
	// drain's BeginTx under e2e query bursts.
	drainDispatchConcurrency := sched.DefaultDrainDispatchConcurrency
	if v := strings.TrimSpace(os.Getenv("FAAS_SCHEDD_INVOCATION_DISPATCH_CONCURRENCY")); v != "" {
		n, parseErr := strconv.Atoi(v)
		if parseErr != nil || n < 1 {
			return fmt.Errorf("FAAS_SCHEDD_INVOCATION_DISPATCH_CONCURRENCY must be a positive integer: %q", v)
		}
		drainDispatchConcurrency = n
	}

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
				sched.WithDrainLogger(log),
				sched.WithDrainDispatchConcurrency(drainDispatchConcurrency))
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

	// Issue #476 / ADR-076: outbound webhook delivery dispatcher.
	// Drains app_webhook_deliveries on a 5s tick with FOR UPDATE SKIP
	// LOCKED claim (per-account round-robin) + retry-with-backoff +
	// DLQ-at-7. Shares the schedulerAuditor so the audit rows carry
	// actor="schedd". The IdentityLoader is the same FAAS_HOST_AGE
	// path the alert evaluator uses (pkg/alerts/evaluator.go:380-395).
	//
	// The dispatcher is a sibling goroutine, not an HTTP server, so
	// it gets the full DefaultDrainTimeout (10s) to flush in-flight
	// POSTs on SIGTERM. The HTTP graceful-stop 5s budget above
	// applies to gsrv + httpSrv only — they do not gate the
	// dispatcher's drain.
	webhookDispatcher := webhook.NewDispatcher(store, schedulerAuditor, log)
	webhookDispatcher.IdentityLoader = func() []*age.X25519Identity {
		path := cfg.HostAgeIdentityPath
		if path == "" {
			path = secretbox.DefaultHostKeyPath
		}
		ident, err := secretbox.LoadHostKey(path)
		if err != nil || ident == nil {
			return nil
		}
		return []*age.X25519Identity{ident}
	}
	// Propagate the dispatcher's lifecycle error through loopErr so
	// a non-context-canceled exit (e.g. a poisoned row) tears down
	// schedd with the rest of the loop — same posture as loopErr +
	// the drain goroutine above. A ctx-canceled return is treated as
	// a clean shutdown and ignored.
	webhookLoopErr := make(chan error, 1)
	go func() {
		err := webhookDispatcher.Run(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			webhookLoopErr <- err
		} else {
			webhookLoopErr <- nil
		}
	}()

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
	case err := <-webhookLoopErr:
		if err != nil {
			log.Warn("webhook dispatcher exited with error", "err", err)
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

// jobsDispatchEnabled is intentionally an exact opt-in. Treating any
// non-empty value (including "0" or "false") as enabled makes a templated
// production environment unexpectedly activate an incomplete jobs path.
func jobsDispatchEnabled(value string) bool {
	return strings.TrimSpace(value) == "1"
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
// brokerEgressConfigFromEnv is the MED-3 (PR #993 / issue #757
// closure) env-var seam for the broker egress shaper. The
// WireChain callsite at main.go:1271+ consumes the returned
// (cfg, true, nil) pair to attach the BrokerAccountor to the
// dispatch loop. Returns:
//
//   - (zero, false, nil)  → env unset, deploy without a shaper
//     (noopBrokerAccountor, the pre-MED-3
//     default; preserves the broker_egress
//     seam's documented "zero = noop"
//     semantics).
//   - (cfg, true, nil)    → env set, attach cfg to the loop.
//   - (zero, false, err)  → parse error; caller MUST fail boot
//     loudly so the operator notices the
//     typo instead of silently running
//     without a shaper.
func brokerEgressConfigFromEnv() (sched.BrokerEgressConfig, bool, error) {
	v := os.Getenv("FAAS_BROKER_EGRESS_MBIT")
	if v == "" {
		return sched.BrokerEgressConfig{}, false, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return sched.BrokerEgressConfig{}, false, fmt.Errorf("FAAS_BROKER_EGRESS_MBIT must be a positive integer (got %q)", v)
	}
	cfg := sched.BrokerEgressConfig{
		InterfaceName: os.Getenv("FAAS_BROKER_EGRESS_IFNAME"),
		EgressMbit:    n,
	}
	if cfg.InterfaceName == "" {
		cfg.InterfaceName = "faas-brokerq"
	}
	return cfg, true, nil
}

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
// scaleup.AdmitResult. trigger (ADR-127) is forwarded so the emitted
// wake.boot_started / wake.boot_completed events stamp the scaleup
// trigger enum value.
func (s schedScaleUpEngine) AdmitInstance(ctx context.Context, appID, scope, trigger string) (scaleup.AdmitResult, error) {
	r, err := s.engine.AdmitInstance(ctx, appID, "", scope, trigger)
	if err != nil {
		return scaleup.AdmitResult{}, err
	}
	return scaleup.AdmitResult{InstanceID: r.InstanceID, AtCapacity: r.AtCapacity}, nil
}

// AdmitInstances implements scaleup.BurstEngine and preserves only the
// result fields the trigger consumes. The wrapped scheduler still owns every
// admission check and returns partial results with the first error.
func (s schedScaleUpEngine) AdmitInstances(ctx context.Context, appID, scope, trigger string, count int) ([]scaleup.AdmitResult, error) {
	results, err := s.engine.AdmitInstances(ctx, appID, scope, trigger, count)
	out := make([]scaleup.AdmitResult, 0, len(results))
	for _, r := range results {
		out = append(out, scaleup.AdmitResult{InstanceID: r.InstanceID, AtCapacity: r.AtCapacity})
	}
	return out, err
}

// EnsureWake (ADR-098) implements scaleup.Engine: delegates to the
// wrapped engine's single-flight wake entry and lifts the relevant
// fields into the thinned scaleup.WakeOutcome. AtCapacity is dropped
// because the leader's ledger closes the at-cap loop; the trigger
// observes the path via the bus, not the return value. trigger
// (ADR-127) is forwarded to the leader's Engine.Wake call.
func (s schedScaleUpEngine) EnsureWake(ctx context.Context, appID, trigger string) (scaleup.WakeOutcome, error) {
	r, err := s.engine.EnsureWake(ctx, appID, trigger)
	if err != nil {
		return scaleup.WakeOutcome{}, err
	}
	if r.Instance == nil {
		return scaleup.WakeOutcome{}, nil
	}
	return scaleup.WakeOutcome{
		InstanceID: r.Instance.InstanceID,
		WakeID:     r.Instance.WakeID,
		ColdBoot:   r.Instance.ColdBoot,
	}, nil
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
func (s schedTargetsEngine) AdmitInstance(ctx context.Context, appID, scope, trigger string) (targets.AdmitResult, error) {
	r, err := s.engine.AdmitInstance(ctx, appID, "", scope, trigger)
	if err != nil {
		return targets.AdmitResult{}, err
	}
	return targets.AdmitResult{InstanceID: r.InstanceID, AtCapacity: r.AtCapacity}, nil
}

// AdmitInstances implements targets.BurstEngine and preserves only the
// result fields the trigger consumes. The wrapped scheduler still owns every
// admission check and returns partial results with the first error.
func (s schedTargetsEngine) AdmitInstances(ctx context.Context, appID, scope, trigger string, count int) ([]targets.AdmitResult, error) {
	results, err := s.engine.AdmitInstances(ctx, appID, scope, trigger, count)
	out := make([]targets.AdmitResult, 0, len(results))
	for _, r := range results {
		out = append(out, targets.AdmitResult{InstanceID: r.InstanceID, AtCapacity: r.AtCapacity})
	}
	return out, err
}

// EnsureWake (ADR-098) implements targets.Engine: delegates to the
// wrapped engine's single-flight wake entry and lifts the relevant
// fields into the thinned targets.WakeOutcome. AtCapacity is dropped
// because the leader's ledger closes the at-cap loop. trigger
// (ADR-127) is forwarded to the leader's Engine.Wake call.
func (s schedTargetsEngine) EnsureWake(ctx context.Context, appID, trigger string) (targets.WakeOutcome, error) {
	r, err := s.engine.EnsureWake(ctx, appID, trigger)
	if err != nil {
		return targets.WakeOutcome{}, err
	}
	if r.Instance == nil {
		return targets.WakeOutcome{}, nil
	}
	return targets.WakeOutcome{
		InstanceID: r.Instance.InstanceID,
		WakeID:     r.Instance.WakeID,
		ColdBoot:   r.Instance.ColdBoot,
	}, nil
}

// schedFloorEngine (issue #557 / ADR-071) adapts *sched.Engine to
// the floor.Engine interface. Mirrors schedTargetsEngine above —
// delegates to engine.AdmitInstance and lifts InstanceID +
// AtCapacity into floor.AdmitResult. AtCapacity is forwarded so
// the trigger's at_capacity branch re-observes the wake-gate
// rejection (no FAILED row, no backoff — engine already handled
// the unattached row).
type schedFloorEngine struct {
	engine *sched.Engine
}

// AdmitInstance implements floor.Engine.
func (s schedFloorEngine) AdmitInstance(ctx context.Context, appID, scope, trigger string) (floor.AdmitResult, error) {
	r, err := s.engine.AdmitInstance(ctx, appID, "", scope, trigger)
	if err != nil {
		return floor.AdmitResult{}, err
	}
	return floor.AdmitResult{InstanceID: r.InstanceID, AtCapacity: r.AtCapacity}, nil
}

// AdmitInstanceForDeployment implements floor.Engine (issue #557
// closure / ADR-074 — per-deployment floor wake). trigger (ADR-127)
// is forwarded so the emitted wake.boot_started / wake.boot_completed
// events stamp the floor.deployment trigger enum value.
func (s schedFloorEngine) AdmitInstanceForDeployment(ctx context.Context, appID, deploymentID, scope, trigger string) (floor.AdmitResult, error) {
	r, err := s.engine.AdmitInstanceForDeployment(ctx, appID, deploymentID, scope, trigger)
	if err != nil {
		return floor.AdmitResult{}, err
	}
	return floor.AdmitResult{InstanceID: r.InstanceID, AtCapacity: r.AtCapacity}, nil
}

// EnsureWake (ADR-098) implements floor.Engine: delegates to the
// wrapped engine's single-flight wake entry and lifts the relevant
// fields into the thinned floor.WakeOutcome. AtCapacity is dropped
// because the leader's ledger closes the at-cap loop; the trigger
// observes the path via the bus, not the return value. trigger
// (ADR-127) is forwarded to the leader's Engine.Wake call.
func (s schedFloorEngine) EnsureWake(ctx context.Context, appID, trigger string) (floor.WakeOutcome, error) {
	r, err := s.engine.EnsureWake(ctx, appID, trigger)
	if err != nil {
		return floor.WakeOutcome{}, err
	}
	if r.Instance == nil {
		return floor.WakeOutcome{}, nil
	}
	return floor.WakeOutcome{
		InstanceID: r.Instance.InstanceID,
		WakeID:     r.Instance.WakeID,
		ColdBoot:   r.Instance.ColdBoot,
	}, nil
}

// schedFloorPlanResolver (issue #557 / ADR-071) adapts
// state.Store.AccountByID into floor.PlanResolver. The trigger
// walks apps per-tick and consults the resolver to gate the
// floor-wake (Free plan → OutcomeDisabled). Returns false when the
// account lookup misses so the trigger falls back to PlanFree
// (fail-closed).
type schedFloorPlanResolver struct {
	store state.Store
}

// ResolvePlan implements floor.PlanResolver.
func (s schedFloorPlanResolver) ResolvePlan(ctx context.Context, accountID string) (api.Plan, bool) {
	acct, err := s.store.AccountByID(ctx, accountID)
	if err != nil {
		return api.PlanFree, false
	}
	return acct.Plan, true
}

// schedFloorLedger (issue #557 / ADR-071) adapts *sched.NodeLedger
// to floor.Ledger. The floor trigger reads Concurrency (per-app
// gate), ResidentRAM (global Σ), and HeadroomMB (§6.2-2 ceiling
// pre-check). Concurrency + ResidentRAM are already exposed by
// schedTargetsLedger; HeadroomMB is the new method the floor
// trigger adds.
type schedFloorLedger struct {
	ledger *sched.NodeLedger
}

// Concurrency implements floor.Ledger.
func (s schedFloorLedger) Concurrency(appID string) int {
	return s.ledger.Concurrency(appID)
}

// ConcurrencyForDeployment implements floor.Ledger (issue #557
// closure / ADR-074 — per-(app, deployment) live count).
func (s schedFloorLedger) ConcurrencyForDeployment(appID, deploymentID string) int {
	return s.ledger.ConcurrencyForDeployment(appID, deploymentID)
}

// ResidentRAM implements floor.Ledger.
func (s schedFloorLedger) ResidentRAM() int {
	return s.ledger.ResidentRAM()
}

// HeadroomMB implements floor.Ledger.
func (s schedFloorLedger) HeadroomMB() int {
	return s.ledger.HeadroomMB()
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

// mirrorPoolAdapter (issue #72 / ADR-124 / ADR-125 PR-A3 commit 4)
// bridges *pgxpool.Pool.Exec's pgconn.CommandTag return into the
// int64-shaped execer interface pkg/mirror expects. Mirrors the
// canonical cmd/meterd/main.go::poolAdapter shape (the meter
// rollup uses the same seam). Avoids importing pgxpool into
// pkg/mirror and keeps the rollup unit-testable without a
// Postgres dependency.
type mirrorPoolAdapter struct{ pool *pgxpool.Pool }

func (a mirrorPoolAdapter) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := a.pool.Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
