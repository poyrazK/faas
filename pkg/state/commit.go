package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrCommitQueueFull = errors.New("state: commit queue full")

type CommitSource struct {
	Destination          string     `json:"destination"`
	ID                   string     `json:"id"`
	AccountID            string     `json:"-"`
	AppID                string     `json:"app_id"`
	Name                 string     `json:"name"`
	Enabled              bool       `json:"enabled"`
	OperationPolicy      string     `json:"operation_policy,omitempty"`
	ContractVersion      int        `json:"contract_version"`
	AllowTenantSelection bool       `json:"allow_tenant_selection"`
	RelayStatus          string     `json:"relay_status,omitempty"`
	LastCheckedAt        *time.Time `json:"last_checked_at,omitempty"`
	PollIntervalMS       int32      `json:"poll_interval_ms,omitempty"`
	PendingEvents        *int64     `json:"pending_events,omitempty"`
	BlockedEvents        *int64     `json:"blocked_events,omitempty"`
	OldestPendingAt      *time.Time `json:"oldest_pending_at,omitempty"`
}
type CommitReceipt struct {
	ID           string                      `json:"receipt_id"`
	SourceID     string                      `json:"source_id"`
	EventID      string                      `json:"event_id"`
	InvocationID string                      `json:"invocation_id,omitempty"`
	OperationID  string                      `json:"operation_id,omitempty"`
	AcceptedAt   time.Time                   `json:"accepted_at"`
	OperationURL string                      `json:"operation_url,omitempty"`
	Publication  *api.CommitEventPublication `json:"publication,omitempty"`
}

type CommitOperation struct {
	ID          string     `json:"id"`
	ReceiptID   string     `json:"receipt_id"`
	SourceID    string     `json:"source_id"`
	EventID     string     `json:"event_id"`
	State       string     `json:"state"`
	AcceptedAt  time.Time  `json:"accepted_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

func (s *PgStore) CommitOperationByID(ctx context.Context, account, id string) (CommitOperation, error) {
	row, err := sqlc.New().CommitOperationHistory(ctx, s.pool, sqlc.CommitOperationHistoryParams{AccountID: account, OperationID: id})
	return CommitOperation{ID: row.OperationID, ReceiptID: row.ReceiptID, SourceID: row.SourceID,
		EventID: row.EventID, State: row.OperationState, AcceptedAt: row.AcceptedAt.Time,
		CompletedAt: exclusiveTime(row.CompletedAt)}, mapErr(err)
}

// CommitRelaySourceLease is the time granted to a scheduler to finish its poll.
const CommitRelaySourceLease = 30 * time.Second

// CommitRelaySource is an internal source lease. The credential and lease token
// never appear in the public source response.
type CommitRelaySource struct {
	CommitSource
	SealedConnection   []byte
	CredentialRevision int64
	PollToken          string
	PollBackoffMS      int
	LastConnectMS      int32
	Changes            CommitChangesConfig
	pollDueAt          pgtype.Timestamptz
}

type CommitBlockedEvent struct {
	EventID    string    `json:"event_id"`
	Type       string    `json:"type"`
	Code       string    `json:"blocked_code"`
	CreatedAt  time.Time `json:"created_at"`
	ObservedAt time.Time `json:"observed_at"`
}

func lockCurrentCommitRelaySource(ctx context.Context, tx pgx.Tx, src CommitRelaySource) (bool, error) {
	var current bool
	err := tx.QueryRow(ctx, `SELECT true FROM commit_sources WHERE account_id=$1::uuid AND id=$2::uuid AND enabled
 AND credential_revision=$3 AND relay_poll_token=$4::uuid AND relay_next_poll_at>clock_timestamp() FOR UPDATE`,
		src.AccountID, src.ID, src.CredentialRevision, src.PollToken).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return current, err
}

func (s *PgStore) ListCommitBlockedEvents(ctx context.Context, account, source string) ([]CommitBlockedEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT event_id::text,event_type,blocked_code,created_at,observed_at FROM commit_blocked_events
 WHERE account_id=$1::uuid AND source_id=$2::uuid ORDER BY created_at,event_id LIMIT 32`, account, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CommitBlockedEvent{}
	for rows.Next() {
		var e CommitBlockedEvent
		if err := rows.Scan(&e.EventID, &e.Type, &e.Code, &e.CreatedAt, &e.ObservedAt); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
func (s *PgStore) RecordCommitBlockedEvents(ctx context.Context, src CommitRelaySource, items []CommitBlockedEvent) error {
	if len(items) > 32 {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := lockCurrentCommitRelaySource(ctx, tx, src)
	if err != nil {
		return mapErr(err)
	}
	if !current {
		return nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM commit_blocked_events WHERE source_id=$1::uuid`, src.ID); err != nil {
		return err
	}
	for _, item := range items {
		if _, err := tx.Exec(ctx, `INSERT INTO commit_blocked_events(account_id,source_id,event_id,event_type,blocked_code,created_at)
   VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6)`, src.AccountID, src.ID, item.EventID, item.Type, item.Code, item.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *PgStore) RequestCommitReplay(ctx context.Context, account, source, event string) error {
	tag, err := s.pool.Exec(ctx, `INSERT INTO commit_replay_requests(account_id,source_id,event_id)
 SELECT account_id,source_id,event_id FROM commit_blocked_events WHERE account_id=$1::uuid AND source_id=$2::uuid AND event_id=$3::uuid
 ON CONFLICT(source_id,event_id) DO UPDATE SET state='pending',requested_at=now(),next_attempt_at=now(),generation=commit_replay_requests.generation+1`, account, source, event)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type CommitReplay struct {
	EventID    string
	Generation int64
}

func (s *PgStore) PendingCommitReplays(ctx context.Context, account, source string) ([]CommitReplay, error) {
	rows, err := s.pool.Query(ctx, `SELECT event_id::text,generation FROM commit_replay_requests
 WHERE account_id=$1::uuid AND source_id=$2::uuid AND state='pending' AND next_attempt_at<=statement_timestamp()
 ORDER BY next_attempt_at,requested_at,event_id LIMIT 32`, account, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []CommitReplay
	for rows.Next() {
		var id CommitReplay
		if err := rows.Scan(&id.EventID, &id.Generation); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// RecordCommitReplayAttempt completes a successful or already-applied replay.
// A positive delay leaves a failed replay pending and schedules its next attempt.
func (s *PgStore) RecordCommitReplayAttempt(ctx context.Context, src CommitRelaySource, request CommitReplay, retryDelay time.Duration) error {
	_, err := s.pool.Exec(ctx, `WITH current_source AS (
 SELECT id FROM commit_sources WHERE account_id=$1::uuid AND id=$2::uuid AND enabled
 AND credential_revision=$3 AND relay_poll_token=$4::uuid AND relay_next_poll_at>clock_timestamp() FOR UPDATE
 ) UPDATE commit_replay_requests r
 SET state=CASE WHEN $7::bigint=0 THEN 'completed' ELSE 'pending' END,
 next_attempt_at=CASE WHEN $7::bigint=0 THEN r.next_attempt_at ELSE clock_timestamp()+$7::bigint*interval '1 millisecond' END
 FROM current_source WHERE r.source_id=current_source.id AND r.account_id=$1::uuid
 AND r.event_id=$5::uuid AND r.generation=$6 AND r.state='pending'`,
		src.AccountID, src.ID, src.CredentialRevision, src.PollToken, request.EventID, request.Generation, retryDelay.Milliseconds())
	return err
}

// ClaimCommitRelaySources moves each due source's next eligible time to its
// lease deadline. Finishing a pass replaces it with the next scheduled poll.
// The last-checked guard honors the configured cadence for existing sources
// and observations written by older schedulers during a rolling upgrade.
func (s *PgStore) ClaimCommitRelaySources(ctx context.Context, limit int, lease time.Duration) ([]CommitRelaySource, error) {
	return s.claimCommitRelaySources(ctx, limit, lease, false)
}

// ClaimHealthyCommitRelaySources leases due sources with a fresh healthy poll.
func (s *PgStore) ClaimHealthyCommitRelaySources(ctx context.Context, limit int, lease time.Duration) ([]CommitRelaySource, error) {
	return s.claimCommitRelaySources(ctx, limit, lease, true)
}

func (s *PgStore) claimCommitRelaySources(ctx context.Context, limit int, lease time.Duration, healthyOnly bool) ([]CommitRelaySource, error) {
	if limit < 1 || limit > 64 || lease < time.Millisecond {
		return nil, ErrInvalidArgument
	}
	query := `WITH due AS (
 SELECT id,relay_next_poll_at FROM commit_sources WHERE enabled AND sealed_connection IS NOT NULL
 AND relay_next_poll_at <= statement_timestamp()
 AND EXISTS (SELECT 1 FROM apps a WHERE a.id = commit_sources.app_id AND a.status <> 'deleted')
 AND (last_checked_at IS NULL OR last_checked_at <= statement_timestamp() - poll_interval_ms * interval '1 millisecond')
 AND (NOT $3::boolean OR (relay_status = 'healthy'
 AND last_checked_at IS DISTINCT FROM relay_reserved_deferred_check_at
 AND last_checked_at >= statement_timestamp() - (poll_interval_ms + $4::bigint) * interval '1 millisecond'))
 ORDER BY relay_next_poll_at,id LIMIT $1 FOR UPDATE SKIP LOCKED
 ) UPDATE commit_sources s
 SET relay_next_poll_at=statement_timestamp()+$2::bigint*interval '1 millisecond',relay_poll_token=gen_random_uuid()
 FROM due WHERE s.id=due.id
 RETURNING s.id::text,s.account_id::text,s.app_id::text,s.name,s.enabled,COALESCE(s.operation_policy,''),s.contract_version,
 s.allow_tenant_selection,s.destination,s.sealed_connection,s.credential_revision,s.relay_poll_token::text,s.relay_poll_backoff_ms,
 COALESCE(s.queue_name,''),s.changes_feeds,COALESCE(s.managed_database_id::text,''),s.poll_interval_ms,s.last_checked_at,due.relay_next_poll_at,
 CASE WHEN s.relay_connect_observed_at=s.last_checked_at THEN COALESCE(s.relay_connect_ms,0) ELSE 0 END`
	if healthyOnly {
		query = "/* commit relay reserved poll */ " + query
	}
	rows, err := s.pool.Query(ctx, query,
		limit, lease.Milliseconds(), healthyOnly, commitwork.ObservationGracePeriod.Milliseconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []CommitRelaySource
	for rows.Next() {
		var src CommitRelaySource
		var feeds []byte
		if err := rows.Scan(&src.ID, &src.AccountID, &src.AppID, &src.Name, &src.Enabled, &src.OperationPolicy, &src.ContractVersion, &src.AllowTenantSelection, &src.Destination, &src.SealedConnection, &src.CredentialRevision,
			&src.PollToken, &src.PollBackoffMS,
			&src.Changes.QueueName, &feeds, &src.Changes.ManagedDatabaseID, &src.Changes.PollIntervalMS, &src.LastCheckedAt, &src.pollDueAt, &src.LastConnectMS); err != nil {
			return nil, err
		}
		if len(feeds) > 0 {
			src.Changes.Feeds = json.RawMessage(feeds)
		}
		sources = append(sources, src)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sources, nil
}

// DeferCommitRelaySource releases the current lease at its original due time.
// General workers can claim it immediately. A completed poll updates
// last_checked_at so the reserved worker can consider the new observation.
func (s *PgStore) DeferCommitRelaySource(ctx context.Context, src CommitRelaySource) (bool, error) {
	tag, err := s.pool.Exec(ctx, `/* commit relay general handoff */
 UPDATE commit_sources SET relay_next_poll_at=$5,relay_poll_token=NULL,relay_reserved_deferred_check_at=last_checked_at
 WHERE account_id=$1::uuid AND id=$2::uuid AND credential_revision=$3 AND enabled
 AND relay_poll_token=$4::uuid AND relay_next_poll_at>clock_timestamp()`,
		src.AccountID, src.ID, src.CredentialRevision, src.PollToken, src.pollDueAt)
	return tag.RowsAffected() == 1, err
}

type CommitStore interface {
	CreateCommitSource(context.Context, CommitSource) (CommitSource, error)
	CommitSourceByID(context.Context, string, string) (CommitSource, error)
	SetCommitSourceEnabled(context.Context, string, string, bool) (CommitSource, error)
	SetCommitSourceConnection(context.Context, string, string, []byte) error
	AcceptCommitEvent(context.Context, string, string, string, string, json.RawMessage, Invocation, int, ...*api.CommitRouting) (CommitReceipt, error)
	AcceptCommitOperation(context.Context, string, string, string, string, json.RawMessage, Invocation, ...*api.CommitRouting) (CommitReceipt, error)
	CommitReceiptByEvent(context.Context, string, string, string) (CommitReceipt, error)
	ListCommitBlockedEvents(context.Context, string, string) ([]CommitBlockedEvent, error)
	RequestCommitReplay(context.Context, string, string, string) error
	CommitOperationByID(context.Context, string, string) (CommitOperation, error)
}

func (s *PgStore) SetCommitSourceConnection(ctx context.Context, account, id string, blob []byte) error {
	if len(blob) == 0 || len(blob) > 16384 {
		return ErrInvalidArgument
	}
	rows, err := sqlc.New().SetCommitSourceConnection(ctx, s.pool, sqlc.SetCommitSourceConnectionParams{AccountID: account, SourceID: id, Connection: blob})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCommitSourceEnabled takes the same row lock as acceptance. Once pause
// returns, no new event can be accepted; existing receipts remain recoverable.
func (s *PgStore) SetCommitSourceEnabled(ctx context.Context, account, id string, enabled bool) (CommitSource, error) {
	return s.setCommitSourceEnabled(ctx, account, id, enabled)
}

func (s *PgStore) CreateCommitSource(ctx context.Context, src CommitSource) (CommitSource, error) {
	if src.Destination == "" {
		src.Destination = "operation"
	}
	if src.Destination == "event" {
		if src.ContractVersion != 3 || src.AllowTenantSelection || src.OperationPolicy != "" {
			return src, ErrInvalidArgument
		}
		return s.createCommitPublicationSource(ctx, src)
	}
	if src.Destination != "operation" {
		return src, ErrInvalidArgument
	}
	if src.ContractVersion == 0 {
		src.ContractVersion = 1
	}
	if (src.ContractVersion != 1 && src.ContractVersion != 2) || (src.AllowTenantSelection && src.ContractVersion != 2) || (src.ContractVersion == 2 && src.OperationPolicy == "") {
		return src, ErrInvalidArgument
	}
	if src.OperationPolicy != "" {
		return s.CreateManagedCommitSource(ctx, src)
	}
	err := s.pool.QueryRow(ctx, `INSERT INTO commit_sources(account_id,app_id,name)
 SELECT $1::uuid,id,$3 FROM apps WHERE id=$2::uuid AND account_id=$1::uuid
 ON CONFLICT(account_id,name) DO UPDATE SET name=commit_sources.name
 WHERE commit_sources.app_id=excluded.app_id AND commit_sources.operation_policy IS NULL AND commit_sources.destination='operation'
 RETURNING id::text,enabled`, src.AccountID, src.AppID, src.Name).Scan(&src.ID, &src.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		var conflict bool
		lookupErr := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM commit_sources WHERE account_id=$1::uuid AND name=$2 AND (app_id<>$3::uuid OR operation_policy IS NOT NULL))`, src.AccountID, src.Name, src.AppID).Scan(&conflict)
		if lookupErr != nil {
			return src, lookupErr
		}
		if conflict {
			return src, ErrConflict
		}
	}
	return src, mapErr(err)
}
func (s *PgStore) CommitSourceByID(ctx context.Context, account, id string) (CommitSource, error) {
	row, err := sqlc.New().CommitSourceIdentity(ctx, s.pool, sqlc.CommitSourceIdentityParams{AccountID: account, SourceID: id})
	src := CommitSource{ID: row.ID, AccountID: account, AppID: row.AppID, Name: row.Name, Destination: row.Destination,
		Enabled: row.Enabled, OperationPolicy: row.OperationPolicy, ContractVersion: int(row.ContractVersion), AllowTenantSelection: row.AllowTenantSelection, RelayStatus: row.RelayStatus,
		LastCheckedAt: exclusiveTime(row.LastCheckedAt), OldestPendingAt: exclusiveTime(row.OldestPendingAt), PollIntervalMS: row.PollIntervalMs}
	if row.PendingEvents.Valid {
		src.PendingEvents = &row.PendingEvents.Int64
	}
	if row.BlockedEvents.Valid {
		src.BlockedEvents = &row.BlockedEvents.Int64
	}
	return src, mapErr(err)
}

func (s *PgStore) RecordCommitRelayStatus(ctx context.Context, src CommitRelaySource, code string, pending, blocked *int64, oldest *time.Time, backoff time.Duration, cleanup ...*int64) error {
	var observation CommitRelayPollObservation
	if len(cleanup) > 0 {
		observation.CleanupEligible = cleanup[0]
	}
	return s.RecordCommitRelayPoll(ctx, src, code, pending, blocked, oldest, backoff, observation)
}

type CommitRelayPollObservation struct {
	CleanupEligible *int64
	ConnectMS       *int32
}

// RecordCommitRelayPoll binds a connection measurement to the same fenced
// healthy observation that allows a source onto the reserved worker.
func (s *PgStore) RecordCommitRelayPoll(ctx context.Context, src CommitRelaySource, code string, pending, blocked *int64, oldest *time.Time, backoff time.Duration, observation CommitRelayPollObservation) error {
	connectMS := observation.ConnectMS
	if code != "healthy" {
		connectMS = nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE commit_sources SET relay_status=$4,last_checked_at=statement_timestamp(),
 pending_events=$5,blocked_events=$6,oldest_pending_at=$7,relay_poll_backoff_ms=$8,cleanup_eligible_events=$10,
 cleanup_observed_at=CASE WHEN $10::bigint IS NULL THEN NULL ELSE statement_timestamp() END,
 relay_connect_ms=$11,relay_connect_observed_at=CASE WHEN $11::int IS NULL THEN NULL ELSE statement_timestamp() END,
 relay_next_poll_at=statement_timestamp()+(poll_interval_ms+$8::int)*interval '1 millisecond',relay_poll_token=NULL
 WHERE account_id=$1::uuid AND id=$2::uuid AND credential_revision=$3 AND enabled
 AND relay_poll_token=$9::uuid AND relay_next_poll_at>clock_timestamp()`,
		src.AccountID, src.ID, src.CredentialRevision, code, pending, blocked, oldest, backoff.Milliseconds(), src.PollToken, observation.CleanupEligible, connectMS)
	return err
}
func (s *PgStore) CommitReceiptByEvent(ctx context.Context, account, source, event string) (CommitReceipt, error) {
	row, err := sqlc.New().CommitReceiptIdentity(ctx, s.pool, sqlc.CommitReceiptIdentityParams{AccountID: account, SourceID: source, EventID: event})
	receipt := CommitReceipt{ID: row.ID, SourceID: row.SourceID, EventID: row.EventID,
		InvocationID: row.InvocationID, OperationID: row.OperationID, AcceptedAt: row.AcceptedAt.Time}
	if row.PublicationAcceptanceID > 0 {
		receipt.Publication = commitPublicationIdentity(row.PublicationAcceptanceID, row.PublicationSource, row.PublicationEventID, receipt.AcceptedAt)
	} else {
		receipt.OperationURL = "/v1/operations/" + receipt.WorkID()
	}
	return receipt, mapErr(err)
}

// AcceptCommitEvent serializes source acceptance and checks replay before
// capacity. Receipt and invocation commit together; receipt retention is
// independent of invocation retention. The source fixes the destination.
func (s *PgStore) AcceptCommitEvent(ctx context.Context, account, source, event, kind string, data json.RawMessage, inv Invocation, maxPending int, routing ...*api.CommitRouting) (CommitReceipt, error) {
	src, err := s.CommitSourceByID(ctx, account, source)
	if err != nil {
		return CommitReceipt{}, err
	}
	if len(routing) > 1 {
		return CommitReceipt{}, ErrInvalidArgument
	}
	if src.Destination == "event" {
		return s.AcceptCommitPublication(ctx, account, source, commitwork.Event{ID: event, Type: kind, Data: data, Routing: commitSelectedRouting(routing)})
	}
	if src.OperationPolicy != "" {
		return s.AcceptCommitOperation(ctx, account, source, event, kind, data, inv, routing...)
	}
	if len(routing) > 1 || (len(routing) == 1 && routing[0] != nil) {
		return CommitReceipt{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CommitReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Use the same account/source lock order as managed admission and pause.
	owner := &exclusivePostgresTx{ctx: ctx, db: tx, q: sqlc.New()}
	if err := owner.lockAccount(account); err != nil {
		return CommitReceipt{}, exclusiveSQLError(err)
	}
	var app string
	var enabled bool
	var tenantRequired bool
	var changes bool
	err = tx.QueryRow(ctx, `SELECT c.app_id::text,c.enabled,a.platform_tenant_required,c.queue_name IS NOT NULL FROM commit_sources c JOIN apps a ON a.id=c.app_id AND a.account_id=c.account_id
 WHERE c.account_id=$1::uuid AND c.id=$2::uuid FOR UPDATE OF c FOR SHARE OF a`, account, source).Scan(&app, &enabled, &tenantRequired, &changes)
	if err != nil {
		return CommitReceipt{}, mapErr(err)
	}
	var receipt CommitReceipt
	var matches bool
	err = tx.QueryRow(ctx, `SELECT id::text,source_id::text,event_id::text,invocation_id::text,accepted_at,
 event_type=$4 AND payload=$5::jsonb FROM commit_receipts WHERE account_id=$1::uuid AND source_id=$2::uuid AND event_id=$3::uuid`, account, source, event, kind, []byte(data)).Scan(&receipt.ID, &receipt.SourceID, &receipt.EventID, &receipt.InvocationID, &receipt.AcceptedAt, &matches)
	if err == nil {
		if !matches {
			return CommitReceipt{}, ErrConflict
		}
		receipt.OperationURL = "/v1/operations/" + receipt.InvocationID
		return receipt, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CommitReceipt{}, err
	}
	// A changes source accepts only captured row changes, through
	// AcceptCommitChange; its existing receipts stay recoverable above.
	if changes || !enabled || tenantRequired || app != inv.AppID || account != inv.AccountID || maxPending < 1 {
		return CommitReceipt{}, ErrInvalidArgument
	}
	var pending int
	// Serializes queue admission among Commit sources targeting this application.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, app); err != nil {
		return CommitReceipt{}, err
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FROM invocations WHERE app_id=$1::uuid AND source IN ('queue','async_invoke') AND state IN ('pending','dispatching')`, app).Scan(&pending)
	if err != nil {
		return CommitReceipt{}, err
	}
	if pending >= maxPending {
		return CommitReceipt{}, ErrCommitQueueFull
	}
	inv.ID = uuid.NewString()
	if _, err = enqueueInvocationRow(ctx, tx, inv); err != nil {
		return CommitReceipt{}, err
	}
	receipt = CommitReceipt{ID: uuid.NewString(), SourceID: source, EventID: event, InvocationID: inv.ID, OperationURL: "/v1/operations/" + inv.ID}
	err = tx.QueryRow(ctx, `INSERT INTO commit_receipts(id,account_id,source_id,event_id,event_type,payload,invocation_id)
 VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::jsonb,$7::uuid) RETURNING accepted_at`, receipt.ID, account, source, event, kind, []byte(data), inv.ID).Scan(&receipt.AcceptedAt)
	if err != nil {
		return CommitReceipt{}, err
	}
	return receipt, tx.Commit(ctx)
}
