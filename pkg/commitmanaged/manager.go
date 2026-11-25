// Package commitmanaged owns the platform-side customer outbox relay lifecycle.
package commitmanaged

import (
	"context"
	"encoding/json"
	"errors"
	"hash/crc32"
	"sync"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	FastReservedConnectBudget         = 750 * time.Millisecond
	DefaultAdaptiveReservedConnectMax = 2 * time.Second

	relayWorkers          = 4
	relayScanInterval     = time.Second
	relayPollTimeout      = 15 * time.Second
	relayStoreTimeout     = 3 * time.Second
	relayMinBackoff       = 5 * time.Second
	relayMaxBackoff       = 5 * time.Minute
	relayTransportCeiling = time.Minute
	relayTransportSpread  = 5 * time.Second
	relayReplayBudget     = 2 * time.Second
	relayReplayStep       = 500 * time.Millisecond
	relayReplayRetry      = 5 * time.Second
	relayDeliveryReserve  = 5 * time.Second
	relayCleanupBudget    = time.Second
	relayCleanupBatch     = 128
	relayCleanupBatches   = 8
)

type Store interface {
	state.CommitStore
	ClaimCommitRelaySources(context.Context, int, time.Duration) ([]state.CommitRelaySource, error)
	ClaimHealthyCommitRelaySources(context.Context, int, time.Duration) ([]state.CommitRelaySource, error)
	DeferCommitRelaySource(context.Context, state.CommitRelaySource) (bool, error)
	AppByID(context.Context, string) (state.App, error)
	AccountByID(context.Context, string) (state.Account, error)
	RecordCommitRelayPoll(context.Context, state.CommitRelaySource, string, *int64, *int64, *time.Time, time.Duration, state.CommitRelayPollObservation) error
	RecordCommitBlockedEvents(context.Context, state.CommitRelaySource, []state.CommitBlockedEvent) error
	PendingCommitReplays(context.Context, string, string) ([]state.CommitReplay, error)
	RecordCommitReplayAttempt(context.Context, state.CommitRelaySource, state.CommitReplay, time.Duration) error
	// AcceptCommitChange enqueues a changes source's captured row change into
	// its app's named queue (ADR-20261112140000), in its row's order when
	// work is set (ADR-20261112193306).
	AcceptCommitChange(context.Context, string, string, string, string, json.RawMessage, state.Invocation, *state.CommitChangeWork) (state.CommitReceipt, error)
	// Changes order each row's changes under the built-in ordered policy.
	state.AppWorkPolicyStore
}

type Manager struct {
	Store             Store
	Identities        []*age.X25519Identity
	Policy            commitwork.NetworkPolicy
	AcceptedRetention time.Duration
	RealtimeEnabled   bool
	// ReserveHealthyWorker keeps one Run worker available for recently healthy sources.
	ReserveHealthyWorker bool
	// ReservedConnectBudget limits only the initial connection on the reserved worker.
	ReservedConnectBudget time.Duration
	// AdaptiveReservedConnectMax caps the deadline for recently observed slow
	// connections. Zero disables adaptive deadlines.
	AdaptiveReservedConnectMax time.Duration
	// PublicDatabases lets changes sources reach customer databases at any
	// globally reachable address (FAAS_COMMIT_PUBLIC_DATABASES,
	// ADR-20261112140000 §6). Other sources keep the operator allowlist.
	PublicDatabases bool
	// Open is injectable for isolated acceptance environments. Production uses
	// the policy-enforcing TLS dialer. Each pool is closed after its bounded tick.
	Open func(context.Context, string, commitwork.NetworkPolicy) (*pgxpool.Pool, error)
	// Report may be called concurrently by polling workers.
	Report        func(string, string, int)
	ReportCleanup func(int64, time.Duration, string)
}

func (m *Manager) claimSources(ctx context.Context, limit int, healthyOnly bool) ([]state.CommitRelaySource, error) {
	claimCtx, cancel := context.WithTimeout(ctx, relayStoreTimeout)
	defer cancel()
	if healthyOnly {
		return m.Store.ClaimHealthyCommitRelaySources(claimCtx, limit, state.CommitRelaySourceLease)
	}
	return m.Store.ClaimCommitRelaySources(claimCtx, limit, state.CommitRelaySourceLease)
}

func (m *Manager) validate() error {
	if m.Store == nil || len(m.Identities) == 0 {
		return errors.New("commit: managed relay configuration missing")
	}
	return nil
}

// Tick processes one bounded batch. Run keeps each worker polling independently.
func (m *Manager) Tick(ctx context.Context) error {
	if err := m.validate(); err != nil {
		return err
	}
	sources, err := m.claimSources(ctx, relayWorkers, false)
	if err != nil {
		return err
	}
	var workers sync.WaitGroup
	for _, src := range sources {
		workers.Go(func() { m.pollSource(ctx, src, 0) })
	}
	workers.Wait()
	return ctx.Err()
}

func (m *Manager) pollSource(ctx context.Context, src state.CommitRelaySource, connectBudget time.Duration) {
	sourceCtx, cancel := context.WithTimeout(ctx, relayPollTimeout)
	result := m.tickSource(sourceCtx, src, connectBudget)
	backoff := sourceBackoff(src, result.status, errors.Is(sourceCtx.Err(), context.DeadlineExceeded), result.transportUnavailable)
	cancel()

	statusCtx, statusCancel := context.WithTimeout(ctx, relayStoreTimeout)
	defer statusCancel()
	if result.deferToGeneral {
		deferred, err := m.Store.DeferCommitRelaySource(statusCtx, src)
		if m.Report != nil {
			if err != nil {
				m.Report(src.ID, "status_write_failed", 0)
			} else if deferred {
				m.Report(src.ID, "reserved_connect_deferred", 0)
			}
		}
		return
	}
	var pending, blocked *int64
	var oldest *time.Time
	var cleanupEligible *int64
	if result.backlog != nil {
		pending, blocked, oldest = &result.backlog.Pending, &result.backlog.Blocked, result.backlog.OldestPending
		cleanupEligible = result.backlog.CleanupEligible
	}
	observation := state.CommitRelayPollObservation{CleanupEligible: cleanupEligible}
	if m.AdaptiveReservedConnectMax > 0 && result.status == "healthy" {
		milliseconds := int32(result.connectDuration.Milliseconds())
		observation.ConnectMS = &milliseconds
	}
	if err := m.Store.RecordCommitRelayPoll(statusCtx, src, result.status, pending, blocked, oldest, backoff, observation); err != nil {
		result.status = "status_write_failed"
	}
	if m.Report != nil {
		m.Report(src.ID, result.status, result.accepted)
	}
}

func sourceBackoff(src state.CommitRelaySource, status string, timedOut, transportUnavailable bool) time.Duration {
	switch status {
	case "database_unavailable", "credential_unavailable", "schema_unqualified", "source_binding_unqualified":
	default:
		if !timedOut {
			return 0
		}
	}
	ceiling := relayMaxBackoff
	if status == "database_unavailable" && transportUnavailable {
		// Spread capped retries across sources, with a stable ceiling across
		// scheduler replicas taking ownership of the same source.
		offset := int64(crc32.ChecksumIEEE([]byte(src.ID))) % (relayTransportSpread.Milliseconds() + 1)
		ceiling = relayTransportCeiling - time.Duration(offset)*time.Millisecond
	}
	return min(ceiling, max(relayMinBackoff, 2*time.Duration(src.PollBackoffMS)*time.Millisecond))
}

// processReplays gives replay a share of the source deadline. A failed request
// stays pending while the remaining requests and the normal delivery pass run.
func (m *Manager) processReplays(ctx context.Context, relay *commitwork.Relay, src state.CommitRelaySource) (pending bool) {
	budget := relayReplayBudget
	if deadline, ok := ctx.Deadline(); ok {
		budget = min(budget, time.Until(deadline)-relayDeliveryReserve)
	}
	if budget <= 0 {
		return false
	}
	replayCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	requests, err := m.Store.PendingCommitReplays(replayCtx, src.AccountID, src.ID)
	if err != nil {
		return true
	}
	for _, request := range requests {
		if replayCtx.Err() != nil {
			return true
		}
		attemptCtx, cancelAttempt := context.WithTimeout(replayCtx, relayReplayStep)
		_, attemptErr := relay.ReplayBlocked(attemptCtx, request.EventID)
		cancelAttempt()
		var delay time.Duration
		if attemptErr != nil {
			delay, pending = relayReplayRetry, true
		}
		recordCtx, cancelRecord := context.WithTimeout(replayCtx, relayReplayStep)
		err := m.Store.RecordCommitReplayAttempt(recordCtx, src, request, delay)
		cancelRecord()
		pending = pending || err != nil
	}
	return pending
}

type relayPollResult struct {
	accepted             int
	status               string
	backlog              *commitwork.Backlog
	transportUnavailable bool
	deferToGeneral       bool
	connectDuration      time.Duration
}

func qualificationPollFailure(err error, status string) relayPollResult {
	if errors.Is(err, commitwork.ErrConnectionTransport) {
		return relayPollResult{status: "database_unavailable", transportUnavailable: true}
	}
	return relayPollResult{status: status}
}

func (m *Manager) tickSource(ctx context.Context, src state.CommitRelaySource, connectBudget time.Duration) relayPollResult {
	raw, err := commitwork.OpenConnection(m.Identities, src.ID, src.SealedConnection)
	if err != nil {
		return relayPollResult{status: "credential_unavailable"}
	}
	open := m.Open
	if open == nil {
		open = commitwork.OpenPool
	}
	policy := m.Policy
	policy.PublicInternet = m.PublicDatabases && src.Changes.IsChanges()
	connectCtx := ctx
	cancel := func() {}
	if connectBudget > 0 {
		connectCtx, cancel = context.WithTimeout(ctx, connectBudget)
	}
	connectStarted := time.Now()
	pool, err := open(connectCtx, raw, policy)
	connectDuration := time.Since(connectStarted)
	budgetExpired := connectBudget > 0 && errors.Is(connectCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
	cancel()
	if err != nil {
		if budgetExpired {
			return relayPollResult{deferToGeneral: true}
		}
		return relayPollResult{status: "database_unavailable", transportUnavailable: errors.Is(err, commitwork.ErrConnectionTransport)}
	}
	defer pool.Close()
	if err := commitwork.QualifySchemaVersion(ctx, pool, src.ContractVersion); err != nil {
		return qualificationPollFailure(err, "schema_unqualified")
	}
	if err := commitwork.QualifySource(ctx, pool, src.ID); err != nil {
		return qualificationPollFailure(err, "source_binding_unqualified")
	}
	relay := commitwork.Relay{ContractVersion: src.ContractVersion, SourceID: src.ID, Pool: pool, Acceptor: &acceptor{store: m.Store, source: src}}
	replayPending := m.processReplays(ctx, &relay, src)
	count, err := relay.Tick(ctx)
	retention := m.AcceptedRetention
	if retention == 0 {
		retention = 7 * 24 * time.Hour
	}
	if retention < 24*time.Hour || retention > 30*24*time.Hour {
		return relayPollResult{accepted: count, status: "retention_unqualified"}
	}
	cleanupCtx, cancelCleanup := context.WithTimeout(ctx, relayCleanupBudget)
	cleanupStarted := time.Now()
	var totalRemoved int64
	cleanupResult := "complete"
	var cleanupErr error
	cutoff := time.Now().UTC().Add(-retention)
	for range relayCleanupBatches {
		var removed int64
		removed, cleanupErr = relay.Cleanup(cleanupCtx, cutoff, relayCleanupBatch)
		totalRemoved += removed
		if cleanupErr != nil || removed < relayCleanupBatch {
			cleanupResult = "complete"
			break
		}
		cleanupResult = "batch_limit"
	}
	// A spent cleanup budget leaves more work for the next poll. A parent
	// cancellation still invalidates the source pass.
	if errors.Is(cleanupErr, context.DeadlineExceeded) && ctx.Err() == nil {
		cleanupResult = "time_limit"
		cleanupErr = nil
	} else if cleanupErr != nil {
		cleanupResult = "error"
	}
	cancelCleanup()
	if m.ReportCleanup != nil {
		m.ReportCleanup(totalRemoved, time.Since(cleanupStarted), cleanupResult)
	}
	var realtimeBacklog *commitwork.Backlog
	if m.RealtimeEnabled {
		n, observed, realtimeErr := m.tickRealtime(ctx, pool, src)
		count += n
		realtimeBacklog = observed
		err = errors.Join(err, realtimeErr)
		if realtimeErr != nil && observed == nil {
			return relayPollResult{accepted: count, status: "status_unavailable"}
		}
	}
	rows, blockedErr := pool.Query(ctx, `SELECT event_id::text,left(event_type,256),
	 CASE WHEN blocked_code IN ('invalid_event','invalid_routing','identity_conflict','destination_unqualified','payload_too_large','acceptance_rejected','event_schema_rejected') THEN blocked_code ELSE 'blocked' END,created_at
 FROM public.gregale_outbox WHERE accepted_at IS NULL AND blocked_code IS NOT NULL ORDER BY created_at,event_id LIMIT 32`)
	if blockedErr != nil {
		return relayPollResult{accepted: count, status: "blocked_scan_pending"}
	}
	items := []state.CommitBlockedEvent{}
	for rows.Next() {
		var item state.CommitBlockedEvent
		if scanErr := rows.Scan(&item.EventID, &item.Type, &item.Code, &item.CreatedAt); scanErr != nil {
			rows.Close()
			return relayPollResult{accepted: count, status: "blocked_scan_pending"}
		}
		items = append(items, item)
	}
	blockedErr = rows.Err()
	rows.Close()
	if blockedErr != nil {
		return relayPollResult{accepted: count, status: "blocked_scan_pending"}
	}
	if err := m.Store.RecordCommitBlockedEvents(ctx, src, items); err != nil {
		return relayPollResult{accepted: count, status: "blocked_status_pending"}
	}
	backlog, backlogErr := relay.Backlog(ctx, cutoff)
	if realtimeBacklog != nil {
		backlog.Pending += realtimeBacklog.Pending
		backlog.Blocked += realtimeBacklog.Blocked
		if realtimeBacklog.OldestPending != nil && (backlog.OldestPending == nil || realtimeBacklog.OldestPending.Before(*backlog.OldestPending)) {
			backlog.OldestPending = realtimeBacklog.OldestPending
		}
	}
	var snapshot *commitwork.Backlog
	if backlogErr == nil {
		snapshot = &backlog
	}
	if err != nil {
		return relayPollResult{accepted: count, status: "handoff_pending", backlog: snapshot}
	}
	if backlogErr != nil {
		return relayPollResult{accepted: count, status: "status_unavailable"}
	}
	if cleanupErr != nil {
		return relayPollResult{accepted: count, status: "cleanup_pending", backlog: snapshot}
	}
	if replayPending {
		return relayPollResult{accepted: count, status: "replay_pending", backlog: snapshot}
	}
	if backlog.Blocked > 0 {
		return relayPollResult{accepted: count, status: "blocked_events", backlog: snapshot}
	}
	return relayPollResult{accepted: count, status: "healthy", backlog: snapshot, connectDuration: connectDuration}
}

func (m *Manager) Run(ctx context.Context) error {
	if err := m.validate(); err != nil {
		return err
	}
	var workers sync.WaitGroup
	for i := range relayWorkers {
		workers.Go(func() { m.runWorker(ctx, m.ReserveHealthyWorker && i == 0) })
	}
	workers.Wait()
	return ctx.Err()
}

func (m *Manager) runWorker(ctx context.Context, healthyOnly bool) {
	ticker := time.NewTicker(relayScanInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		sources, err := m.claimSources(ctx, 1, healthyOnly)
		if err == nil && len(sources) > 0 {
			var connectBudget time.Duration
			if healthyOnly {
				connectBudget = m.reservedConnectDeadline(sources[0].LastConnectMS)
			}
			m.pollSource(ctx, sources[0], connectBudget)
			continue
		}
		if err != nil && ctx.Err() == nil && m.Report != nil {
			m.Report("", "source_scan_failed", 0)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Manager) reservedConnectDeadline(observedMS int32) time.Duration {
	budget := m.ReservedConnectBudget
	if budget <= 0 || m.AdaptiveReservedConnectMax == 0 {
		return budget
	}
	if observed := time.Duration(observedMS) * time.Millisecond; observed > budget {
		budget = observed + 250*time.Millisecond
	}
	return min(m.AdaptiveReservedConnectMax, budget)
}

type acceptor struct {
	store  Store
	source state.CommitRelaySource
}

func (a *acceptor) Accept(ctx context.Context, e commitwork.Event) (commitwork.Receipt, error) {
	if err := e.Validate(); err != nil {
		return commitwork.Receipt{}, &commitwork.PermanentError{Code: "invalid_event"}
	}
	if a.source.Destination == "event" {
		publisher, ok := a.store.(state.CommitPublicationStore)
		if !ok {
			return commitwork.Receipt{}, errors.New("commit: publication store unavailable")
		}
		r, err := publisher.AcceptCommitPublication(ctx, a.source.AccountID, a.source.ID, e)
		return convert(r, err)
	}
	if e.SchemaVersion != "" {
		return commitwork.Receipt{}, &commitwork.PermanentError{Code: "invalid_event"}
	}
	if a.source.Changes.IsChanges() {
		return a.acceptChange(ctx, e)
	}
	// Recover receipt before current version/plan checks. Content comparison is
	// always performed by the durable store, even when the destination changed.
	if _, err := a.store.CommitReceiptByEvent(ctx, a.source.AccountID, a.source.ID, e.ID); err == nil {
		r, err := a.store.AcceptCommitEvent(ctx, a.source.AccountID, a.source.ID, e.ID, e.Type, e.Data, state.Invocation{}, 0, e.Routing)
		return convert(r, err)
	} else if !errors.Is(err, state.ErrNotFound) {
		return commitwork.Receipt{}, err
	}
	app, err := a.store.AppByID(ctx, a.source.AppID)
	if err != nil {
		return commitwork.Receipt{}, err
	}
	if app.AccountID != a.source.AccountID || (app.PlatformTenantRequired && !a.source.AllowTenantSelection) || !app.AcceptsRequestInvocations() {
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
	if e.Routing != nil {
		inv.PlatformTenantID = e.Routing.PlatformTenantID
	}
	inv, _, err = state.ResolveInvocationVersion(ctx, a.store, inv)
	if err != nil {
		return commitwork.Receipt{}, err
	}
	r, err := a.store.AcceptCommitEvent(ctx, account.ID, a.source.ID, e.ID, e.Type, e.Data, inv, limits.MaxQueueDepth, e.Routing)
	return convert(r, err)
}
func convert(r state.CommitReceipt, err error) (commitwork.Receipt, error) {
	if errors.Is(err, state.ErrConflict) || errors.Is(err, exclusivework.ErrIdentityConflict) {
		err = &commitwork.PermanentError{Code: "identity_conflict"}
	}
	if errors.Is(err, state.ErrEventSchemaRequired) || errors.Is(err, state.ErrEventSchemaUnknown) || errors.Is(err, state.ErrEventDataInvalid) {
		err = &commitwork.PermanentError{Code: "event_schema_rejected"}
	}
	if errors.Is(err, state.ErrInvalidArgument) || errors.Is(err, state.ErrNotFound) {
		err = &commitwork.PermanentError{Code: "destination_unqualified"}
	}
	return commitwork.Receipt{ID: r.ID, InvocationID: r.InvocationID, OperationID: r.OperationID, Publication: r.Publication}, err
}
