package db

// Boot-time wait for PostgreSQL (ADR-1344 §5 rule 6).
//
// Every daemon unit runs Restart=on-failure with RestartSec=2s under
// StartLimitBurst=5 per 60 s. A daemon that exits because Postgres is not
// reachable yet therefore burns its whole restart budget in about ten seconds
// and lands in the failed state, and stays there after Postgres returns. That
// turned a short database outage during a host reboot, a deploy, or a
// failover into a manual restart of every daemon.
//
// Under systemd's notify protocol a daemon can instead wait for the database
// while still activating: it reports STATUS and extends its start timeout
// with EXTEND_TIMEOUT_USEC each attempt. FaasStuckActivating pages if the
// wait lasts five minutes. Outside a Type=notify unit (CLIs, tests, local
// runs) NOTIFY_SOCKET is unset and Open keeps failing fast.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/daemonunit"
)

// BootWaitEnv disables the boot-time wait when set to "0".
const BootWaitEnv = "FAAS_DB_BOOT_WAIT"

const (
	bootWaitMinBackoff = time.Second
	bootWaitMaxBackoff = 15 * time.Second
	// bootWaitLogEvery bounds the "waiting for PostgreSQL" warning.
	bootWaitLogEvery = time.Minute
)

// BootWaitRecord reports whether Open had to wait for the database. A daemon
// that waited saw the store unavailable before it started, which callers
// such as schedd's liveness verdicts must account for (ADR-1344 §5 rule 4).
type BootWaitRecord struct {
	waited atomic.Bool
}

// Waited reports whether Open retried the initial ping before it succeeded.
func (r *BootWaitRecord) Waited() bool { return r != nil && r.waited.Load() }

type bootWaitRecordKey struct{}

// WithBootWaitRecord returns a context under which Open records into r
// whether it had to wait for the database.
func WithBootWaitRecord(ctx context.Context, r *BootWaitRecord) context.Context {
	return context.WithValue(ctx, bootWaitRecordKey{}, r)
}

func recordBootWait(ctx context.Context) {
	if r, ok := ctx.Value(bootWaitRecordKey{}).(*BootWaitRecord); ok && r != nil {
		r.waited.Store(true)
	}
}

// bootWaiter retries the initial ping while the database is unreachable.
type bootWaiter struct {
	enabled    bool
	notify     func(string) error
	minBackoff time.Duration
	maxBackoff time.Duration
	log        *slog.Logger
}

func defaultBootWaiter() bootWaiter {
	return bootWaiter{
		enabled:    os.Getenv("NOTIFY_SOCKET") != "" && os.Getenv(BootWaitEnv) != "0",
		notify:     daemonunit.Notify,
		minBackoff: bootWaitMinBackoff,
		maxBackoff: bootWaitMaxBackoff,
		log:        slog.Default(),
	}
}

// ping runs ping until it succeeds. A disabled waiter, an error that waiting
// cannot fix (bad credentials, missing database), or a cancelled ctx returns
// the error at once.
func (w bootWaiter) ping(ctx context.Context, ping func(context.Context) error) error {
	backoff := w.minBackoff
	var waitedSince, lastLog time.Time
	for {
		err := ping(ctx)
		if err == nil {
			if !waitedSince.IsZero() {
				recordBootWait(ctx)
				_ = w.notify("STATUS=PostgreSQL reachable")
				w.log.Info("db: PostgreSQL reachable; continuing startup",
					"waited", time.Since(waitedSince).Round(time.Second).String())
			}
			return nil
		}
		if !w.enabled || ctx.Err() != nil || !retryableBootError(err) {
			return err
		}
		now := time.Now()
		if waitedSince.IsZero() {
			waitedSince = now
		}
		// Keep the start job alive past the next attempt and its ping.
		extend := backoff + pingTimeout + 10*time.Second
		_ = w.notify(fmt.Sprintf("EXTEND_TIMEOUT_USEC=%d\nSTATUS=waiting for PostgreSQL: %s",
			extend.Microseconds(), oneLine(err.Error())))
		if now.Sub(lastLog) >= bootWaitLogEvery {
			lastLog = now
			w.log.Warn("db: PostgreSQL unreachable at startup; waiting",
				"err", err, "waited", now.Sub(waitedSince).Round(time.Second).String())
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, w.maxBackoff)
	}
}

// retryableBootError reports whether err means "the database is not
// reachable or not accepting connections yet", as opposed to a configuration
// error that waiting cannot fix.
func retryableBootError(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case strings.HasPrefix(pgErr.Code, "08"): // connection_exception
			return true
		case strings.HasPrefix(pgErr.Code, "53"): // insufficient_resources, incl. too_many_connections
			return true
		case pgErr.Code == "57P01", pgErr.Code == "57P02", pgErr.Code == "57P03":
			// admin_shutdown, crash_shutdown, cannot_connect_now (starting up, in recovery)
			return true
		default:
			return false
		}
	}
	// No server response: refused, unreachable, socket missing, DNS, timeout.
	return true
}

func oneLine(s string) string {
	return strings.ReplaceAll(s, "\n", " ")
}
