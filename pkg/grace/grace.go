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
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
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
type ArchiveDeleter interface {
	Delete(context.Context, string) error
}

type Params struct {
	Store       state.Store
	Mailer      Sender
	Interval    time.Duration // default 60s
	Now         func() time.Time
	Log         *slog.Logger
	Notif       Notifier
	Audit       Auditor // optional; PR-5.5 emits account.deleted after DeleteAccount fires
	Artifacts   storage.StorageBackend
	LogArchives ArchiveDeleter
	Registry    prometheus.Registerer
}

// Grace is the 30-day deletion-grace timer. Owns the ticker + the
// sweep loop; Run exits cleanly when ctx is cancelled.
type Grace struct {
	p       Params
	metrics *metrics
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
	return &Grace{p: p, metrics: newMetrics(p.Registry)}
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
		if err := g.deleteAccountWithArchives(ctx, acct); err != nil {
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
	return g.RunAppsOnce(ctx)
}

// RunAppsOnce permanently removes app tombstones whose seven-day restore
// window has elapsed. It is separate so callers and tests can drive app
// cleanup independently from the account sweep.
func (g *Grace) RunAppsOnce(ctx context.Context) error {
	rows, err := g.p.Store.ListDeletedApps(ctx)
	if err != nil {
		g.metrics.failure("list_tombstones")
		return err
	}
	now := g.p.Now()
	missingDeadlines := 0
	var retainedArtifactBytes int64
	for _, app := range rows {
		if app.DeleteGraceUntil != nil && app.DeleteGraceUntil.After(now) {
			continue
		}
		func() {
			unlock, err := g.archiveLock(ctx, app.ID)
			if err != nil {
				g.metrics.failure("archive_lock")
				g.p.Log.Warn("grace: archive lock failed", "app", app.ID, "err", err)
				return
			}
			defer unlock(context.WithoutCancel(ctx))
			if app.DeleteGraceUntil != nil {
				if err := g.p.Store.ClaimAppDeletion(ctx, app.ID); err != nil {
					if !errors.Is(err, state.ErrNotFound) {
						g.metrics.failure("claim_tombstone")
						g.p.Log.Warn("grace: claim app deletion failed", "app", app.ID, "err", err)
					}
					return
				}
			}
			artifacts, err := g.p.Store.ListAppDeletionArtifacts(ctx, app.ID)
			if err != nil {
				g.metrics.failure("list_artifacts")
				g.p.Log.Warn("grace: list app deletion artifacts failed", "app", app.ID, "err", err)
				if app.DeleteGraceUntil == nil {
					missingDeadlines++
				}
				return
			}
			var appArtifactBytes int64
			for _, artifact := range artifacts {
				if artifact.Bytes > 0 {
					appArtifactBytes += artifact.Bytes
				}
			}
			if app.DeleteGraceUntil == nil {
				missingDeadlines++
				retainedArtifactBytes += appArtifactBytes
				g.p.Log.Error("grace: quarantined deleted app without purge deadline",
					"app", app.ID, "slug", app.Slug, "artifact_bytes", appArtifactBytes)
				return
			}
			if len(artifacts) != 0 && g.p.Artifacts == nil {
				retainedArtifactBytes += appArtifactBytes
				g.metrics.failure("artifact_backend_missing")
				g.p.Log.Error("grace: refusing app purge without artifact backend",
					"app", app.ID, "artifact_count", len(artifacts), "artifact_bytes", appArtifactBytes)
				return
			}
			artifactFailed := false
			for _, artifact := range artifacts {
				if err := g.p.Artifacts.Delete(ctx, artifact.Key); err != nil && !storage.IsNotFound(err) {
					artifactFailed = true
					if artifact.Bytes > 0 {
						retainedArtifactBytes += artifact.Bytes
					}
					g.metrics.failure("delete_artifact")
					g.p.Log.Warn("grace: app artifact delete failed", "app", app.ID,
						"key", artifact.Key, "bytes", artifact.Bytes, "err", err)
				}
			}
			if artifactFailed {
				return
			}
			if err := g.deleteAppArchives(ctx, app); err != nil {
				g.metrics.failure("delete_log_archive")
				g.p.Log.Warn("grace: log archive cleanup failed", "app", app.ID, "err", err)
				return
			}
			if err := g.p.Store.DeleteAppPermanently(ctx, app.ID); err != nil {
				if errors.Is(err, state.ErrNotFound) {
					return
				}
				g.metrics.failure("delete_database")
				g.p.Log.Warn("grace: app hard delete failed", "app", app.ID, "err", err)
				return
			}
			g.metrics.deleted.Inc()
			// Reuse the existing lifecycle channel so schedd's cleanup path
			// gets a durable redelivery signal. The app row has already been
			// removed, so consumers treat a missing row as an idempotent no-op.
			payload, _ := json.Marshal(map[string]string{"app_id": app.ID, "slug": app.Slug})
			if err := g.p.Notif(ctx, db.NotifyAppDelete, string(payload)); err != nil {
				g.p.Log.Warn("grace: app delete notify failed", "app", app.ID, "err", err)
			}
			appID := app.AccountID
			g.p.Audit.Emit(ctx, "app.deleted", &appID, map[string]any{
				"app_id": app.ID, "slug": app.Slug, "actor": "grace-sweep",
			})
			g.p.Log.Info("grace: app hard-deleted", "app", app.ID, "slug", app.Slug)
		}()
	}
	g.metrics.missingDeadlines.Set(float64(missingDeadlines))
	g.metrics.expiredArtifactBytes.Set(float64(retainedArtifactBytes))
	g.metrics.lastSuccess.SetToCurrentTime()
	return nil
}

func (g *Grace) archiveLock(ctx context.Context, appID string) (func(context.Context), error) {
	locker, ok := g.p.Store.(state.RuntimeLogArchiveLocker)
	if !ok {
		if _, catalogOK := g.p.Store.(state.RuntimeLogArchiveCatalog); catalogOK {
			return nil, errors.New("grace: archive publication lock unavailable")
		}
		return func(context.Context) {}, nil
	}
	return locker.AcquireRuntimeLogArchivePublicationLock(ctx, appID)
}

// Pending reservations are collected too: PUT can time out after storage commits.
func (g *Grace) deleteAppArchives(ctx context.Context, app state.App) error {
	catalog, ok := g.p.Store.(state.RuntimeLogArchiveCatalog)
	if !ok {
		return nil
	}
	refs, err := catalog.ListAppRuntimeLogArchives(ctx, app.ID)
	if err != nil {
		return fmt.Errorf("list log archives: %w", err)
	}
	if len(refs) != 0 && g.p.LogArchives == nil {
		return errors.New("grace: log archive store unavailable")
	}
	for _, ref := range refs {
		if !ref.ValidReference() || ref.AppID != app.ID || ref.AccountID != app.AccountID || ref.ComputeRegion != app.ComputeRegion {
			return errors.New("grace: invalid log archive owner")
		}
		if err := g.p.LogArchives.Delete(ctx, ref.StorageKey); err != nil && !storage.IsNotFound(err) {
			return fmt.Errorf("delete log archive %s: %w", ref.StorageKey, err)
		}
	}
	return nil
}

func (g *Grace) deleteAccountWithArchives(ctx context.Context, expected state.Account) error {
	accountID := expected.ID
	apps, err := g.p.Store.ListApps(ctx, accountID)
	if err != nil {
		return err
	}
	deleted, err := g.p.Store.ListDeletedApps(ctx)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(apps))
	for _, app := range apps {
		seen[app.ID] = true
	}
	for _, app := range deleted {
		if app.AccountID == accountID && !seen[app.ID] {
			apps = append(apps, app)
		}
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].ID < apps[j].ID })
	// AccountDeletedPending forbids new reservations. Drain and collect each app
	// separately: retaining every lock would exhaust the connection pool before
	// the ordinary catalog queries or final account delete could run.
	for _, app := range apps {
		if err := g.collectAccountAppArchives(ctx, expected, app); err != nil {
			return err
		}
	}
	if err := g.checkAccountDeletion(ctx, expected); err != nil {
		return err
	}
	return g.p.Store.DeleteAccount(ctx, accountID)
}

func (g *Grace) collectAccountAppArchives(ctx context.Context, expected state.Account, app state.App) error {
	unlock, err := g.archiveLock(ctx, app.ID)
	if err != nil {
		return err
	}
	defer unlock(context.WithoutCancel(ctx))
	if err := g.checkAccountDeletion(ctx, expected); err != nil {
		return err
	}
	return g.deleteAppArchives(ctx, app)
}

func (g *Grace) checkAccountDeletion(ctx context.Context, expected state.Account) error {
	account, err := g.p.Store.AccountByID(ctx, expected.ID)
	if err != nil {
		return err
	}
	if account.Status != state.AccountDeletedPending || account.DeletionRequestedAt == nil || expected.DeletionRequestedAt == nil || !account.DeletionRequestedAt.Equal(*expected.DeletionRequestedAt) || account.DeletionRequestedAt.After(g.p.Now().Add(-state.DeletionGraceDuration())) {
		return state.ErrNotFound
	}
	return nil
}
