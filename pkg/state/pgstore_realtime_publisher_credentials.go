package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	ttlq "github.com/onebox-faas/faas/pkg/state/realtimettl/sqlc"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func scanRealtimePublisherCredential(row pgx.Row) (RealtimePublisherCredential, error) {
	var c RealtimePublisherCredential
	err := row.Scan(&c.ID, &c.EndpointID, &c.Name, &c.Channels, &c.EventTypes, &c.PublishesPerMinute, &c.MaxPayloadBytes, &c.Generation, &c.IssuedAt, &c.ExpiresAt, &c.RevokedAt, &c.AcceptedCount, &c.ReplayCount, &c.LastUsedAt, &c.LastChannel, &c.LastSequence, &c.MaxMessageTTLSeconds)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return c, err
}
func lockRealtimePublisherEndpoint(ctx context.Context, tx pgx.Tx, ep string, enabled bool) error {
	var id string
	err := tx.QueryRow(ctx, `select id from managed_realtime_endpoints where id=$1 and (not $2::boolean or enabled) for update`, ep, enabled).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func (s *PgStore) IssueRealtimePublisherCredential(ctx context.Context, ep string, req RealtimePublisherCredentialRequest) (RealtimePublisherCredentialIssued, error) {
	var result RealtimePublisherCredentialIssued
	if err := normalizeRealtimePublisherPolicy(&req); err != nil {
		return result, err
	}
	token, hash, err := newRealtimePublisherToken()
	if err != nil {
		return result, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockRealtimePublisherEndpoint(ctx, tx, ep, true); err != nil {
		return result, err
	}
	_, err = tx.Exec(ctx, `delete from managed_realtime_publisher_credentials where endpoint_id=$1 and least(expires_at,coalesce(revoked_at,expires_at))<clock_timestamp()-make_interval(secs=>$2::double precision)`, ep, api.RealtimePublisherCredentialHistorySeconds)
	if err != nil {
		return result, err
	}
	var count int
	err = tx.QueryRow(ctx, `select count(*) from managed_realtime_publisher_credentials where endpoint_id=$1`, ep).Scan(&count)
	if err != nil {
		return result, err
	}
	if count >= api.RealtimePublisherCredentialsMaxPerEndpoint {
		return result, &RealtimeConsumerGroupCapacityError{Resource: "publisher credentials per endpoint", Limit: api.RealtimePublisherCredentialsMaxPerEndpoint, Observed: count}
	}
	result.Info, err = scanRealtimePublisherCredential(tx.QueryRow(ctx, `insert into managed_realtime_publisher_credentials(endpoint_id,name,token_hash,channels,event_types,publishes_per_minute,max_payload_bytes,issued_at,expires_at)
 select $1,$2,$3,$4,$5,$6,$7,t,t+make_interval(secs=>$8::double precision) from (select clock_timestamp() t) stamp
 returning id,endpoint_id,name,channels,event_types,publishes_per_minute,max_payload_bytes,generation::text,issued_at,expires_at,revoked_at,accepted_count::text,replay_count::text,last_used_at,last_channel,last_sequence::text,max_message_ttl_seconds`, ep, req.Name, hash, req.Channels, req.EventTypes, req.PublishesPerMinute, req.MaxPayloadBytes, req.TTLSeconds))
	if err != nil {
		return result, err
	}
	if err = new(ttlq.Queries).SetPublisherTTLCap(ctx, tx, ttlq.SetPublisherTTLCapParams{ID: result.Info.ID, MaxMessageTtlSeconds: int32(req.MaxMessageTTLSeconds)}); err != nil {
		return result, err
	}
	result.Info.MaxMessageTTLSeconds = req.MaxMessageTTLSeconds
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	result.Credential = token
	return result, nil
}
func (s *PgStore) ListRealtimePublisherCredentials(ctx context.Context, ep string) ([]RealtimePublisherCredential, error) {
	rows, err := s.pool.Query(ctx, `select id,endpoint_id,name,channels,event_types,publishes_per_minute,max_payload_bytes,generation::text,issued_at,expires_at,revoked_at,accepted_count::text,replay_count::text,last_used_at,last_channel,last_sequence::text,max_message_ttl_seconds
 from managed_realtime_publisher_credentials where endpoint_id=$1 order by issued_at,id limit $2`, ep, api.RealtimePublisherCredentialsMaxPerEndpoint)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []RealtimePublisherCredential{}
	for rows.Next() {
		c, err := scanRealtimePublisherCredential(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func (s *PgStore) RotateRealtimePublisherCredential(ctx context.Context, ep, id string, ttl int) (RealtimePublisherCredentialIssued, error) {
	var result RealtimePublisherCredentialIssued
	if ttl == 0 {
		ttl = api.RealtimePublisherCredentialTTLDefaultSeconds
	}
	if ttl < 1 || ttl > api.RealtimePublisherCredentialTTLMaxSeconds {
		return result, ErrInvalidArgument
	}
	token, hash, err := newRealtimePublisherToken()
	if err != nil {
		return result, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockRealtimePublisherEndpoint(ctx, tx, ep, true); err != nil {
		return result, err
	}
	result.Info, err = scanRealtimePublisherCredential(tx.QueryRow(ctx, `update managed_realtime_publisher_credentials set token_hash=$3,generation=generation+1,issued_at=t,expires_at=t+make_interval(secs=>$4::double precision)
 from (select clock_timestamp() t) stamp where id=$1 and endpoint_id=$2 and revoked_at is null
 returning id,endpoint_id,name,channels,event_types,publishes_per_minute,max_payload_bytes,generation::text,issued_at,expires_at,revoked_at,accepted_count::text,replay_count::text,last_used_at,last_channel,last_sequence::text,max_message_ttl_seconds`, id, ep, hash, ttl))
	if err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	result.Credential = token
	return result, nil
}
func (s *PgStore) RevokeRealtimePublisherCredential(ctx context.Context, ep, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockRealtimePublisherEndpoint(ctx, tx, ep, false); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `update managed_realtime_publisher_credentials set revoked_at=coalesce(revoked_at,clock_timestamp()) where id=$1 and endpoint_id=$2`, id, ep)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}
func (s *PgStore) AuthenticateRealtimePublisherCredential(ctx context.Context, ep, token string) (RealtimePublisherCredential, error) {
	hash, err := realtimePublisherTokenHash(token)
	if err != nil {
		return RealtimePublisherCredential{}, err
	}
	c, err := scanRealtimePublisherCredential(s.pool.QueryRow(ctx, `select c.id,c.endpoint_id,c.name,c.channels,c.event_types,c.publishes_per_minute,c.max_payload_bytes,c.generation::text,c.issued_at,c.expires_at,c.revoked_at,c.accepted_count::text,c.replay_count::text,c.last_used_at,c.last_channel,c.last_sequence::text,c.max_message_ttl_seconds
 from managed_realtime_publisher_credentials c join managed_realtime_endpoints e on e.id=c.endpoint_id join accounts a on a.id=e.account_id join apps app on app.id=e.app_id
 where c.token_hash=$1 and e.id=$2 and c.revoked_at is null and c.expires_at>clock_timestamp() and e.enabled and a.status in ('active','past_due') and app.status<>'deleted'`, hash, ep))
	if errors.Is(err, ErrNotFound) {
		err = ErrRealtimePublisherCredentialDenied
	}
	return c, err
}

type realtimePublisherAppend struct {
	credentialID           string
	tokenHash, contentHash []byte
	key, eventType         string
	ttlSeconds             int
	replayed               bool
}
type realtimePublisherAppendKey struct{}

func (s *PgStore) PublishRealtimeWithCredential(ctx context.Context, ep, ch, token string, req RealtimePublisherMessageRequest, key string) (RealtimePublisherResult, error) {
	var result RealtimePublisherResult
	if !realtimePublisherKeyValid(key) || validateManagedRealtimeHistoryRequest(ep, ch) != nil || !validRealtimeWorkerName(req.EventType, api.RealtimePublisherEventTypeMaxBytes) {
		return result, ErrInvalidArgument
	}
	data, err := base64.StdEncoding.DecodeString(req.DataBase64)
	if err != nil || len(data) > api.RealtimePublisherPayloadMaxBytes {
		return result, ErrInvalidArgument
	}
	metadata := cloneRealtimeMetadata(req.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	if event, exists := metadata["event_type"]; exists && event != req.EventType {
		return result, ErrInvalidArgument
	}
	metadata["event_type"] = req.EventType
	if api.ValidateRealtimeMetadata(metadata) != nil {
		return result, ErrInvalidArgument
	}
	ctx, err = ttlContext(ctx, req.TTLSeconds)
	if err != nil {
		return result, err
	}
	credential, err := s.AuthenticateRealtimePublisherCredential(ctx, ep, token)
	if err != nil {
		return result, err
	}
	hash, _ := realtimePublisherTokenHash(token)
	encoded, _ := json.Marshal(struct {
		Channel    string
		Data       []byte
		Binary     bool
		Metadata   map[string]string
		TTLSeconds int `json:"TTLSeconds,omitempty"`
	}{ch, data, req.Binary, metadata, req.TTLSeconds})
	content := sha256.Sum256(encoded)
	input := &realtimePublisherAppend{credentialID: credential.ID, tokenHash: hash, contentHash: content[:], key: key, eventType: req.EventType, ttlSeconds: req.TTLSeconds}
	digest := sha256.Sum256([]byte(key))
	publisherID := "publisher:" + credential.ID + ":" + hex.EncodeToString(digest[:])
	result.Message, err = s.appendManagedRealtimeChannel(context.WithValue(ctx, realtimePublisherAppendKey{}, input), ep, ch, data, req.Binary, publisherID, metadata, nil, nil, nil)
	result.Replayed = input.replayed
	return result, err
}
func lockRealtimePublisherAppend(ctx context.Context, tx pgx.Tx, input *realtimePublisherAppend, ep, ch string, data []byte) (ManagedRealtimeChannelMessage, bool, error) {
	var message ManagedRealtimeChannelMessage
	if err := lockRealtimePublisherEndpoint(ctx, tx, ep, true); err != nil {
		if errors.Is(err, ErrNotFound) {
			err = ErrRealtimePublisherCredentialDenied
		}
		return message, false, err
	}
	var maxPayload, rate, count int
	var plan api.Plan
	var endpointMax int64
	var channelAllowed, eventAllowed bool
	err := tx.QueryRow(ctx, `select c.max_payload_bytes,c.publishes_per_minute,
 case when c.rate_window=date_trunc('minute',clock_timestamp()) then c.rate_count else 0 end,a.plan,$4=any(c.channels),$5=any(c.event_types),e.max_message_bytes
 from managed_realtime_publisher_credentials c join managed_realtime_endpoints e on e.id=c.endpoint_id join accounts a on a.id=e.account_id join apps app on app.id=e.app_id
 where c.id=$1 and c.endpoint_id=$2 and c.token_hash=$3 and c.revoked_at is null and c.expires_at>clock_timestamp() and a.status in ('active','past_due') and app.status<>'deleted'
 for update of c`, input.credentialID, ep, input.tokenHash, ch, input.eventType).Scan(&maxPayload, &rate, &count, &plan, &channelAllowed, &eventAllowed, &endpointMax)
	if errors.Is(err, pgx.ErrNoRows) {
		return message, false, ErrRealtimePublisherCredentialDenied
	}
	if err != nil {
		return message, false, err
	}
	capSeconds, err := new(ttlq.Queries).ReadPublisherTTLCap(ctx, tx, input.credentialID)
	if err != nil {
		return message, false, err
	}
	if capSeconds > 0 && (input.ttlSeconds == 0 || input.ttlSeconds > int(capSeconds)) {
		return message, false, ErrRealtimePublisherPermission
	}
	if limits, ok := api.RealtimeLimitsFor(plan); !ok || limits.EndpointsPerApp == 0 {
		return message, false, ErrRealtimePublisherPermission
	}
	if !channelAllowed || !eventAllowed {
		return message, false, ErrRealtimePublisherPermission
	}
	if len(data) > maxPayload || (endpointMax > 0 && int64(len(data)) > endpointMax) {
		return message, false, ErrInvalidArgument
	}
	_, err = tx.Exec(ctx, `delete from managed_realtime_publisher_receipts where credential_id=$1 and expires_at<=clock_timestamp()`, input.credentialID)
	if err != nil {
		return message, false, err
	}
	var previousHash []byte
	err = tx.QueryRow(ctx, `select content_hash,channel,sequence,published_at from managed_realtime_publisher_receipts where credential_id=$1 and idempotency_key=$2`, input.credentialID, input.key).Scan(&previousHash, &message.Channel, &message.Sequence, &message.CreatedAt)
	if err == nil {
		if !bytes.Equal(previousHash, input.contentHash) {
			return message, false, ErrConflict
		}
		message.EndpointID = ep
		message.TTLSeconds = input.ttlSeconds
		if input.ttlSeconds > 0 {
			deadline := message.CreatedAt.Add(time.Duration(input.ttlSeconds) * time.Second)
			message.ExpiresAt = &deadline
		}
		input.replayed = true
		_, err = tx.Exec(ctx, `update managed_realtime_publisher_credentials set replay_count=replay_count+1,last_used_at=clock_timestamp(),last_channel=$2,last_sequence=$3 where id=$1`, input.credentialID, ch, message.Sequence)
		return message, true, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return message, false, err
	}
	if count >= rate {
		return message, false, ErrRealtimePublisherRate
	}
	var receipts int
	err = tx.QueryRow(ctx, `select count(*) from managed_realtime_publisher_receipts where credential_id=$1`, input.credentialID).Scan(&receipts)
	if err != nil {
		return message, false, err
	}
	if receipts >= api.RealtimePublisherReceiptsMaxPerCredential {
		return message, false, &RealtimeConsumerGroupCapacityError{Resource: "publisher idempotency receipts", Limit: api.RealtimePublisherReceiptsMaxPerCredential, Observed: receipts}
	}
	return message, false, nil
}
func saveRealtimePublisherAppend(ctx context.Context, tx pgx.Tx, input *realtimePublisherAppend, message ManagedRealtimeChannelMessage) error {
	_, err := tx.Exec(ctx, `insert into managed_realtime_publisher_receipts(credential_id,idempotency_key,content_hash,channel,sequence,published_at,expires_at)
 values($1,$2,$3,$4,$5,$6,clock_timestamp()+make_interval(secs=>$7::double precision))`, input.credentialID, input.key, input.contentHash, message.Channel, message.Sequence, message.CreatedAt, api.RealtimePublisherReceiptRetentionSeconds)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `update managed_realtime_publisher_credentials set
 rate_count=case when rate_window=date_trunc('minute',clock_timestamp()) then rate_count+1 else 1 end,
 rate_window=date_trunc('minute',clock_timestamp()),accepted_count=accepted_count+1,last_used_at=clock_timestamp(),last_channel=$2,last_sequence=$3 where id=$1`, input.credentialID, message.Channel, message.Sequence)
	return err
}
