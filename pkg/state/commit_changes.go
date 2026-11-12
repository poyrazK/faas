package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrCommitQueueUnavailable means a changes source's app has no enabled,
// shared queue binding named by the source. The relay retries: changes wait
// in the customer's outbox until the worker that declares it is deployed
// (ADR-20261112140000 §3).
var ErrCommitQueueUnavailable = errors.New("state: commit changes queue binding unavailable")

// CommitChangesConfig is a changes source's declared delivery and capture
// (ADR-20261112140000). QueueName is empty for every other Commit source.
type CommitChangesConfig struct {
	QueueName         string          `json:"queue_name,omitempty"`
	Feeds             json.RawMessage `json:"feeds,omitempty"`
	ManagedDatabaseID string          `json:"managed_database_id,omitempty"`
	PollIntervalMS    int             `json:"poll_interval_ms,omitempty"`
	// Connected reports whether a relay connection has been sealed.
	Connected bool `json:"connected"`
}

// IsChanges reports whether the source delivers row changes to a queue.
func (c CommitChangesConfig) IsChanges() bool { return c.QueueName != "" }

// PollDue reports whether a source last checked at last is due for a relay
// tick at now. Sources other than changes sources are always due.
func (c CommitChangesConfig) PollDue(last *time.Time, now time.Time) bool {
	if !c.IsChanges() || last == nil || c.PollIntervalMS <= 0 {
		return true
	}
	return !now.Before(last.Add(time.Duration(c.PollIntervalMS) * time.Millisecond))
}

// CommitChangesStore is the narrow surface apid uses to manage changes
// sources; it is deliberately not part of Store.
type CommitChangesStore interface {
	UpsertCommitChangesSource(ctx context.Context, account, app, name string, cfg CommitChangesConfig) (CommitSource, error)
	CommitChangesSourceForApp(ctx context.Context, account, app string) (CommitSource, CommitChangesConfig, error)
	SetCommitSourceConnection(ctx context.Context, account, id string, blob []byte) error
	SetCommitSourceEnabled(ctx context.Context, account, id string, enabled bool) (CommitSource, error)
}

// UpsertCommitChangesSource creates the app's changes source, or replaces the
// declared configuration of the one it has. name is unique per account; a
// name held by another app or by a non-changes source conflicts, as does a
// second changes source for the app under another name.
func (s *PgStore) UpsertCommitChangesSource(ctx context.Context, account, app, name string, cfg CommitChangesConfig) (CommitSource, error) {
	if !cfg.IsChanges() || len(cfg.Feeds) == 0 || cfg.PollIntervalMS < 1000 || cfg.PollIntervalMS > 600000 {
		return CommitSource{}, ErrInvalidArgument
	}
	src := CommitSource{AccountID: account, AppID: app, Name: name, Destination: "operation", ContractVersion: 1}
	err := s.pool.QueryRow(ctx, `INSERT INTO commit_sources(account_id, app_id, name, queue_name, changes_feeds, managed_database_id, poll_interval_ms)
 SELECT $1::uuid, id, $3, $4, $5::jsonb, NULLIF($6, '')::uuid, $7 FROM apps WHERE id = $2::uuid AND account_id = $1::uuid
 ON CONFLICT (account_id, name) DO UPDATE SET queue_name = excluded.queue_name, changes_feeds = excluded.changes_feeds,
   managed_database_id = excluded.managed_database_id, poll_interval_ms = excluded.poll_interval_ms
 WHERE commit_sources.app_id = excluded.app_id AND commit_sources.queue_name IS NOT NULL
 RETURNING id::text, enabled`, account, app, name, cfg.QueueName, []byte(cfg.Feeds), cfg.ManagedDatabaseID, cfg.PollIntervalMS).Scan(&src.ID, &src.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		var appExists bool
		if lookupErr := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM apps WHERE id = $1::uuid AND account_id = $2::uuid)`, app, account).Scan(&appExists); lookupErr != nil {
			return CommitSource{}, lookupErr
		}
		if !appExists {
			return CommitSource{}, ErrNotFound
		}
		return CommitSource{}, ErrConflict
	}
	return src, mapErr(err)
}

// CommitChangesSourceForApp returns the app's changes source.
func (s *PgStore) CommitChangesSourceForApp(ctx context.Context, account, app string) (CommitSource, CommitChangesConfig, error) {
	var src CommitSource
	var cfg CommitChangesConfig
	var feeds []byte
	err := s.pool.QueryRow(ctx, `SELECT id::text, app_id::text, name, enabled, relay_status, last_checked_at, pending_events, blocked_events, oldest_pending_at,
 queue_name, changes_feeds, COALESCE(managed_database_id::text, ''), poll_interval_ms, sealed_connection IS NOT NULL
 FROM commit_sources WHERE account_id = $1::uuid AND app_id = $2::uuid AND queue_name IS NOT NULL`, account, app).Scan(
		&src.ID, &src.AppID, &src.Name, &src.Enabled, &src.RelayStatus, &src.LastCheckedAt, &src.PendingEvents, &src.BlockedEvents, &src.OldestPendingAt,
		&cfg.QueueName, &feeds, &cfg.ManagedDatabaseID, &cfg.PollIntervalMS, &cfg.Connected)
	if err != nil {
		return CommitSource{}, CommitChangesConfig{}, mapErr(err)
	}
	src.AccountID, src.Destination, src.ContractVersion = account, "operation", 1
	cfg.Feeds = json.RawMessage(feeds)
	return src, cfg, nil
}

// AcceptCommitChange accepts one captured row change of a changes source into
// its app's named queue. Like AcceptCommitEvent, the receipt and the
// invocation commit together and a replayed event returns its original
// receipt; a changed type or payload for the same event conflicts. inv
// carries the app, payload, version and admission caps; the source fixes the
// queue and its binding.
//
// Lock order matches pause and the other Commit admissions: account, source,
// then the app row the queue admission serialises on.
func (s *PgStore) AcceptCommitChange(ctx context.Context, account, source, event, kind string, data json.RawMessage, inv Invocation) (CommitReceipt, error) {
	if _, err := uuid.Parse(event); err != nil {
		return CommitReceipt{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CommitReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	owner := &exclusivePostgresTx{ctx: ctx, db: tx, q: sqlc.New()}
	if err := owner.lockAccount(account); err != nil {
		return CommitReceipt{}, exclusiveSQLError(err)
	}
	var app, queue string
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT app_id::text, enabled, COALESCE(queue_name, '') FROM commit_sources
 WHERE account_id = $1::uuid AND id = $2::uuid FOR UPDATE`, account, source).Scan(&app, &enabled, &queue)
	if err != nil {
		return CommitReceipt{}, mapErr(err)
	}
	if queue == "" {
		return CommitReceipt{}, ErrInvalidArgument
	}
	var receipt CommitReceipt
	var matches bool
	err = tx.QueryRow(ctx, `SELECT id::text, source_id::text, event_id::text, invocation_id::text, accepted_at,
 event_type = $4 AND payload = $5::jsonb FROM commit_receipts
 WHERE account_id = $1::uuid AND source_id = $2::uuid AND event_id = $3::uuid`,
		account, source, event, kind, []byte(data)).Scan(&receipt.ID, &receipt.SourceID, &receipt.EventID, &receipt.InvocationID, &receipt.AcceptedAt, &matches)
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
	if !enabled || app != inv.AppID || account != inv.AccountID || inv.AdmissionMaxPending < 1 {
		return CommitReceipt{}, ErrInvalidArgument
	}
	inv.Source = InvocationQueue
	inv.QueueName = queue
	inv.ID = uuid.NewString()
	if err := lockInvocationAdmissionAppDB(ctx, tx, inv); err != nil {
		return CommitReceipt{}, err
	}
	var tenantRequired bool
	if err := tx.QueryRow(ctx, `SELECT platform_tenant_required FROM apps WHERE id = $1::uuid AND account_id = $2::uuid`, app, account).Scan(&tenantRequired); err != nil {
		return CommitReceipt{}, mapErr(err)
	}
	if tenantRequired {
		return CommitReceipt{}, ErrInvalidArgument
	}
	// Only the app-wide (shared) binding: environment-owned queues are out of
	// scope for changes sources.
	err = tx.QueryRow(ctx, `SELECT id::text FROM queue_bindings
 WHERE account_id = $1::uuid AND app_id = $2::uuid AND queue_name = $3
   AND deployment_scope = '' AND enabled AND retired_at IS NULL
 ORDER BY created_at, id LIMIT 1`, account, app, queue).Scan(&inv.QueueBindingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CommitReceipt{}, ErrCommitQueueUnavailable
	}
	if err != nil {
		return CommitReceipt{}, err
	}
	if err := checkInvocationAdmissionDepthDB(ctx, tx, inv); err != nil {
		if errors.Is(err, ErrQuotaExceeded) {
			return CommitReceipt{}, ErrCommitQueueFull
		}
		return CommitReceipt{}, err
	}
	if _, err := enqueueInvocationRow(ctx, tx, inv); err != nil {
		return CommitReceipt{}, err
	}
	receipt = CommitReceipt{ID: uuid.NewString(), SourceID: source, EventID: event, InvocationID: inv.ID, OperationURL: "/v1/operations/" + inv.ID}
	err = tx.QueryRow(ctx, `INSERT INTO commit_receipts(id, account_id, source_id, event_id, event_type, payload, invocation_id)
 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::jsonb, $7::uuid) RETURNING accepted_at`,
		receipt.ID, account, source, event, kind, []byte(data), inv.ID).Scan(&receipt.AcceptedAt)
	if err != nil {
		return CommitReceipt{}, err
	}
	return receipt, tx.Commit(ctx)
}
