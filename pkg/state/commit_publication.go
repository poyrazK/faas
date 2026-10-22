// adr: 935
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/eventcontract"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CommitPublicationStore owns the atomic Commit receipt and event acceptance.
// It is separate from CommitStore so legacy adapters do not silently emulate it.
type CommitPublicationStore interface {
	AcceptCommitPublication(context.Context, string, string, commitwork.Event) (CommitReceipt, error)
}

func commitSelectedRouting(routing []*api.CommitRouting) *api.CommitRouting {
	if len(routing) == 1 {
		return routing[0]
	}
	return nil
}

func (s *PgStore) createCommitPublicationSource(ctx context.Context, src CommitSource) (CommitSource, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return src, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.LockExclusiveWorkAccount(ctx, tx, src.AccountID); err != nil {
		return src, mapErr(err)
	}
	if _, err = q.ExclusiveWorkAppScope(ctx, tx, sqlc.ExclusiveWorkAppScopeParams{AccountID: src.AccountID, AppID: src.AppID}); err != nil {
		return src, mapErr(err)
	}
	row, err := q.CommitPublicationSource(ctx, tx, sqlc.CommitPublicationSourceParams{AccountID: src.AccountID, AppID: src.AppID, Name: src.Name})
	if errors.Is(err, pgx.ErrNoRows) {
		return src, ErrConflict
	}
	if err != nil {
		return src, err
	}
	src.ID, src.Enabled = row.ID, row.Enabled
	return src, tx.Commit(ctx)
}

func commitPublicationIdentity(id int64, source, event string, at time.Time) *api.CommitEventPublication {
	query := url.Values{"source": {source}, "id": {event}, "expected_accepted_at": {at.UTC().Format(time.RFC3339Nano)}}
	return &api.CommitEventPublication{AcceptanceID: id, Source: source, ID: event, AcceptedAt: at, ReceiptURL: "/v1/events/receipt?" + query.Encode()}
}

// AcceptCommitPublication checks retained Commit identity before live policy or
// event retention. A replay never calls event publication again. The Commit
// receipt, quota accounting and subscription snapshot commit in one transaction.
func (s *PgStore) AcceptCommitPublication(ctx context.Context, account, source string, event commitwork.Event) (CommitReceipt, error) {
	if event.Validate() != nil || event.Routing != nil {
		return CommitReceipt{}, ErrInvalidArgument
	}
	eventUUID, err := uuid.Parse(event.ID)
	if err != nil {
		return CommitReceipt{}, ErrInvalidArgument
	}
	event.ID = eventUUID.String()
	sourceUUID, err := uuid.Parse(source)
	if err != nil {
		return CommitReceipt{}, ErrInvalidArgument
	}
	source = sourceUUID.String()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CommitReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	// Match pause/admission lock order; this also fences current account plan.
	if _, err = q.LockExclusiveWorkAccount(ctx, tx, account); err != nil {
		return CommitReceipt{}, mapErr(err)
	}
	src, err := q.CommitSourceForManagedAdmission(ctx, tx, sqlc.CommitSourceForManagedAdmissionParams{AccountID: account, SourceID: source})
	if err != nil {
		return CommitReceipt{}, mapErr(err)
	}
	if src.Destination != "event" {
		return CommitReceipt{}, ErrInvalidArgument
	}
	prior, err := q.CommitPublicationReplay(ctx, tx, sqlc.CommitPublicationReplayParams{AccountID: account, SourceID: source, EventID: event.ID, EventType: event.Type, Payload: event.Data, SchemaVersion: event.SchemaVersion})
	if err == nil {
		if !prior.Matches.Valid || !prior.Matches.Bool || !prior.PublicationAcceptanceID.Valid {
			return CommitReceipt{}, ErrConflict
		}
		return CommitReceipt{ID: prior.ID, SourceID: source, EventID: event.ID, AcceptedAt: prior.AcceptedAt.Time,
			Publication: commitPublicationIdentity(prior.PublicationAcceptanceID.Int64, prior.PublicationSource.String, prior.PublicationEventID.String, prior.AcceptedAt.Time)}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return CommitReceipt{}, err
	}
	if !src.Enabled || src.ContractVersion != 3 || src.AllowTenantSelection || src.OperationPolicy != "" || src.PlatformTenantRequired {
		return CommitReceipt{}, ErrInvalidArgument
	}
	if _, err = q.ExclusiveWorkAppScope(ctx, tx, sqlc.ExclusiveWorkAppScopeParams{AccountID: account, AppID: src.CAppID}); err != nil {
		return CommitReceipt{}, mapErr(err)
	}
	// Normalize app and Commit UUIDs: textual UUID spelling must not change identity.
	appUUID, err := uuid.Parse(src.CAppID)
	if err != nil {
		return CommitReceipt{}, ErrInvalidArgument
	}
	envelope, err := (eventcontract.Envelope{ID: "commit." + sourceUUID.String() + "." + event.ID, Source: "app." + appUUID.String(), Type: event.Type, Data: event.Data, SchemaVersion: event.SchemaVersion}).Normalize(account, time.Now().UTC())
	if err != nil {
		return CommitReceipt{}, ErrInvalidArgument
	}
	if err = ValidatePublishedEventSchema(ctx, commitPublicationSchemaStore{EventSchemaStore: s, db: tx}, account, envelope.Source, envelope.Type, envelope.SchemaVersion, envelope.Data); err != nil {
		return CommitReceipt{}, err
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return CommitReceipt{}, err
	}
	if int64(len(payload)) > api.AppEventPublishBodyMaxBytes {
		return CommitReceipt{}, ErrInvalidArgument
	}
	if err = appendCustomerPublishedEventTx(ctx, tx, "commit", account, "", "", payload, nil, nil, nil); err != nil {
		return CommitReceipt{}, err
	}
	accepted, err := q.CommitPublicationAcceptance(ctx, tx, sqlc.CommitPublicationAcceptanceParams{AccountID: account, Source: envelope.Source, EventID: envelope.ID})
	if err != nil {
		return CommitReceipt{}, err
	}
	receipt := CommitReceipt{ID: uuid.NewString(), SourceID: sourceUUID.String(), EventID: event.ID, AcceptedAt: accepted.CreatedAt.Time,
		Publication: commitPublicationIdentity(accepted.ID, envelope.Source, envelope.ID, accepted.CreatedAt.Time)}
	if err = q.CommitPublicationReceipt(ctx, tx, sqlc.CommitPublicationReceiptParams{ID: receipt.ID, AccountID: account, SourceID: source, EventID: event.ID, EventType: event.Type, Payload: event.Data, SchemaVersion: event.SchemaVersion, AcceptanceID: accepted.ID, PublicationSource: envelope.Source, PublicationEventID: envelope.ID, AcceptedAt: accepted.CreatedAt}); err != nil {
		return CommitReceipt{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CommitReceipt{}, fmt.Errorf("state: commit event publication: %w", err)
	}
	return receipt, nil
}

// Schema reads use the admission transaction's connection. Opening another pool
// connection while holding the account lock would stall single-connection pools.
type commitPublicationSchemaStore struct {
	EventSchemaStore
	db sqlc.DBTX
}

func (s commitPublicationSchemaStore) LookupEventSchemaForPublish(ctx context.Context, account, source, kind, version string) (bool, json.RawMessage, error) {
	row, err := sqlc.New().CommitPublicationSchema(ctx, s.db, sqlc.CommitPublicationSchemaParams{AccountID: account, Source: source, EventType: kind, Version: version})
	return row.Required, row.Schema, err
}
