package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrCommitQueueFull = errors.New("state: commit queue full")

type CommitSource struct {
	ID              string     `json:"id"`
	AccountID       string     `json:"-"`
	AppID           string     `json:"app_id"`
	Name            string     `json:"name"`
	Enabled         bool       `json:"enabled"`
	RelayStatus     string     `json:"relay_status,omitempty"`
	LastCheckedAt   *time.Time `json:"last_checked_at,omitempty"`
	PendingEvents   *int64     `json:"pending_events,omitempty"`
	BlockedEvents   *int64     `json:"blocked_events,omitempty"`
	OldestPendingAt *time.Time `json:"oldest_pending_at,omitempty"`
}
type CommitReceipt struct {
	ID           string    `json:"receipt_id"`
	SourceID     string    `json:"source_id"`
	EventID      string    `json:"event_id"`
	InvocationID string    `json:"invocation_id"`
	AcceptedAt   time.Time `json:"accepted_at"`
	OperationURL string    `json:"operation_url"`
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
	var operation CommitOperation
	err := s.pool.QueryRow(ctx, `SELECT invocation_id::text,id::text,source_id::text,event_id::text,operation_state,accepted_at,completed_at
 FROM commit_receipts WHERE account_id=$1::uuid AND invocation_id=$2::uuid`, account, id).Scan(&operation.ID, &operation.ReceiptID, &operation.SourceID, &operation.EventID, &operation.State, &operation.AcceptedAt, &operation.CompletedAt)
	return operation, mapErr(err)
}

// CommitRelaySource is an internal projection. Ciphertext never appears in the
// public source DTO. Keyset paging bounds scheduler work without starving
// sources beyond the first page.
type CommitRelaySource struct {
	CommitSource
	SealedConnection   []byte
	CredentialRevision int64
}

type CommitBlockedEvent struct {
	EventID    string    `json:"event_id"`
	Type       string    `json:"type"`
	Code       string    `json:"blocked_code"`
	CreatedAt  time.Time `json:"created_at"`
	ObservedAt time.Time `json:"observed_at"`
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
	var current bool
	err = tx.QueryRow(ctx, `SELECT enabled AND credential_revision=$3 FROM commit_sources WHERE account_id=$1::uuid AND id=$2::uuid FOR UPDATE`, src.AccountID, src.ID, src.CredentialRevision).Scan(&current)
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
 ON CONFLICT(source_id,event_id) DO UPDATE SET state='pending',requested_at=now(),generation=commit_replay_requests.generation+1`, account, source, event)
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
	rows, err := s.pool.Query(ctx, `SELECT event_id::text,generation FROM commit_replay_requests WHERE account_id=$1::uuid AND source_id=$2::uuid AND state='pending' ORDER BY requested_at,event_id LIMIT 32`, account, source)
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
func (s *PgStore) CompleteCommitReplay(ctx context.Context, account, source, event string, generation int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE commit_replay_requests SET state='completed' WHERE account_id=$1::uuid AND source_id=$2::uuid AND event_id=$3::uuid AND generation=$4`, account, source, event, generation)
	return err
}

func (s *PgStore) ListCommitRelaySources(ctx context.Context, after string, limit int) ([]CommitRelaySource, error) {
	if limit < 1 || limit > 64 {
		return nil, ErrInvalidArgument
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,account_id::text,app_id::text,name,enabled,sealed_connection,credential_revision
 FROM commit_sources WHERE enabled AND sealed_connection IS NOT NULL
 AND ($1::text='' OR id>NULLIF($1,'')::uuid) ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sources []CommitRelaySource
	for rows.Next() {
		var src CommitRelaySource
		if err := rows.Scan(&src.ID, &src.AccountID, &src.AppID, &src.Name, &src.Enabled, &src.SealedConnection, &src.CredentialRevision); err != nil {
			return nil, err
		}
		sources = append(sources, src)
	}
	return sources, rows.Err()
}

type CommitStore interface {
	CreateCommitSource(context.Context, CommitSource) (CommitSource, error)
	CommitSourceByID(context.Context, string, string) (CommitSource, error)
	SetCommitSourceEnabled(context.Context, string, string, bool) (CommitSource, error)
	SetCommitSourceConnection(context.Context, string, string, []byte) error
	AcceptCommitEvent(context.Context, string, string, string, string, json.RawMessage, Invocation, int) (CommitReceipt, error)
	CommitReceiptByEvent(context.Context, string, string, string) (CommitReceipt, error)
	ListCommitBlockedEvents(context.Context, string, string) ([]CommitBlockedEvent, error)
	RequestCommitReplay(context.Context, string, string, string) error
	CommitOperationByID(context.Context, string, string) (CommitOperation, error)
}

func (s *PgStore) SetCommitSourceConnection(ctx context.Context, account, id string, blob []byte) error {
	if len(blob) == 0 || len(blob) > 16384 {
		return ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `UPDATE commit_sources SET sealed_connection=$3,credential_revision=credential_revision+1
 WHERE account_id=$1::uuid AND id=$2::uuid`, account, id, blob)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCommitSourceEnabled takes the same row lock as acceptance. Once pause
// returns, no new event can be accepted; existing receipts remain recoverable.
func (s *PgStore) SetCommitSourceEnabled(ctx context.Context, account, id string, enabled bool) (CommitSource, error) {
	src := CommitSource{AccountID: account}
	err := s.pool.QueryRow(ctx, `UPDATE commit_sources SET enabled=$3 WHERE account_id=$1::uuid AND id=$2::uuid
 RETURNING id::text,app_id::text,name,enabled`, account, id, enabled).Scan(&src.ID, &src.AppID, &src.Name, &src.Enabled)
	return src, mapErr(err)
}

func (s *PgStore) CreateCommitSource(ctx context.Context, src CommitSource) (CommitSource, error) {
	err := s.pool.QueryRow(ctx, `INSERT INTO commit_sources(account_id,app_id,name)
 SELECT $1::uuid,id,$3 FROM apps WHERE id=$2::uuid AND account_id=$1::uuid
 ON CONFLICT(account_id,name) DO UPDATE SET name=commit_sources.name
 WHERE commit_sources.app_id=excluded.app_id
 RETURNING id::text,enabled`, src.AccountID, src.AppID, src.Name).Scan(&src.ID, &src.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		var conflict bool
		lookupErr := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM commit_sources WHERE account_id=$1::uuid AND name=$2 AND app_id<>$3::uuid)`, src.AccountID, src.Name, src.AppID).Scan(&conflict)
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
	src := CommitSource{AccountID: account}
	err := s.pool.QueryRow(ctx, `SELECT id::text,app_id::text,name,enabled,relay_status,last_checked_at,pending_events,blocked_events,oldest_pending_at FROM commit_sources WHERE account_id=$1::uuid AND id=$2::uuid`, account, id).Scan(&src.ID, &src.AppID, &src.Name, &src.Enabled, &src.RelayStatus, &src.LastCheckedAt, &src.PendingEvents, &src.BlockedEvents, &src.OldestPendingAt)
	return src, mapErr(err)
}

func (s *PgStore) RecordCommitRelayStatus(ctx context.Context, src CommitRelaySource, code string, pending, blocked *int64, oldest *time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE commit_sources SET relay_status=$4,last_checked_at=now(),
 pending_events=$5,blocked_events=$6,oldest_pending_at=$7
 WHERE account_id=$1::uuid AND id=$2::uuid AND credential_revision=$3 AND enabled`, src.AccountID, src.ID, src.CredentialRevision, code, pending, blocked, oldest)
	return err
}
func (s *PgStore) CommitReceiptByEvent(ctx context.Context, account, source, event string) (CommitReceipt, error) {
	var receipt CommitReceipt
	err := s.pool.QueryRow(ctx, `SELECT id::text,source_id::text,event_id::text,invocation_id::text,accepted_at FROM commit_receipts WHERE account_id=$1::uuid AND source_id=$2::uuid AND event_id=$3::uuid`, account, source, event).Scan(&receipt.ID, &receipt.SourceID, &receipt.EventID, &receipt.InvocationID, &receipt.AcceptedAt)
	receipt.OperationURL = "/v1/operations/" + receipt.InvocationID
	return receipt, mapErr(err)
}

// AcceptCommitEvent serializes source acceptance and checks replay before
// capacity. Receipt and invocation commit together; receipt retention is
// independent of invocation retention. The source fixes the destination.
func (s *PgStore) AcceptCommitEvent(ctx context.Context, account, source, event, kind string, data json.RawMessage, inv Invocation, maxPending int) (CommitReceipt, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CommitReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var app string
	var enabled bool
	var tenantRequired bool
	err = tx.QueryRow(ctx, `SELECT c.app_id::text,c.enabled,a.platform_tenant_required FROM commit_sources c JOIN apps a ON a.id=c.app_id AND a.account_id=c.account_id
 WHERE c.account_id=$1::uuid AND c.id=$2::uuid FOR UPDATE OF c FOR SHARE OF a`, account, source).Scan(&app, &enabled, &tenantRequired)
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
	if err != pgx.ErrNoRows {
		return CommitReceipt{}, err
	}
	if !enabled || tenantRequired || app != inv.AppID || account != inv.AccountID || maxPending < 1 {
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
