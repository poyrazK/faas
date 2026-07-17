package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Notification is a single pg_notify delivery on a subscribed channel.
// Channel names live in one place (cmd/* and pkg/* use the constants below).
type Notification struct {
	Channel string
	Payload string
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
//	NotifyDeploymentChanged {"kind":"image|tarball|dockerfile|function",
//	                         "app_id":uuid, "deployment_id":uuid,
//	                         "to":"pending|building|...|live|failed",
//	                         "image_digest":"sha256:..."}      // image_digest when kind=image
//	NotifyDomainChanged     {"domain":"..."}
//	NotifyCronChanged       {"cron_id":uuid, "app_id":uuid}
//	NotifyKeyChanged        {"key_id":uuid}
//	NotifyBuildQueued       {"build_id":uuid, "app_id":uuid,
//	                         "kind":"tarball|dockerfile|function",
//	                         "deployment_id":uuid}
//	NotifyDomainVerify      {"domain":"..."}
//	NotifyInstanceChanged   {"instance_id":uuid, "app_id":uuid,
//	                         "state":"parked|running|cold_booting|..."}
//	NotifySnapshotPrime     {"app_id":uuid, "deployment_id":uuid}
//	                         imaged → schedd: layer is built, cold-boot once and
//	                         snapshot it (spec §5 step 6, ADR-018).
//	NotifySnapshotWritten   {"deployment_id":uuid, "mem_path":"...",
//	                         "vmstate_path":"...", "mem_bytes":int,
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
//	                         was dispatched through gatewayd so metering
//	                         and rate limits apply identically (spec §4.4,
//	                         M7 cron firing).
const (
	NotifyAppChanged        = "app_changed"
	NotifyDeploymentChanged = "deployment_changed"
	NotifyDomainChanged     = "domain_changed"
	NotifyCronChanged       = "cron_changed"
	NotifyKeyChanged        = "key_changed"
	NotifyBuildQueued       = "build_queued"
	NotifyDomainVerify      = "domain_verify"
	NotifyInstanceChanged   = "instance_changed"
	NotifySnapshotPrime     = "snapshot_prime"
	NotifySnapshotWritten   = "snapshot_written"
	NotifyBillingPastDue    = "billing_past_due"
	NotifyQuotaWarning      = "quota_warning"
	NotifyCronFired         = "cron_fired"
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
