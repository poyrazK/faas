package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
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
	// OutboxID is non-zero when this delivery came from the durable handoff
	// queue. Consumers acknowledge it after handing the payload to their
	// idempotent handler; legacy/direct notifications leave it zero.
	OutboxID int64
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

// AppChangedPayload is the shared wire contract for app_changed. New
// producers always emit JSON. ParseAppChangedPayload also accepts the legacy
// bare UUID emitted by older database triggers during a mixed-version rollout.
type AppChangedPayload struct {
	Kind             string `json:"kind"`
	AppID            string `json:"app_id"`
	AccountID        string `json:"account_id,omitempty"`
	Slug             string `json:"slug,omitempty"`
	WakeID           string `json:"wake_id,omitempty"`
	IP               string `json:"ip,omitempty"`
	Status           string `json:"status,omitempty"`
	LifecycleChanged bool   `json:"lifecycle_changed,omitempty"`
	Legacy           bool   `json:"-"`
}

// RuntimeConfigChangedPayload is the private invalidation envelope used by
// vmmd's live configuration cache. Values are never carried on this channel;
// consumers re-read the scoped rows after receiving the wake-up.
type RuntimeConfigChangedPayload struct {
	Kind      string `json:"kind,omitempty"`
	AppID     string `json:"app_id"`
	AccountID string `json:"account_id,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Key       string `json:"key,omitempty"`
}

// ParseRuntimeConfigChangedPayload validates the minimal identity needed to
// invalidate one app's cached configuration. Legacy bare app IDs are accepted
// so mixed-version control planes can still invalidate safely.
func ParseRuntimeConfigChangedPayload(raw string) (RuntimeConfigChangedPayload, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return RuntimeConfigChangedPayload{}, errors.New("db: empty runtime config payload")
	}
	if strings.HasPrefix(raw, "{") {
		var payload RuntimeConfigChangedPayload
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return RuntimeConfigChangedPayload{}, fmt.Errorf("db: decode runtime config payload: %w", err)
		}
		payload.AppID = strings.TrimSpace(payload.AppID)
		if payload.AppID == "" {
			return RuntimeConfigChangedPayload{}, errors.New("db: runtime config payload missing app_id")
		}
		return payload, nil
	}
	return RuntimeConfigChangedPayload{AppID: raw}, nil
}

// EdgeRuleChangedPayload is the fleet convergence wire contract for edge-rule
// mutations. Prepare fences every affected hostname before the database write;
// apply invalidates caches and releases the fence after the write; abort
// releases a prepared fence when persistence fails. Generation is allocated
// from PostgreSQL and never decreases across apid restarts.
type EdgeRuleChangedPayload struct {
	AppID      string   `json:"app_id"`
	RuleID     string   `json:"rule_id,omitempty"`
	Operation  string   `json:"op"`
	Phase      string   `json:"phase,omitempty"`
	Generation int64    `json:"generation,omitempty"`
	MatchHosts []string `json:"match_hosts,omitempty"`
}

// EdgeRuleAckPayload is emitted by each serving gateway only after it has
// applied the requested prepare/apply/abort phase locally.
type EdgeRuleAckPayload struct {
	Generation int64  `json:"generation"`
	Phase      string `json:"phase"`
	Node       string `json:"node"`
}

// DeploymentRouteChangedPayload is the scheduler -> gateway handoff envelope
// for a service rollout cutover. A gateway acknowledges only after both its
// deployment weights and live target set reflect the authoritative database
// state for AppID.
type DeploymentRouteChangedPayload struct {
	AppID        string `json:"app_id"`
	DeploymentID string `json:"deployment_id"`
	Generation   int64  `json:"generation"`
}

// DeploymentRouteAckPayload is emitted by each serving gateway after it has
// applied DeploymentRouteChangedPayload locally.
type DeploymentRouteAckPayload struct {
	Generation int64  `json:"generation"`
	Node       string `json:"node"`
}

func ParseEdgeRuleChangedPayload(raw string) (EdgeRuleChangedPayload, error) {
	var payload EdgeRuleChangedPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return EdgeRuleChangedPayload{}, fmt.Errorf("db: decode edge_rule_changed payload: %w", err)
	}
	if strings.TrimSpace(payload.AppID) == "" {
		return EdgeRuleChangedPayload{}, errors.New("db: edge_rule_changed payload missing app_id")
	}
	return payload, nil
}

func ParseEdgeRuleAckPayload(raw string) (EdgeRuleAckPayload, error) {
	var payload EdgeRuleAckPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return EdgeRuleAckPayload{}, fmt.Errorf("db: decode edge_rule_ack payload: %w", err)
	}
	if payload.Generation <= 0 || strings.TrimSpace(payload.Phase) == "" || strings.TrimSpace(payload.Node) == "" {
		return EdgeRuleAckPayload{}, errors.New("db: incomplete edge_rule_ack payload")
	}
	return payload, nil
}

func ParseDeploymentRouteChangedPayload(raw string) (DeploymentRouteChangedPayload, error) {
	var payload DeploymentRouteChangedPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return DeploymentRouteChangedPayload{}, fmt.Errorf("db: decode deployment_route_changed payload: %w", err)
	}
	payload.AppID = strings.TrimSpace(payload.AppID)
	payload.DeploymentID = strings.TrimSpace(payload.DeploymentID)
	if payload.AppID == "" || payload.DeploymentID == "" || payload.Generation <= 0 {
		return DeploymentRouteChangedPayload{}, errors.New("db: incomplete deployment_route_changed payload")
	}
	return payload, nil
}

func ParseDeploymentRouteAckPayload(raw string) (DeploymentRouteAckPayload, error) {
	var payload DeploymentRouteAckPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return DeploymentRouteAckPayload{}, fmt.Errorf("db: decode deployment_route_ack payload: %w", err)
	}
	payload.Node = strings.TrimSpace(payload.Node)
	if payload.Generation <= 0 || payload.Node == "" {
		return DeploymentRouteAckPayload{}, errors.New("db: incomplete deployment_route_ack payload")
	}
	return payload, nil
}

// ParseAppChangedPayload decodes the canonical JSON envelope and the legacy
// raw UUID. It rejects anonymous JSON and arbitrary non-JSON strings so every
// consumer makes the same routing and privacy decision.
func ParseAppChangedPayload(raw string) (AppChangedPayload, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return AppChangedPayload{}, errors.New("db: empty app_changed payload")
	}
	if strings.HasPrefix(raw, "{") {
		var payload AppChangedPayload
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return AppChangedPayload{}, fmt.Errorf("db: decode app_changed payload: %w", err)
		}
		payload.AppID = strings.TrimSpace(payload.AppID)
		if payload.AppID == "" {
			return AppChangedPayload{}, errors.New("db: app_changed payload missing app_id")
		}
		payload.Kind = strings.TrimSpace(payload.Kind)
		if payload.Kind == "" {
			return AppChangedPayload{}, errors.New("db: app_changed payload missing kind")
		}
		return payload, nil
	}
	parsed, uuidErr := uuid.Parse(raw)
	canonicalUUID := uuidErr == nil && len(raw) == 36 && parsed.String() == strings.ToLower(raw)
	if !canonicalUUID && !isLowerHexID(raw) {
		if uuidErr == nil {
			uuidErr = errors.New("non-canonical UUID")
		}
		return AppChangedPayload{}, fmt.Errorf("db: invalid legacy app_changed app_id: %w", uuidErr)
	}
	return AppChangedPayload{Kind: "updated", AppID: raw, Legacy: true}, nil
}

func isLowerHexID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

// MarshalAppChangedPayload returns the canonical JSON envelope. Legacy input
// is normalized before it is forwarded to customer-visible event streams.
func MarshalAppChangedPayload(payload AppChangedPayload) ([]byte, error) {
	payload.Legacy = false
	return json.Marshal(payload)
}

// NotifyPayloadMaxBytes is the largest NOTIFY payload PostgreSQL accepts.
//
// The server's buffer is 8000 bytes including the NUL terminator, so the
// usable maximum is 7999: a 7999-byte payload is accepted and an 8000-byte
// one is rejected with "payload string too long". Both boundaries are
// asserted against a real server in notify_payload_limit_test.go rather than
// taken from the documentation, which says only "8000 bytes".
const NotifyPayloadMaxBytes = 7999

// ErrNotifyPayloadTooLarge reports a payload that cannot fit in a NOTIFY.
//
// This used to surface as a raw pgx error from deep inside a callsite that
// had already discarded its context, on a Notify whose error most producers
// deliberately ignore. Callers can now match it with errors.Is and decide,
// and the message names the channel and the two sizes.
var ErrNotifyPayloadTooLarge = errors.New("db: notify payload exceeds the PostgreSQL limit")

// Notify publishes a payload on the given channel. Deploy handoff channels
// first persist a replay row and publish an envelope in the same transaction;
// all other channels retain the direct pg_notify path.
//
// Payloads over NotifyPayloadMaxBytes are rejected before the round trip.
// The previous contract — "limited to ~8 KB by Postgres — caller's
// responsibility" — was enforced by nothing: one channel capped its content
// at 3 KiB, another switched to a pipe-delimited encoding to stay under the
// limit, and the rest simply hoped. Each new channel re-litigated the
// question, and an oversize payload failed as an opaque SQLSTATE at runtime.
func Notify(ctx context.Context, pool *pgxpool.Pool, channel, payload string) error {
	if IsDurableNotificationChannel(channel) {
		return enqueueAndNotify(ctx, pool, channel, payload)
	}
	if len(payload) > NotifyPayloadMaxBytes {
		return fmt.Errorf("%w: channel %s, %d bytes > %d",
			ErrNotifyPayloadTooLarge, channel, len(payload), NotifyPayloadMaxBytes)
	}
	_, err := pool.Exec(ctx, "SELECT pg_notify($1, $2)", channel, payload)
	if err != nil {
		return fmt.Errorf("db: notify %s: %w", channel, err)
	}
	return nil
}

// enqueueAndNotify makes the durable row and its wakeup one commit boundary.
// The producer's state mutation may have committed in an earlier transaction
// (the existing Notifier interface intentionally has no transaction handle),
// but a successful handoff can no longer disappear between enqueue and notify.
func enqueueAndNotify(ctx context.Context, pool *pgxpool.Pool, channel, payload string) error {
	if pool == nil {
		return fmt.Errorf("db: notify %s: nil pool", channel)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: notify %s begin: %w", channel, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id int64
	availableAt := time.Now().UTC().Add(notificationOutboxWakeDelay)
	err = tx.QueryRow(ctx, `
		INSERT INTO notification_outbox (channel, payload, available_at)
		VALUES ($1, $2, $3)
		RETURNING id`, channel, payload, availableAt).Scan(&id)
	if err != nil {
		return fmt.Errorf("db: enqueue notification %s: %w", channel, err)
	}
	// The envelope adds ~50 bytes, so a payload that fitted on its own can
	// overflow once wrapped. Skip only the wakeup in that case and still
	// commit the row: RunNotificationOutbox polls on a ticker independently
	// of NOTIFY, so the handoff is recovered on the next sweep with added
	// latency rather than lost.
	//
	// Failing the Exec instead would roll back the whole transaction — the
	// outbox row included — so the one mechanism built to survive a missed
	// notification would be defeated by the notification being too large.
	wire := wrapNotificationPayload(id, payload)
	if len(wire) <= NotifyPayloadMaxBytes {
		if _, err := tx.Exec(ctx, "SELECT pg_notify($1, $2)", channel, wire); err != nil {
			return fmt.Errorf("db: notify %s: %w", channel, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: notify %s commit: %w", channel, err)
	}
	return nil
}

type notificationEnvelope struct {
	OutboxID int64           `json:"_notification_outbox_id"`
	Payload  json.RawMessage `json:"_notification_payload"`
}

func wrapNotificationPayload(id int64, payload string) string {
	if id <= 0 || !json.Valid([]byte(payload)) {
		return payload
	}
	wire, err := json.Marshal(notificationEnvelope{OutboxID: id, Payload: json.RawMessage(payload)})
	if err != nil {
		return payload
	}
	return string(wire)
}

func decodeNotification(channel, payload string) Notification {
	n := Notification{Channel: channel, Payload: payload}
	if !IsDurableNotificationChannel(channel) || !json.Valid([]byte(payload)) {
		return n
	}
	var envelope notificationEnvelope
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil || envelope.OutboxID <= 0 || len(envelope.Payload) == 0 || !json.Valid(envelope.Payload) {
		return n
	}
	n.OutboxID = envelope.OutboxID
	n.Payload = string(envelope.Payload)
	return n
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
// Payload contracts (JSON; required fields are called out below):
//
//	NotifyAppChanged        {"app_id":uuid, // required
//	                         "kind":"updated|parked|woken|restart|...", // required
//	                         "wake_id":uuid           // restart correlation id
//	                         "lifecycle_changed":bool} // lifecycle fields changed
//	NotifyAppWake           {"app_id":uuid,"wake_id":uuid}
//	                         apid → schedd: durable explicit pre-warm request.
//	NotifyRuntimeConfigRestart {"app_id":uuid,"wake_id":uuid}
//	                         apid → schedd: durable destroy-without-snapshot
//	                         followed by a cold wake.
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
//	NotifyTriggerChanged    {"kind":"created|updated|deleted|paused|resumed",
//	                         "trigger_id":uuid, "app_id":uuid}
//	                         (issue #757 / ADR-0NN — listeners: schedd
//	                         trigger-fanout, dashboard SSE; mirrors
//	                         NotifyCronChanged's payload shape so the
//	                         existing decoder pattern applies unchanged)
//	NotifyKeyChanged        {"key_id":uuid}
//	NotifyClusterSigningKeysChanged {}
//	                         PR-3 / ADR-125: cluster_signing_keys row
//	                         INSERT/UPDATE/DELETE; the listener pattern
//	                         in cmd/schedd/cluster_key_loader.go and
//	                         cmd/gatewayd-internal/cluster_key_verifier_loader.go
//	                         re-loads the row on every delivery. Payload
//	                         is bare table name (mirrors
//	                         compute_node_keys_changed_trg at
//	                         schema.sql:4317-4320); consumer re-reads.
//	NotifyBuildQueued       {"build_id":uuid, "app_id":uuid,
//	                         "kind":"tarball|dockerfile|function",
//	                         "deployment_id":uuid}
//	NotifyBuildQueued       {"build_id":uuid, "app_id":uuid,
//	                         "kind":"tarball|dockerfile|function",
//	                         "deployment_id":uuid}
//	NotifyBuildChanged      {"build_id":uuid, "deployment_id":uuid,
//	                         "status":"cancelled", "reason":"user|...|system",
//	                         "cascade":bool}
//	                         apidsource → builderd: a build row was flipped
//	                         to "cancelled" (either by user-initiated
//	                         `gregale deploys cancel <id>` cascading from
//	                         CancelDeploymentTx, or by direct user action
//	                         via the future build-cancel endpoint). Payload
//	                         mirrors NotifyBuildQueued's shape so the
//	                         existing decoder pattern applies. Listeners:
//	                         cmd/builderd/main.go's build-LISTEN goroutine
//	                         dispatches on channel name and calls
//	                         VM.Cancel to drop the in-flight VM. The
//	                         source-of-truth row flip already happened
//	                         in the same tx — Cancel is fire-and-forget.
//	NotifyBuildLog          {"build":"uuid","line":"..."}
//	                         builderd → dashboards / SSE: live build output
//	                         (UX spec §2.4 streamed logs).
//	NotifyDomainVerify      {"domain":"..."}
//	NotifyInstanceChanged   {"instance_id":uuid, "app_id":uuid,
//	                         "state":"parked|running|cold_booting|..."}
//	                         App instances require app_id. Job-task
//	                         instances intentionally omit it and carry
//	                         kind:"job" so gateway subscribers ignore
//	                         the event without warning (issue #2763).
//	NotifySnapshotPrime     {"app_id":uuid, "deployment_id":uuid}
//	                         imaged → schedd: layer is built; schedd applies
//	                         the app execution mode (snapshot prime for
//	                         request/service, active worker, artifact-only job).
//	NotifySnapshotWritten   {"deployment_id":uuid, "vmstate_path":"...",
//	                         "storage_key":"...", "mem_bytes":int,
//	                         "vmstate_bytes":int, "fc_version":"...",
//	                         "base_image_version":"..."}
//	                         schedd → imaged: a park wrote a snapshot blob;
//	                         imaged records the row (it is the sole writer to the
//	                         snapshots table, CLAUDE.md ownership).
//	NotifyDeploymentReady   {"deployment_id":uuid,
//	                         "execution_mode":"worker|job",
//	                         "instance_id":uuid?}
//	                         schedd → imaged: a non-snapshot deployment is
//	                         ready for activation. Worker readiness carries the
//	                         RUNNING instance; jobs are artifact-only at deploy
//	                         time and omit instance_id. imaged remains the sole
//	                         deployment-live writer.
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
//	NotifyMigrationsApplied {"version_id":int64}
//	                         the leader's goose-up landed an applied migration
//	                         ledger row (migration 20260904000000001 trigger).
//	                         cmd/migrate -wait-for-migrations wakes and compares
//	                         the complete embedded ID set before declaring the
//	                         fleet caught up. ADR-142.
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
//	                         §6.2 / ADR-098). schedd's app-delete
//	                         subscriber (pkg/sched/app_delete_subscriber.go)
//	                         calls Engine.wakeCoord.Forget(appID) so any
//	                         in-flight wake on the deleted app unwinds
//	                         with ErrAppDeleted instead of waiting for
//	                         the wake-coord TTL.
//	NotifySnapshotBoot      {"app_id":uuid, "deployment_id":uuid, "node_id":string}
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
	NotifyAppChanged = "app_changed"
	// NotifyAppEnvChanged wakes live-config consumers after an app env row is
	// changed. The payload contains identity only; values are re-read from the
	// store by the receiving vmmd.
	NotifyAppEnvChanged = "app_env_changed"
	// NotifySecretRotated uses the same identity-only envelope for secret set,
	// rotate, and delete mutations. The name is retained for compatibility with
	// the original rotation contract.
	NotifySecretRotated = "secret_rotated"
	NotifyAppWake       = "app_wake"
	// NotifyRuntimeConfigRestart applies a changed environment or secret to a
	// live app. It is deliberately separate from app_changed/restart: restoring
	// or capturing process memory would preserve the previous environment.
	NotifyRuntimeConfigRestart = "runtime_config_restart"
	// NotifyPrivateNetworkAttachmentChanged carries the durable cleanup
	// event emitted when an app attachment is detached. Unlike the broad
	// app_changed stream, this channel is replayed so a schedd restart or
	// LISTEN gap cannot leave stale private routes on a live VM.
	NotifyPrivateNetworkAttachmentChanged = "private_network_attachment_changed"
	// NotifyPrivateNetworkChanged wakes schedd after a Gregale-owned network
	// policy or peering mutation. The payload carries account/region identity so
	// policy changes and deleted peerings converge without waiting for a sweep.
	NotifyPrivateNetworkChanged = "private_network_changed"
	NotifyDeploymentChanged     = "deployment_changed"
	// NotifyDeploymentSmokeChallenge carries a short-lived, random challenge
	// from imaged to every public gateway. It is deliberately separate from
	// deployment_changed: account SSE subscribers must never receive the token.
	// Payload: {"app_id":uuid,"deployment_id":uuid,"token":string,
	//           "expires_at":RFC3339}
	NotifyDeploymentSmokeChallenge = "deployment_smoke_challenge"
	// NotifyGithubDeploymentChanged is emitted by the deployment status
	// trigger for githubd's Check Run projector. It is intentionally separate
	// from NotifyDeploymentChanged so existing scheduler/gateway consumers do
	// not receive a duplicate of their explicit lifecycle notifications.
	NotifyGithubDeploymentChanged = "github_deployment_changed"
	NotifyDomainChanged           = "domain_changed"
	NotifyCronChanged             = "cron_changed"
	NotifyTriggerChanged          = "trigger_changed"
	// NotifyEventSubscriptionChanged wakes event-routing workers after a
	// manifest deploy creates or compensates a durable subscription row.
	NotifyEventSubscriptionChanged = "event_subscription_changed"
	// NotifyEventPublished carries the normalized CloudEvents envelope to the
	// scheduler's fanout worker. The event ledger remains authoritative; this
	// channel is a low-latency wakeup for matching and enqueueing deliveries.
	NotifyEventPublished = "event_published"
	// NotifyJobChanged fires when a row is inserted/updated/deleted
	// in public.jobs (issue #1184 Workstream A / ADR-099). Listeners:
	//   - schedd dispatchJobsTick: wakes the 1s tick to claim any
	//     newly-eligible queued tasks (kind='job_task' instances).
	//   - dashboard SSE (events/job.go::TopicJobEvent): publishes
	//     job.created/updated/deleted envelopes to live dashboards.
	// Payload shape matches NotifyCronChanged:
	//   {"kind":"created"|"updated"|"deleted", "job_id":uuid,
	//    "account_id":uuid}
	// — see handlers_jobs.go for the emit sites.
	NotifyJobChanged = "job_changed"
	// NotifyMigrationsApplied fires when the migration_notify_trg
	// (updated by migration 20260904000000001) inserts any applied row
	// into goose_db_version. cmd/migrate -wait-for-migrations
	// subscribes via cmd/migrate/wait.go::WaitForMigrationsApplied
	// so non-leader daemons block until the leader's complete migration set
	// lands before opening their own connection (PR-2 / audit F2-B /
	// ADR-124 amendment / ADR-142). The payload is the decimal version_id;
	// the waiter re-reads the exact ledger set before returning, so a
	// spurious early fire is harmless.
	NotifyMigrationsApplied = "migrations_applied"
	// NotifyTriggerReady fires when a row is inserted into
	// trigger_records (migrations/00297_triggers.sql). schedd's
	// dispatch tick consumes via cmd/schedd/main.go's existing
	// SubscribeWithReconnect block. Listeners:
	//   - pkg/sched/dispatch_triggers.go (runTriggerTick fan-in
	//     from any source — kind=queue pulls existing rows on
	//     next tick, kind=kafka/nats/redis/sqs get the immediate
	//     wake-up signal so an idle broker doesn't sit for a
	//     full 1s tick before the first batch)
	//   - dashboard SSE (commit #16 / §4.7.X — operator-facing
	//     "your trigger just received a record" notification)
	NotifyTriggerReady = "trigger_ready"
	NotifyKeyChanged   = "key_changed"
	// NotifyClusterSigningKeysChanged fires when the
	// cluster_signing_keys_changed_trg (migration 00351) inserts,
	// updates, or deletes a row in cluster_signing_keys. PR-3 /
	// audit F1+F20 / ADR-125: the listener pattern in
	// cmd/{schedd,gatewayd-internal}/cluster_*_loader.go subscribes
	// to this channel and re-loads the cluster-wide Ed25519 keypair
	// from PG, so a rotation propagates to every minter and verifier
	// within one Postgres NOTIFY delivery (~5 ms end-to-end). The
	// payload is informational — consumers always re-read the row
	// to defend against notify loss.
	NotifyClusterSigningKeysChanged = "cluster_signing_keys_changed"
	NotifyBuildQueued               = "build_queued"
	// NotifyBuildChanged mirrors the deployment_changed row-flip signal
	// at the build level (ADR-124). Fired by apidsource after the
	// CancelDeploymentTx single-tx orchestrator commits the build row
	// to "cancelled" — listeners (currently builderd only) fire their
	// VM teardown off this signal.
	NotifyBuildChanged    = "build_changed"
	NotifyBuildLog        = "build_log"
	NotifyDomainVerify    = "domain_verify"
	NotifyInstanceChanged = "instance_changed"
	NotifySnapshotPrime   = "snapshot_prime"
	NotifySnapshotBoot    = "snapshot_boot"
	NotifySnapshotWritten = "snapshot_written"
	NotifyDeploymentReady = "deployment_ready"
	NotifyBillingPastDue  = "billing_past_due"
	NotifyQuotaWarning    = "quota_warning"
	NotifyCronFired       = "cron_fired"
	// NotifyDebugRegressionChanged carries one account-scoped, redacted
	// regression observation whenever detection or operator workflow state
	// changes. Payload includes app_id, deployment_id, route, and state.
	NotifyDebugRegressionChanged = "debug_regression_changed"
	// NotifyRateLimitChanged fires on every INSERT / UPDATE of
	// tokens/last_refill on pg_ratelimit_counters (migration
	// 00126, the C4 trigger). Payload is JSON
	// {scope, subject_id, plan} — the (scope, subject_id, plan)
	// triple that uniquely identifies a counter row. Consumed
	// by pkg/wire/pgratelimit_invalidator.go to invalidate the
	// in-process LRU cache entries of every gatewayd-internal
	// replica when a peer writes to the counter (Phase 4 of
	// issue #881, ADR-104 amendment 5). Channel name is
	// hard-coded in the SQL trigger — keep them in sync.
	NotifyRateLimitChanged = "rate_limit_changed"
	// NotifyCronRunNow fires when a row is inserted into
	// cron_fire_now_requests (migrations/00193) — apid emits, schedd
	// consumes via cmd/schedd/main.go's existing SubscribeWithReconnect
	// block. See the payload contract in the file header. ADR-090 PR-C.
	NotifyCronRunNow             = "cron_run_now"
	NotifyAccountDeletionPending = "account_deletion_pending"
	NotifyAccountDeleted         = "account_deleted"
	// NotifyAppDelete is emitted by apid on app deletion (spec §6.2
	// / ADR-098). schedd's app-delete subscriber consumes it and
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
	// *grpc.ClientConn drops on effective configuration and lifecycle changes,
	// so the next request re-dials against the fresh row. ADR-161 suppresses
	// heartbeat-only and no-op updates to preserve in-flight RPCs.
	// Issue #98 / ADR-028; lifecycle is also present in the JSON payload.
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
	// NotifyAuditEvent {"outbox_id":N, "kind":"app.signature_missing|...",
	//                    "app_id":uuid, "deployment_id":uuid,
	//                    "ref":"...", "signer":"..."}
	//   imaged → apid-side audit (issue #472 / ADR-054 / ADR-141):
	//   the imaged-side signature audit path now persists a durable
	//   audit_event_outbox row and uses pg_notify as the low-latency
	//   wakeup. Apid's subscriber delivers the referenced row;
	//   its replay worker recovers missed notifications. Legacy
	//   producers without outbox_id retain the direct audit path.
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
	// NotifyDataUpstreamChanged {app_id, scope, kind, host, port, op}
	//   apid env-classifier + customer-facing
	//   POST/DELETE /v1/apps/{slug}/upstreams (PR-B) →
	//   schedd's pkg/sched/upstream_affinity.go subscriber
	//   (PR-B/C): a row in data_upstreams was written, updated,
	//   or deleted. The payload is pipe-delimited (rather than
	//   JSONB like github_webhook_secrets_changed) to keep the
	//   string under the 8000-byte pg_notify limit even on a
	//   worst-case 253-char host. Trigger definition:
	//   migrations/00226_data_upstreams.sql's
	//   data_upstreams_notify_trg. Reserved for PR-B; PR-A ships
	//   the trigger but no LISTEN subscriber — pg_notify drops
	//   unlistened payloads, so no backlog accumulates. ADR-098
	//   §D2.
	NotifyDataUpstreamChanged = "data_upstreams_changed"
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
	// NotifyEdgeRuleAck {"generation":int,"phase":"prepare|apply|abort",
	//                    "node":string}
	//   gatewayd-internal -> apid: the named serving gateway applied one
	//   phase of an edge-rule convergence barrier. This channel is consumed
	//   only by the mutation request that allocated the generation.
	NotifyEdgeRuleAck = "edge_rule_ack"
	// NotifyDeploymentRouteChanged {"app_id":uuid,
	//   "deployment_id":uuid,"generation":int}
	//   schedd -> gatewayd-internal: a readiness-gated service rollout has
	//   published its candidate as the sole positive-weight generation.
	NotifyDeploymentRouteChanged = "deployment_route_changed"
	// NotifyDeploymentRouteAck {"generation":int,"node":string}
	//   gatewayd-internal -> schedd: the named serving gateway refreshed both
	//   deployment weights and live targets for the generation.
	NotifyDeploymentRouteAck = "deployment_route_ack"
	// NotifyCachePurge is emitted by the explicit per-app cache purge API.
	// Payload: {"app_id":uuid,"path_glob":string}; an empty glob purges
	// the app's complete response cache.
	NotifyCachePurge = "cache_purge_requested"
	// NotifyAppOpenAPIDocChanged (ADR-126 / issue #975 item #2)
	// {"app_id":uuid, "op":"created|replaced|deleted"}.
	//   apid is the only listener (cmd/apid/openapi_doc_subscriber.go
	//   wires it alongside NotifyEdgeRuleChanged); the payload
	//   flushes the per-app cache entry in pkg/openapidiff.SpecCache.
	//   PR-A scope: this signal is one of two triggers for the
	//   auto-gen `?source=auto` cache. The other is
	//   NotifyEdgeRuleChanged (existing). The payload is
	//   informational — the listener re-reads the row.
	NotifyAppOpenAPIDocChanged = "app_openapi_doc_changed"
	// ADR-091 amendment (PR-A #??? / apps.maintenance_mode):
	// the existing NotifyAppChanged channel (declared in the const
	// block above; payload contract at line 73) is reused for
	// maintenance_mode flips. The maintenance trigger (migrations
	// /00221_apps_maintenance_mode.sql) fires ONLY when
	// maintenance_mode IS DISTINCT FROM OLD.maintenance_mode, so
	// the channel stays low-volume (per-app flips, not every app
	// UPDATE). The gatewayd-internal listener (cmd/gatewayd-
	// internal/run.go) calls Backend.ResetApp(appID) which
	// `delete`s the entry from the per-host apps LRU so the next
	// Backend.Lookup repopulates the row from PG and picks up the
	// new MaintenanceMode value. Without this notification the
	// apps LRU (no TTL) keeps the stale MaintenanceMode for the
	// lifetime of the cache entry — the first node to see the app
	// returns 503 forever, every subsequent node returns 200.
	// NotifyGithubWebhookSecretChanged {"installation_id":bigint}
	//   apid → githubd (PR-D / ADR-012 §7 amendment): a row in
	//   github_webhook_secrets was rotated via the admin endpoint.
	//   The githubd resolver listens and drops its cached entry so
	//   the next webhook for that install rebuilds from the DB
	//   (avoiding the 60s TTL fail-closed window). The payload is
	//   informational — the consumer re-reads the row to defend
	//   against notify loss. Consumed by cmd/githubd/main.go.
	NotifyGithubWebhookSecretChanged = "github_webhook_secret_changed"
	// NotifyTenantSurfaceChanged {"surface_id":uuid}
	//   any tenant_surfaces / tenant_hostnames mutation →
	//   cmd/gatewayd-internal: the cert-remint goroutine
	//   re-assembles the SAN set for the surface and asks the
	//   issuer (pkg/gateway/cert_issuer.go) for a fresh cert.
	//   ADR-100 D3 (issue #879). Payload is the bare surface
	//   uuid — the consumer re-reads the row + hostnames
	//   (defence against notify loss; the trigger at
	//   migrations/00243 fires on every INSERT/UPDATE/DELETE of
	//   either table, including verified flips).
	NotifyTenantSurfaceChanged = "tenant_surface_changed"
	// NotifyOperatorIntent {"intent_id":uuid, "kind":closed-vocabulary,
	//                       "target_id":uuid}
	//   apid → schedd (PR #1099 P2 redesign, ADR-127): a row
	//   was inserted into operator_intents (migrations/00431).
	//   schedd's operator-intent subscriber
	//   (pkg/sched/operator_intent_subscriber.go) is the only
	//   listener; it multiplexes onto schedd's existing
	//   SubscribeWithReconnect block (zero extra pool
	//   connections, matches PR-D's cron_run_now precedent).
	//   The payload is the bare intent_id — kind is re-read
	//   from the row at claim time (defence against notify
	//   loss; the trigger fires on every INSERT and the
	//   safety tick at 30s sweeps any unclaimed rows). The
	//   original depguard violation (apid → schedd gRPC) is
	//   closed by this channel: apid never imports
	//   pkg/scheddgrpc.
	NotifyOperatorIntent = "operator_intent"
	// NotifyRuntimeConfigChanged is emitted by migration 00466 after a
	// durable operator configuration write. Daemons reconcile the row from
	// Postgres instead of trusting the payload so missed notifications are
	// repaired on the next boot/reconnect.
	NotifyRuntimeConfigChanged          = "runtime_config_changed"
	NotifyRuntimeConfigOperationChanged = "runtime_config_operation_changed"
	// NotifyCorsPresetChanged {"account_id":uuid}
	//   any cors_presets mutation →
	//   cmd/gatewayd-internal: the per-host edge-rule LRU holds
	//   compiled EdgeRuleCORSResolved slices that baked-in the
	//   preset's allow_origins / allow_methods / etc. at the
	//   last compile. The compile path (compileCORSRules)
	//   calls GetCorsPresetByID on a cache miss, so the
	//   receipt of this notification drops the affected
	//   account's rules from the LRU wholesale; the next
	//   request recompiles and re-fetches the preset against
	//   the up-to-date row. Per-account payload so a noisy
	//   multi-tenant node doesn't drop rules for unaffected
	//   accounts. The trigger at migrations/00428 fires on
	//   every cors_presets INSERT / UPDATE / DELETE. ADR-129
	//   D4 (issue #975 #4 PR-B).
	NotifyCorsPresetChanged = "cors_preset_changed"
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
	// Session-scoped: see direct.go.
	conn, err := DirectPool(pool).Acquire(ctx)
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
			case out <- decodeNotification(n.Channel, n.Payload):
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
	// ADR-190: one LISTEN connection per pool. The hub keeps this
	// function's contract (fail-fast initial acquire, LISTEN active on
	// return, channel closes only on ctx cancel); FAAS_DB_NOTIFY_HUB=0
	// falls through to the legacy connection-per-subscriber path.
	if notifyHubEnabled() {
		return hubFor(pool, log).subscribe(ctx, channels)
	}
	// Bound the INITIAL acquire only. On this path every subscriber parks
	// its own connection, so a daemon whose pool is sized for the hub runs
	// out partway through its subscriptions — and pgxpool.Acquire waits for
	// a release that is never coming, because the connections are held by
	// this daemon's own earlier subscribers. Unbounded, that is a hang
	// before sd_notify(READY=1) with no error anywhere: the unit sits in
	// `activating` until systemd's TimeoutStartSec kills it.
	//
	// The reconnect loop below deliberately keeps its unbounded ctx; a
	// transient drop must retry forever. This deadline only converts an
	// unsatisfiable boot into a named failure.
	subCtx, subCancel := context.WithTimeout(ctx, legacySubscribeAcquireTimeout)
	inner, cancel, err := Subscribe(subCtx, pool, channels)
	subCancel()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, fmt.Errorf(
				"db: SubscribeWithReconnect(%v): could not acquire a LISTEN connection within %s. "+
					"%s=0 is set, so every subscriber parks its own connection; this pool is almost "+
					"certainly sized for the notify hub (see db.DaemonMaxConnectionsNotifyHubDisabled). "+
					"Unset %s or raise the daemon's pool budget: %w",
				channels, legacySubscribeAcquireTimeout, NotifyHubEnv, NotifyHubEnv, err)
		}
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
