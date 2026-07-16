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

// NotifyChannels are the pg_notify channel names used across the platform.
// Keep this list aligned with the LISTEN calls in cmd/schedd, cmd/imaged,
// cmd/apid (verifier goroutine), and the producer side of every Store
// mutation.
const (
	NotifyAppChanged        = "app_changed"        // apps insert/update/delete
	NotifyDeploymentChanged = "deployment_changed" // deployments insert/status transition
	NotifyDomainChanged     = "domain_changed"     // custom_domains insert/update/delete
	NotifyCronChanged       = "cron_changed"       // crons insert/update/delete
	NotifyKeyChanged        = "key_changed"        // api_keys insert/delete
	NotifyBuildQueued       = "build_queued"       // builderd consumer
	NotifyDomainVerify      = "domain_verify"      // apid verifier consumer
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

	out := make(chan Notification, 16)
	cancel := func() {
		conn.Release()
		// Drain so a producer doesn't block on a closed channel.
		go func() {
			for range out {
			}
		}()
		close(out)
	}

	go func() {
		defer cancel()
		for {
			n, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				// ctx cancellation closes the connection; surface as EOF.
				return
			}
			select {
			case out <- Notification{Channel: n.Channel, Payload: n.Payload}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, cancel, nil
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
