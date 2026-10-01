// Package commitmanaged owns the platform-side customer outbox relay lifecycle.
package commitmanaged

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

type Store interface {
	state.CommitStore
	ListCommitRelaySources(context.Context, string, int) ([]state.CommitRelaySource, error)
	AppByID(context.Context, string) (state.App, error)
	AccountByID(context.Context, string) (state.Account, error)
	RecordCommitRelayStatus(context.Context, state.CommitRelaySource, string, *int64, *int64, *time.Time) error
	RecordCommitBlockedEvents(context.Context, state.CommitRelaySource, []state.CommitBlockedEvent) error
	PendingCommitReplays(context.Context, string, string) ([]state.CommitReplay, error)
	CompleteCommitReplay(context.Context, string, string, string, int64) error
}

type Manager struct {
	Store             Store
	Identities        []*age.X25519Identity
	Policy            commitwork.NetworkPolicy
	AcceptedRetention time.Duration
	// Open is injectable for isolated acceptance environments. Production uses
	// the policy-enforcing TLS dialer. Each pool is closed after its bounded tick.
	Open   func(context.Context, string, commitwork.NetworkPolicy) (*pgxpool.Pool, error)
	Report func(string, string, int)
	cursor string
}

func (m *Manager) Tick(ctx context.Context) error {
	if m.Store == nil || len(m.Identities) == 0 {
		return errors.New("commit: managed relay configuration missing")
	}
	sources, err := m.Store.ListCommitRelaySources(ctx, m.cursor, 16)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		m.cursor = ""
		return nil
	}
	for _, src := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		sourceCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		count, code, backlog := m.tickSource(sourceCtx, src)
		cancel()
		statusCtx, statusCancel := context.WithTimeout(ctx, 3*time.Second)
		var pending, blocked *int64
		var oldest *time.Time
		if backlog != nil {
			pending = &backlog.Pending
			blocked = &backlog.Blocked
			oldest = backlog.OldestPending
		}
		statusErr := m.Store.RecordCommitRelayStatus(statusCtx, src, code, pending, blocked, oldest)
		statusCancel()
		if statusErr != nil {
			code = "status_write_failed"
		}
		if m.Report != nil {
			m.Report(src.ID, code, count)
		}
		m.cursor = src.ID
	}
	if len(sources) < 16 {
		m.cursor = ""
	}
	return nil
}

func (m *Manager) tickSource(ctx context.Context, src state.CommitRelaySource) (int, string, *commitwork.Backlog) {
	raw, err := commitwork.OpenConnection(m.Identities, src.ID, src.SealedConnection)
	if err != nil {
		return 0, "credential_unavailable", nil
	}
	open := m.Open
	if open == nil {
		open = commitwork.OpenPool
	}
	pool, err := open(ctx, raw, m.Policy)
	if err != nil {
		return 0, "database_unavailable", nil
	}
	defer pool.Close()
	if err := commitwork.QualifySchema(ctx, pool); err != nil {
		return 0, "schema_unqualified", nil
	}
	if err := commitwork.QualifySource(ctx, pool, src.ID); err != nil {
		return 0, "source_binding_unqualified", nil
	}
	relay := commitwork.Relay{SourceID: src.ID, Pool: pool, Acceptor: &acceptor{store: m.Store, source: src}}
	replays, replayErr := m.Store.PendingCommitReplays(ctx, src.AccountID, src.ID)
	if replayErr != nil {
		return 0, "replay_pending", nil
	}
	for _, request := range replays {
		if _, err := relay.ReplayBlocked(ctx, request.EventID); err != nil {
			return 0, "replay_pending", nil
		}
		if err := m.Store.CompleteCommitReplay(ctx, src.AccountID, src.ID, request.EventID, request.Generation); err != nil {
			return 0, "replay_pending", nil
		}
	}
	count, err := relay.Tick(ctx)
	retention := m.AcceptedRetention
	if retention == 0 {
		retention = 7 * 24 * time.Hour
	}
	if retention < 24*time.Hour || retention > 30*24*time.Hour {
		return count, "retention_unqualified", nil
	}
	if _, cleanupErr := relay.Cleanup(ctx, time.Now().UTC().Add(-retention), 32); cleanupErr != nil {
		return count, "cleanup_pending", nil
	}
	rows, blockedErr := pool.Query(ctx, `SELECT event_id::text,left(event_type,256),
 CASE WHEN blocked_code IN ('invalid_event','identity_conflict','destination_unqualified','payload_too_large','acceptance_rejected') THEN blocked_code ELSE 'blocked' END,created_at
 FROM public.gregale_outbox WHERE accepted_at IS NULL AND blocked_code IS NOT NULL ORDER BY created_at,event_id LIMIT 32`)
	if blockedErr != nil {
		return count, "blocked_scan_pending", nil
	}
	items := []state.CommitBlockedEvent{}
	for rows.Next() {
		var item state.CommitBlockedEvent
		if scanErr := rows.Scan(&item.EventID, &item.Type, &item.Code, &item.CreatedAt); scanErr != nil {
			rows.Close()
			return count, "blocked_scan_pending", nil
		}
		items = append(items, item)
	}
	blockedErr = rows.Err()
	rows.Close()
	if blockedErr != nil {
		return count, "blocked_scan_pending", nil
	}
	if err := m.Store.RecordCommitBlockedEvents(ctx, src, items); err != nil {
		return count, "blocked_status_pending", nil
	}
	backlog, backlogErr := relay.Backlog(ctx)
	var snapshot *commitwork.Backlog
	if backlogErr == nil {
		snapshot = &backlog
	}
	if err != nil {
		return count, "handoff_pending", snapshot
	}
	if backlogErr != nil {
		return count, "status_unavailable", nil
	}
	if backlog.Blocked > 0 {
		return count, "blocked_events", snapshot
	}
	return count, "healthy", snapshot
}

func (m *Manager) Run(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.Tick(ctx); err != nil && m.Report != nil {
			m.Report("", "source_scan_failed", 0)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type acceptor struct {
	store  Store
	source state.CommitRelaySource
}

func (a *acceptor) Accept(ctx context.Context, e commitwork.Event) (commitwork.Receipt, error) {
	if err := e.Validate(); err != nil {
		return commitwork.Receipt{}, &commitwork.PermanentError{Code: "invalid_event"}
	}
	// Recover receipt before current version/plan checks. Content comparison is
	// always performed by the durable store, even when the destination changed.
	if _, err := a.store.CommitReceiptByEvent(ctx, a.source.AccountID, a.source.ID, e.ID); err == nil {
		r, err := a.store.AcceptCommitEvent(ctx, a.source.AccountID, a.source.ID, e.ID, e.Type, e.Data, state.Invocation{}, 0)
		return convert(r, err)
	} else if !errors.Is(err, state.ErrNotFound) {
		return commitwork.Receipt{}, err
	}
	app, err := a.store.AppByID(ctx, a.source.AppID)
	if err != nil {
		return commitwork.Receipt{}, err
	}
	if app.AccountID != a.source.AccountID || app.PlatformTenantRequired || !app.AcceptsRequestInvocations() {
		return commitwork.Receipt{}, &commitwork.PermanentError{Code: "destination_unqualified"}
	}
	account, err := a.store.AccountByID(ctx, a.source.AccountID)
	if err != nil {
		return commitwork.Receipt{}, err
	}
	limits := api.MustLimitsFor(account.Plan)
	if limits.MaxQueueDepth == 0 {
		return commitwork.Receipt{}, errors.New("commit: plan unavailable")
	}
	if int64(len(e.Data)) > int64(limits.MaxSourceBytesPerInvocation) {
		return commitwork.Receipt{}, &commitwork.PermanentError{Code: "payload_too_large"}
	}
	envelope, err := (events.Envelope{ID: e.ID, Source: "gregale.commit." + a.source.ID, Type: e.Type, Data: e.Data}).Normalize(account.ID, time.Now().UTC())
	if err != nil {
		return commitwork.Receipt{}, &commitwork.PermanentError{Code: "invalid_event"}
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return commitwork.Receipt{}, err
	}
	policy := app.RetryPolicy()
	policy.MaxAttempts = api.EffectiveRetryMaxAttempts(policy.MaxAttempts, limits.MaxQueueAttempts)
	retryPolicy, err := json.Marshal(policy)
	if err != nil {
		return commitwork.Receipt{}, err
	}
	inv := state.Invocation{AccountID: account.ID, AppID: app.ID, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/", Payload: payload, DueAt: time.Now().UTC(), RetryPolicyJSON: retryPolicy}
	inv, _, err = state.ResolveInvocationVersion(ctx, a.store, inv)
	if err != nil {
		return commitwork.Receipt{}, err
	}
	r, err := a.store.AcceptCommitEvent(ctx, account.ID, a.source.ID, e.ID, e.Type, e.Data, inv, limits.MaxQueueDepth)
	return convert(r, err)
}
func convert(r state.CommitReceipt, err error) (commitwork.Receipt, error) {
	if errors.Is(err, state.ErrConflict) {
		err = &commitwork.PermanentError{Code: "identity_conflict"}
	}
	return commitwork.Receipt{ID: r.ID, InvocationID: r.InvocationID}, err
}
