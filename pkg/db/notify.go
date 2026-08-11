package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrWaitTimeout is returned by WaitForNotification when the timeout
// elapses without a matching payload. It is distinct from
// context.DeadlineExceeded so the queueReceive / invokeApp handlers can
// tell "server-side budget ran out" from "client cancelled".
//
// Move 2 (long-lived surface). The drain and daemon-side loops use
// SubscribeWithReconnect because they own a long-lived LISTEN session;
// per-request callers (apid handlers) want the short-lived sibling.
var ErrWaitTimeout = errors.New("db: wait timeout")

// Notification is a single pg_notify delivery on a subscribed channel.
// Channel names live in one place (cmd/* and pkg/* use the constants below).
type Notification struct {
	Channel string
	Payload string
}

// BuildQueuedPayload is the wire shape on the `build_queued` channel
// (apid emits on the write path; imaged re-emits for stale queued
// rows — PR-A). Both producers and consumers marshal/unmarshal via
// this struct so the four fields stay in lock-step. builderd only
// reads BuildID; imaged reads all four (see imaged/handler.go).
type BuildQueuedPayload struct {
	BuildID      string `json:"build"`
	DeploymentID string `json:"deployment"`
	AppID        string `json:"app"`
	Kind         string `json:"kind"`
}

// Notify publishes a payload on the given channel. Non-blocking; returns an
// error only if the underlying pg_notify call fails. Payloads are limited to
// ~8 KB by Postgres — caller's responsibility.
func Notify(ctx context.Context, pool *pgxpool.Pool, channel, payload string) error {
	_, err := pool.Exec(ctx, "SELECT pg_notify($1, $2)", channel, payload)
	if err != nil {
		return fmt.Errorf("db: notify %s: %w", channel, err)
	}
	return nil
}

// PoolNotifier adapts *pgxpool.Pool to the small Notifier interface
// daemons (meterd, imaged, schedd) take in their constructors. Production
// callers do `db.PoolNotifier{Pool: pool}.Notify(ctx, ch, payload)`;
// tests substitute a recording fake so they can assert on the payload.
type PoolNotifier struct {
	Pool *pgxpool.Pool
}

// Notify forwards to the package-level Notify helper.
func (p PoolNotifier) Notify(ctx context.Context, channel, payload string) error {
	return Notify(ctx, p.Pool, channel, payload)
}

// NotifyChannels are the pg_notify channel names used across the platform.
// Keep this list aligned with the LISTEN calls in cmd/schedd, cmd/imaged,
// cmd/apid (verifier goroutine), and the producer side of every Store
// mutation.
//
// Payload contracts (JSON, all optional fields may be omitted):
//
//	NotifyAppChanged        {"app_id":uuid}
//	NotifyDeploymentChanged {"kind":"image|tarball|dockerfile|function|
//	                         rollback|superseded",
//	                         "app_id":uuid, "deployment_id":uuid,
//	                         "from":uuid,         // present on rollback|superseded
//	                         "to":uuid,           // the deployment the listener
//	                                            //   should react to (target of
//	                                            //   rollback, victim of supersede,
//	                                            //   or fresh deployment on first
//	                                            //   image/tarball/dockerfile emit)
//	                         "status":"pending|building|...|live|failed|superseded",
//	                         "image_digest":"sha256:..."}      // image_digest when kind=image
//	NotifyDomainChanged     {"domain":"..."}
//	NotifyCronChanged       {"cron_id":uuid, "app_id":uuid}
//	NotifyKeyChanged        {"key_id":uuid}
//	NotifyBuildQueued       {"build_id":uuid, "app_id":uuid,
//	                         "kind":"tarball|dockerfile|function",
//	                         "deployment_id":uuid}
//	NotifyBuildLog          {"build":"uuid","line":"..."}
//	                         builderd → dashboards / SSE: live build output
//	                         (UX spec §2.4 streamed logs).
//	NotifyDomainVerify      {"domain":"..."}
//	NotifyInstanceChanged   {"instance_id":uuid, "app_id":uuid,
//	                         "state":"parked|running|cold_booting|..."}
//	NotifySnapshotPrime     {"app_id":uuid, "deployment_id":uuid}
//	                         imaged → schedd: layer is built, cold-boot once and
//	                         snapshot it (spec §5 step 6, ADR-018).
//	NotifySnapshotWritten   {"deployment_id":uuid, "vmstate_path":"...",
//	                         "storage_key":"...", "mem_bytes":int,
//	                         "vmstate_bytes":int, "fc_version":"..."}
//	                         schedd → imaged: a park wrote a snapshot blob;
//	                         imaged records the row (it is the sole writer to the
//	                         snapshots table, CLAUDE.md ownership).
//	NotifyBillingPastDue    {"account_id":uuid, "used_gb":float,
//	                         "quota_gb":int, "at":rfc3339nano}
//	                         meterd → apid/dashboard: Free-tier hard stop
//	                         triggered (spec §4.7). The account row is
//	                         already flipped to `suspended` in the same tick.
//	NotifyQuotaWarning      {"account_id":uuid, "plan":"hobby|pro|scale",
//	                         "used_gb":float, "quota_gb":int,
//	                         "at":rfc3339nano}
//	                         meterd → dashboard: paid-tier overage crossed
//	                         100 %; apps keep running, overage accrues at
//	                         €0.01/GB-h (spec §1, §10).
//	NotifyCronFired         {"cron_id":uuid, "app_id":uuid, "at":rfc3339nano}
//	                         schedd → dashboard: a synthetic cron request
//	                         was dispatched through gatewayd-internal so metering
//	                         and rate limits apply identically (spec §4.4,
//	                         M7 cron firing).
//	NotifyCronRunNow        {"request_id":uuid}
//	                         apid → schedd: a customer just POSTed
//	                         /v1/crons/{id}/run; the row in
//	                         cron_fire_now_requests (migrations/00193) is
//	                         the durable record; the notify is the
//	                         wakeup. schedd's fire-now consumer
//	                         (pkg/sched/fire_now.go) does
//	                         `SELECT … FOR UPDATE SKIP LOCKED LIMIT 1`
//	                         from the table and calls RunCronNow in
//	                         its own process. ADR-090 PR-C.
//	NotifyAccountDeletionPending {"account_id":uuid, "scheduled_at":rfc3339nano,
//	                         "restore_until":rfc3339nano}
//	                         apid → audit/sessions: customer scheduled
//	                         their account for deletion (spec §17 G6).
//	                         schedd subscribes to drop any live instance
//	                         belonging to the account at the moment of
//	                         pending (ADR-024 — schedd walks instances
//	                         directly; no RAM-aggregate in the payload).
//	NotifyAccountDeleted    {"account_id":uuid}
//	                         apid/pkg/grace → audit: the 30-day grace
//	                         window lapsed and the hard delete ran.
//	                         Anything that kept an in-memory cache (the
//	                         gateway's route table, meterd's per-account
//	                         usage map) re-reads on this signal.
//	NotifyAppDelete         {"app_id":uuid}
//	                         apid → schedd: an app was deleted (spec
//	                         §6.2 / ADR-095). schedd's app-delete
//	                         subscriber (pkg/sched/app_delete_subscriber.go)
//	                         calls Engine.wakeCoord.Forget(appID) so any
//	                         in-flight wake on the deleted app unwinds
//	                         with ErrAppDeleted instead of waiting for
//	                         the wake-coord TTL.
//	NotifySnapshotBoot      {"app_id":uuid, "deployment_id":uuid}
//	                         builderd → imaged: a build VM has produced an
//	                         OCI image tarball and stamped it on
//	                         deployments.rootfs_path. imaged converts the
//	                             tarball into the per-app ext4 (drive1) and
//	                             then re-emits NotifySnapshotPrime for schedd
//	                             to cold-boot + snapshot. Splits the
//	                             "prime" edge in two so the OCI tarball is
//	                             never exposed to schedd's vmmd call (which
//	                             would try to mount .tar as virtio-blk and
//	                             400). Only imaged subscribes.
const (
	NotifyAppChanged        = "app_changed"
	NotifyDeploymentChanged = "deployment_changed"
	NotifyDomainChanged     = "domain_changed"
	NotifyCronChanged       = "cron_changed"
	NotifyKeyChanged        = "key_changed"
	NotifyBuildQueued       = "build_queued"
	NotifyBuildLog          = "build_log"
	NotifyDomainVerify      = "domain_verify"
	NotifyInstanceChanged   = "instance_changed"
	NotifySnapshotPrime     = "snapshot_prime"
	NotifySnapshotBoot      = "snapshot_boot"
	NotifySnapshotWritten   = "snapshot_written"
	NotifyBillingPastDue    = "billing_past_due"
	NotifyQuotaWarning      = "quota_warning"
	NotifyCronFired         = "cron_fired"
	// NotifyCronRunNow fires when a row is inserted into
	// cron_fire_now_requests (migrations/00193) — apid emits, schedd
	// consumes via cmd/schedd/main.go's existing SubscribeWithReconnect
	// block. See the payload contract in the file header. ADR-090 PR-C.
	NotifyCronRunNow             = "cron_run_now"
	NotifyAccountDeletionPending = "account_deletion_pending"
	NotifyAccountDeleted         = "account_deleted"
	// NotifyAppDelete is emitted by apid on app deletion (spec §6.2
	// / ADR-095). schedd's app-delete subscriber consumes it and
	// evicts any in-flight wake for the deleted app via
	// Engine.wakeCoord.Forget so followers unwind promptly
	// instead of waiting for the wake-coord TTL.
	NotifyAppDelete = "app_delete"
	// NotifyCliAuthCodeActivated fires when a dashboard /cli-auth
	// POST successfully claims a pending code (binds it to an
	// account_id). Reserved for a follow-up SSE push from apid to
	// the CLI; today the CLI polls /v1/cli-auth/exchange at 1s, so
	// no listener is wired. Add the listener when §11 introduces
	// the public-listener SSE channel design.
	NotifyCliAuthCodeActivated = "cli_auth_code_activated"
	// NotifyStatelessAdvisory {"app_id":uuid, "instance":..., "n":N,
	//                          "sample_path":"..."}
	//   Wave 0 PR-C / ADR-047: vmmd → apid → events row →
	//   pg_notify for the /v1/events SSE consumer. Small summary
	//   payload (NOT the full batch — the audit row at
	//   /v1/audit-events?kind_prefix=stateless.advisory is the
	//   detail surface). Subscriber: cmd/apid/handlers_events.go
	//   (live dashboard) and pkg/sched/engine.go (optional wake
	//   correlation, future).
	NotifyStatelessAdvisory = "stateless_advisory"
	// NotifyAlertRuleChanged {"rule_id":uuid, "account_id":uuid,
	//                         "op":"created|updated|deleted|fired"}
	//   apid → meterd: any rule-change path invalidates the
	//   ListEnabledAlertRules cache meterd keeps between ticks.
	//   The listener short-circuits a stale-rule evaluation cycle
	//   instead of waiting up to FAAS_ALERT_INTERVAL for the next
	//   sweep. Payload is informational; the listener re-reads the
	//   table on every signal anyway.
	NotifyAlertRuleChanged = "alert_rule_changed"
	// NotifyComputeNodeChanged {"node_id":uuid, "active":bool}
	// schedd (SetComputeNodeActive + UpsertComputeNode) →
	// gatewayd-internal (NodeClientCache.Evict). gatewayd-internal's per-node
	// *grpc.ClientConn must drop on every UPSERT (admin UPDATE)
	// and every active=false (heartbeat watchdog), so a future
	// request to the same node re-dials against the fresh row.
	// Issue #98 / ADR-028.
	//
	// NotifyInvocationDue {"invocation_id":uuid, "app_id":uuid,
	//                     "source":"async_invoke|queue|delayed_task|cron"}
	//   schedd drain wakes immediately on a new pending row so an
	//   async invoke lands inside the customer's SLO without waiting
	//   on the 1s safety ticker. Source identifier lets the drain skip
	//   the cap re-check on rows the apid already gated.
	// NotifyInvocationDone {"invocation_id":uuid, "app_id":uuid,
	//                     "source":"...", "state":"completed|failed|cancelled"}
	//   fired by the drain's state-machine transition; reserved for a
	//   follow-up SSE push (the dashboard polls /v1/invocations today).
	NotifyComputeNodeChanged = "compute_node_changed"
	NotifyInvocationDue      = "invocation_due"
	NotifyInvocationDone     = "invocation_done"
	// NotifyEgressPolicyChanged {"policy_id":uuid, "public_iface":"...",
	//                           "masquerade_cidr":"..."}
	//   ops → cmd/vmmd/egress_watcher: the per-host egress policy
	//   audit row was written/updated. The watcher re-renders the
	//   ruleset with the canonical values from pkg/netns.DefaultHostPolicy
	//   (the host's compile-time defaults — the payload is informational,
	//   the canonical values still live in the Go renderer), validates
	//   via `nft -c -f <staging>`, and atomic-replaces
	//   /etc/nftables.conf followed by `nft -f`. Gated on
	//   cfg.ComputeNode.NodeName != "" so single-box daemons don't
	//   observe the channel. ADR-055.
	NotifyEgressPolicyChanged = "egress_policy_changed"
	// NotifyTrustedSignerChanged {"app_id":uuid, "signer":"...",
	//                              "kind":"upserted|deleted"}
	//   apid → imaged (issue #472 / ADR-054): a row was written or
	//   removed from app_trusted_signers. imaged refreshes its
	//   in-memory cache of /etc/faas/secrets/trusted-publishers/ so
	//   a freshly-onboarded publisher takes effect on the NEXT
	//   deploy without an imaged restart. The pg_notify payload is
	//   informational — the on-disk dir is the source of truth.
	NotifyTrustedSignerChanged = "trusted_signer_changed"
	// NotifyAuditEvent {"kind":"app.signature_missing|...",
	//                    "app_id":uuid, "deployment_id":uuid,
	//                    "ref":"...", "signer":"..."}
	//   imaged → apid-side pkg/audit (issue #472 / ADR-054):
	//   imaged-side audit emits (cosign signature verify failures)
	//   travel over pg_notify so the audit write surface stays
	//   single-sourced in apid. The Loop in pkg/loop subscribes;
	//   the audit row lands in `events` like any other kind.
	NotifyAuditEvent = "audit_event"
	// NotifyWarmHintPublished {"app_id":uuid, "node_id":uuid}
	//   schedd → gatewayd-public + gatewayd-internal: the sticky-warm
	//   affinity hint for an app was just published. The row lives
	//   in the warm_hint table (migrations/00116_warm_hint.sql);
	//   both gatewayd-public and gatewayd-internal subscribe
	//   independently (their per-process LRU mirrors converge within
	//   one Postgres NOTIFY delivery, ~5 ms end-to-end). The payload
	//   is informational — consumers can re-read the row to defend
	//   against notify loss.
	NotifyWarmHintPublished = "warm_hint_published"
	// NotifyEdgeRuleChanged {"app_id":uuid, "rule_id":uuid,
	//                        "op":"created|updated|deleted"}
	//   apid → gatewayd-internal: the per-host LRU mirroring the
	//   edge_rules table was just mutated. The listener triggers a
	//   wholesale cache flush for the affected host (one-box scale
	//   assumption per spec §4.3; per-host key invalidation
	//   deferred — wholesale is the safer v1 default). The payload
	//   is informational; the consumer re-reads the row to defend
	//   against notify loss. Consumed by cmd/gatewayd-internal/
	//   backend.go (PR 8).
	NotifyEdgeRuleChanged = "edge_rule_changed"
	// NotifyGithubWebhookSecretChanged {"installation_id":bigint}
	//   apid → githubd (PR-D / ADR-012 §7 amendment): a row in
	//   github_webhook_secrets was rotated via the admin endpoint.
	//   The githubd resolver listens and drops its cached entry so
	//   the next webhook for that install rebuilds from the DB
	//   (avoiding the 60s TTL fail-closed window). The payload is
	//   informational — the consumer re-reads the row to defend
	//   against notify loss. Consumed by cmd/githubd/main.go.
	NotifyGithubWebhookSecretChanged = "github_webhook_secret_changed"
)

// Subscribe holds a dedicated connection on the pool in LISTEN state for the
// given channels and returns a Go channel that emits each notification. The
// returned cancel func releases the connection.
//
// The dedicated connection model is the standard pgx/pgxpool pattern: one
// connection is parked in LISTEN mode; the rest of the daemon uses the pool
// normally. Listeners live for the lifetime of the daemon.
//
// Usage:
//
//	notif, cancel, err := db.Subscribe(ctx, pool, []string{db.NotifyAppChanged})
//	defer cancel()
//	for n := range notif {
//	    switch n.Channel {
//	    case db.NotifyAppChanged:
//	        // react
//	    }
//	}
func Subscribe(ctx context.Context, pool *pgxpool.Pool, channels []string) (<-chan Notification, func(), error) {
	if len(channels) == 0 {
		return nil, func() {}, fmt.Errorf("db: Subscribe requires at least one channel")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, func() {}, fmt.Errorf("db: acquire listener: %w", err)
	}
	for _, ch := range channels {
		if _, err := conn.Exec(ctx, fmt.Sprintf("LISTEN %s", quoteIdent(ch))); err != nil {
			conn.Release()
			return nil, func() {}, fmt.Errorf("db: LISTEN %s: %w", ch, err)
		}
	}

	// subCtx lets cancel() signal the listener goroutine to stop. The goroutine
	// is the ONLY owner of `out` and `conn`: it closes the channel and releases
	// the connection exactly once, on exit. cancel() therefore just cancels the
	// context — safe to call any number of times, from the caller and the
	// goroutine both (context.CancelFunc is idempotent), with no double-close.
	subCtx, subCancel := context.WithCancel(ctx)
	out := make(chan Notification, 16)

	go func() {
		defer close(out)
		defer conn.Release()
		for {
			n, err := conn.Conn().WaitForNotification(subCtx)
			if err != nil {
				// ctx cancellation closes the connection; surface as EOF.
				return
			}
			select {
			case out <- Notification{Channel: n.Channel, Payload: n.Payload}:
			case <-subCtx.Done():
				return
			}
		}
	}()
	return out, subCancel, nil
}

// SubscribeWithReconnect is the production-grade LISTEN wrapper. It returns a
// channel that delivers notifications across connection drops, Postgres
// restarts, and LISTEN-side errors. On the inner channel closing (conn
// drop, server restart, LISTEN error), the wrapper resubscribes with
// exponential backoff (100ms → 5s cap) and re-emits on the outer channel.
// The outer channel closes only when ctx cancels — callers in daemon
// loops see one stable select arm forever, even if Postgres bounces.
//
// The first Subscribe is performed synchronously; a database that is
// unreachable at boot should still fail-fast (caller chooses whether to
// retry or exit). The wrapper does NOT mask the initial acquire error.
//
// Backoff resets to 100ms after each successful (re-)subscribe.
//
// F-11: replaced the silent-LISTEN-close bug class across every daemon
// loop (schedd, builderd, imaged, gatewayd-internal). The four call sites all
// switched from `db.Subscribe(...)` to this function.
func SubscribeWithReconnect(
	ctx context.Context,
	pool *pgxpool.Pool,
	channels []string,
	log *slog.Logger,
) (<-chan Notification, error) {
	if pool == nil {
		return nil, fmt.Errorf("db: SubscribeWithReconnect: nil pool")
	}
	if len(channels) == 0 {
		return nil, fmt.Errorf("db: SubscribeWithReconnect: no channels")
	}
	inner, cancel, err := Subscribe(ctx, pool, channels)
	if err != nil {
		return nil, fmt.Errorf("db: SubscribeWithReconnect initial Subscribe: %w", err)
	}
	const (
		minBackoff = 100 * time.Millisecond
		maxBackoff = 5 * time.Second
	)
	out := make(chan Notification, 32) // larger than the inner 16 so a slow consumer doesn't drop on subscribe handoff
	go func() {
		defer cancel()
		defer close(out)
		backoff := minBackoff
		for {
			select {
			case <-ctx.Done():
				return
			case n, ok := <-inner:
				if !ok {
					// Inner closed: resubscribe (could be transient conn
					// drop, pg restart, or LISTEN error). Backoff until
					// success or ctx cancel. The exponential cap keeps
					// log noise reasonable on a permanently-down DB while
					// still retrying every ≤5s.
					if log != nil {
						log.Warn("db: LISTEN channel closed; reconnecting",
							"channels", channels, "backoff", backoff.String())
					}
					for {
						if ctx.Err() != nil {
							return
						}
						// Cancel the previous inner's cancel before re-subscribing
						// (the Subscribe helper binds a fresh subCtx; defensive
						// double-cancel is safe and idempotent).
						cancel()
						inner, cancel, err = Subscribe(ctx, pool, channels)
						if err == nil {
							backoff = minBackoff
							if log != nil {
								log.Info("db: LISTEN re-subscribed", "channels", channels)
							}
							break
						}
						if log != nil {
							log.Warn("db: LISTEN re-subscribe failed",
								"channels", channels, "err", err, "backoff", backoff.String())
						}
						select {
						case <-time.After(backoff):
						case <-ctx.Done():
							return
						}
						backoff *= 2
						if backoff > maxBackoff {
							backoff = maxBackoff
						}
					}
					continue
				}
				select {
				case out <- n:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// quoteIdent quotes a SQL identifier so callers can pass channel names
// without worrying about reserved words. Postgres identifiers are not the same
// as string literals — double-quoting is the right escape.
func quoteIdent(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			out = append(out, '"', '"')
		} else {
			out = append(out, c)
		}
	}
	out = append(out, '"')
	return string(out)
}
