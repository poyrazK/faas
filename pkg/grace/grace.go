// Package grace is the G6 30-day grace timer (spec §17 G6, ADR-021).
//
// When a customer calls DELETE /v1/account, apid flips the row to
// status='deleted_pending' and stamps deletion_requested_at=now(). The
// Grace goroutine in this package wakes every Interval (default 60s),
// walks ListAllAccounts, and hard-deletes any row whose grace window
// has lapsed. A redelivered tick is safe — DeleteAccount returns
// ErrNotFound for an already-gone row, which RunOnce swallows.
//
// The timer lives in apid (not meterd) because:
//   - the grace write side (DELETE /v1/account, POST /v1/account/restore)
//     is apid;
//   - meterd owns quotas + billing (spec §4.7);
//   - keeping the timer near the write path avoids a pg_notify loop
//     just to schedule a deletion.
//
// RunOnce is exported so tests drive the sweep deterministically
// without spinning up a real ticker.
package grace

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// Sender is the slice of mail.Sender the post-delete notification
// depends on. Kept as a tiny local interface so pkg/grace doesn't
// pull pkg/mail (which would create a graph cycle through pkg/api).
type Sender interface {
	Send(ctx context.Context, to []string, subject, textBody string) error
}

// Notifier publishes a payload on a pg_notify channel. Matches the
// shape of apid's `Notifier` (and pkg/db.Notify) — passed as a
// function so pkg/grace stays free of pgx imports.
type Notifier func(ctx context.Context, channel, payload string) error

// Auditor emits an audit row to the events table (issue #755 /
// PR-5.5). pkg/grace depends on this seam so the post-hard-delete
// audit row carries the source-of-truth that the deletion fired
// at <now> from the sweep goroutine (not a customer's DELETE).
// accountID may be nil if the auditor is invoked outside an account
// scope (none today); data is the structured payload that lands in
// the events.data JSONB column. Optional — nil falls back to no-op
// so tests can leave it unset.
type Auditor interface {
	Emit(ctx context.Context, kind string, accountID *string, data map[string]any)
}

// Params wires the runtime dependencies the Grace loop needs. All
// fields except Store are optional and fall back to no-op behavior;
// Store must be non-nil (the constructor panics otherwise).
type Params struct {
	Store    state.Store
	Mailer   Sender
	Interval time.Duration // default 60s
	Now      func() time.Time
	Log      *slog.Logger
	Notif    Notifier
	Audit    Auditor // optional; PR-5.5 emits account.deleted after DeleteAccount fires
}

// Grace is the 30-day deletion-grace timer. Owns the ticker + the
// sweep loop; Run exits cleanly when ctx is cancelled.
type Grace struct {
	p Params
}

// New returns a Grace ready to Run. Defaults: Interval 60s, Now
// time.Now, Log slog.Default, Mailer/Noop, Notif swallowed.
func New(p Params) *Grace {
	if p.Store == nil {
		panic("grace: Params.Store is required")
	}
	if p.Interval <= 0 {
		p.Interval = 60 * time.Second
	}
	if p.Now == nil {
		p.Now = time.Now
	}
	if p.Log == nil {
		p.Log = slog.Default()
	}
	if p.Mailer == nil {
		p.Mailer = noopSender{}
	}
	if p.Notif == nil {
		p.Notif = func(context.Context, string, string) error { return nil }
	}
	if p.Audit == nil {
		p.Audit = noopAuditor{}
	}
	return &Grace{p: p}
}

// noopAuditor discards every emit. Default for tests.
type noopAuditor struct{}

func (noopAuditor) Emit(_ context.Context, _ string, _ *string, _ map[string]any) {}

// noopSender discards every message. Default for tests.
type noopSender struct{}

func (noopSender) Send(_ context.Context, _ []string, _, _ string) error { return nil }

// Run loops on Interval until ctx is cancelled. Each tick calls
// RunOnce; an error from RunOnce is logged and swallowed so a
// transient store outage doesn't kill the loop.
func (g *Grace) Run(ctx context.Context) error {
	t := time.NewTicker(g.p.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := g.RunOnce(ctx); err != nil {
				g.p.Log.Warn("grace tick failed", "err", err)
			}
		}
	}
}

// deletionPayload is the JSON shape NotifyAccountDeleted subscribers
// (audit, sessions, meterd cache invalidation) parse. Keep the
// contract minimal — account_id is enough to evict everything keyed
// by the account.
type deletionPayload struct {
	AccountID string `json:"account_id"`
}

// RunOnce walks every account in deleted_pending state and hard-deletes
// the row(s) whose deletion_requested_at is older than the grace
// window. Idempotent: a second tick on an already-deleted row returns
// ErrNotFound from DeleteAccount and is silently ignored.
//
// Exported so tests can drive the sweep without spinning up a real
// ticker.
func (g *Grace) RunOnce(ctx context.Context) error {
	rows, err := g.p.Store.ListAllAccounts(ctx)
	if err != nil {
		return err
	}
	cutoff := g.p.Now().Add(-state.DeletionGraceDuration())
	for _, acct := range rows {
		if acct.Status != state.AccountDeletedPending {
			continue
		}
		if acct.DeletionRequestedAt == nil {
			continue
		}
		if acct.DeletionRequestedAt.After(cutoff) {
			continue
		}
		// Past grace — hard delete. The next tick will skip this row
		// because DeleteAccount removed it; we still guard on the
		// returned ErrNotFound for the redelivery race (two ticks
		// firing close together on the same overdue row).
		if err := g.p.Store.DeleteAccount(ctx, acct.ID); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			g.p.Log.Warn("grace: hard delete failed", "account", acct.ID, "err", err)
			continue
		}
		// Audit the hard delete (issue #755 / PR-5.5 events-side
		// + PR-6 audit_log-side). Two parallel rows are written so
		// the post-deletion state is preserved across two tables:
		//
		//   - The events row written here (account_id = the deleted
		//     account) is the per-event trail that the
		//     pkg/eventretention 90-day sweep (ADR-075) will trim.
		//   - The audit_log row written by DeleteAccount inside the
		//     same tx (PR-6) is the FK-free, immutable
		//     post-deletion evidence a regulator / DPO can read
		//     after the accounts row is gone. ISO 27001 SoA A.5.33
		//     retention = forever.
		//
		// actor distinguishes the sweep from a customer's DELETE so
		// a forensic auditor can tell the two surfaces apart.
		// Best-effort: the events-side auditor is non-blocking by
		// spec §5.1, so a backlog here cannot delay the sweep. The
		// audit_log-side insert lives inside the DeleteAccount tx,
		// so an insert failure rolls back the entire delete and
		// surfaces the error to the sweep above (the
		// `hard delete failed` log).
		acctID := acct.ID
		g.p.Audit.Emit(ctx, "account.deleted", &acctID, map[string]any{
			"actor":  "grace-sweep",
			"email":  acct.Email,
			"source": "grace-sweep",
		})
		// Stamp completed_at on the GDPR audit row so the export
		// bundle reflects "yes, your account was hard-deleted at
		// <now>". Best-effort; ErrNotFound just means the customer
		// never had a delete row (e.g. account was deleted out-of-band)
		// — log and keep moving.
		if err := g.p.Store.CompleteGdprRequest(ctx, acct.ID, string(state.GdprActionDelete)); err != nil && !errors.Is(err, state.ErrNotFound) {
			g.p.Log.Warn("grace: complete gdpr_requests failed", "account", acct.ID, "err", err)
		}
		payload, _ := json.Marshal(deletionPayload{AccountID: acct.ID})
		if err := g.p.Notif(ctx, db.NotifyAccountDeleted, string(payload)); err != nil {
			g.p.Log.Warn("grace: notify failed", "channel", db.NotifyAccountDeleted, "err", err)
		}
		subject, body := mail.AccountDeletionCompleteBody(acct.Email)
		if err := g.p.Mailer.Send(ctx, []string{acct.Email}, subject, body); err != nil {
			g.p.Log.Warn("grace: post-delete mail failed", "account", acct.ID, "err", err)
		}
		g.p.Log.Info("grace: account hard-deleted", "account", acct.ID)
	}
	return nil
}
