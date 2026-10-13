package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// ManagedRealtimeCommit identity survives channel-history expiry. Receipt and
// channel append commit together; a lost source checkpoint is safe to retry.
type ManagedRealtimeCommit struct {
	TTLSeconds int
	AccountID  string
	SourceID   string
	EventID    string
	EndpointID string
	Channel    string
	Data       []byte
	Metadata   map[string]string
}

func (s *PgStore) PublishManagedRealtimeCommit(ctx context.Context, input ManagedRealtimeCommit) (ManagedRealtimeChannelMessage, error) {
	for _, id := range []*string{&input.AccountID, &input.SourceID, &input.EventID} {
		parsed, err := uuid.Parse(*id)
		if err != nil || parsed == uuid.Nil {
			return ManagedRealtimeChannelMessage{}, ErrInvalidArgument
		}
		*id = parsed.String()
	}
	if !utf8.Valid(input.Data) {
		return ManagedRealtimeChannelMessage{}, ErrInvalidArgument
	}
	ctx, err := ttlContext(ctx, input.TTLSeconds)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	key := "commit:" + input.SourceID + ":" + input.EventID
	return s.appendManagedRealtimeChannel(ctx, input.EndpointID, input.Channel, input.Data, false, key, input.Metadata, nil, nil, nil, &input)
}

func realtimeCommitHash(input ManagedRealtimeCommit) []byte {
	if len(input.Metadata) == 0 {
		input.Metadata = nil
	}
	encoded, _ := json.Marshal(struct {
		Endpoint, Channel string
		Data              []byte
		Metadata          map[string]string
		TTLSeconds        int `json:"TTLSeconds,omitempty"`
	}{input.EndpointID, input.Channel, input.Data, cloneRealtimeMetadata(input.Metadata), input.TTLSeconds})
	digest := sha256.Sum256(encoded)
	return digest[:]
}

func lockRealtimeCommit(ctx context.Context, tx pgx.Tx, input ManagedRealtimeCommit) (ManagedRealtimeChannelMessage, bool, error) {
	// Serialize receipts per source, including across different channels. Locking
	// the source also fences deletion/disable while a publish is being committed.
	var app string
	err := tx.QueryRow(ctx, `SELECT app_id::text FROM commit_sources WHERE id=$1::uuid AND account_id=$2::uuid AND enabled FOR UPDATE`, input.SourceID, input.AccountID).Scan(&app)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedRealtimeChannelMessage{}, false, ErrNotFound
	}
	if err != nil {
		return ManagedRealtimeChannelMessage{}, false, err
	}
	var hash []byte
	message := ManagedRealtimeChannelMessage{EndpointID: input.EndpointID, Channel: input.Channel}
	err = tx.QueryRow(ctx, `SELECT content_hash,sequence,published_at FROM managed_realtime_commit_receipts WHERE source_id=$1::uuid AND event_id=$2::uuid`, input.SourceID, input.EventID).Scan(&hash, &message.Sequence, &message.CreatedAt)
	if err == nil {
		if !bytes.Equal(hash, realtimeCommitHash(input)) {
			return message, false, ErrConflict
		}
		message.TTLSeconds = input.TTLSeconds
		if input.TTLSeconds > 0 {
			deadline := message.CreatedAt.Add(time.Duration(input.TTLSeconds) * time.Second)
			message.ExpiresAt = &deadline
		}
		return message, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return message, false, err
	}
	var plan api.Plan
	if err = tx.QueryRow(ctx, `SELECT plan FROM accounts WHERE id=$1::uuid`, input.AccountID).Scan(&plan); err != nil {
		return message, false, err
	}
	if limits, ok := api.RealtimeLimitsFor(plan); !ok || limits.EndpointsPerApp == 0 {
		return message, false, ErrNotFound
	}
	var endpoint string
	err = tx.QueryRow(ctx, `SELECT id FROM managed_realtime_endpoints WHERE id=$1 AND account_id=$2::uuid AND app_id=$3::uuid AND enabled AND (max_message_bytes = 0 OR max_message_bytes >= $4) FOR NO KEY UPDATE`, input.EndpointID, input.AccountID, app, len(input.Data)).Scan(&endpoint)
	if errors.Is(err, pgx.ErrNoRows) {
		return message, false, ErrNotFound
	}
	return message, false, err
}

func saveRealtimeCommit(ctx context.Context, tx pgx.Tx, input ManagedRealtimeCommit, message ManagedRealtimeChannelMessage) error {
	_, err := tx.Exec(ctx, `INSERT INTO managed_realtime_commit_receipts(source_id,event_id,content_hash,endpoint_id,channel,sequence,published_at) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7)`, input.SourceID, input.EventID, realtimeCommitHash(input), input.EndpointID, input.Channel, message.Sequence, message.CreatedAt)
	return err
}
