package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

const (
	ManagedRealtimeInboxMaxPrincipals            = 256
	ManagedRealtimeInboxMaxMessages              = 256
	ManagedRealtimeInboxMaxConsumers             = 1024
	ManagedRealtimeInboxMaxConsumersPerPrincipal = 16
	ManagedRealtimeInboxRetention                = 24 * time.Hour
	ManagedRealtimeInboxCursorRetention          = 30 * 24 * time.Hour
)

// ManagedRealtimeInboxStore retains one ordered stream per principal and a
// checkpoint for each named device. Stream keys never enter public channel APIs.
// IdempotencyKey on each returned message is the caller-supplied message ID.
type ManagedRealtimeInboxStore interface {
	AppendManagedRealtimeInboxMessage(context.Context, string, string, []byte, bool, string) (ManagedRealtimeChannelMessage, error)
	ReadManagedRealtimeInbox(context.Context, string, string, int64, int) (ManagedRealtimeChannelHistory, error)
	LoadManagedRealtimeInboxCursor(context.Context, string, string, string, int64) (int64, error)
	AdvanceManagedRealtimeInboxCursor(context.Context, string, string, string, int64) (int64, error)
	ResetManagedRealtimeInboxCursor(context.Context, string, string, string, int64) (int64, error)
	GetManagedRealtimeInboxCursor(context.Context, string, string, string) (int64, error)
}

type ManagedRealtimeInboxReaper interface {
	PruneExpiredManagedRealtimeInboxMessages(context.Context, int) (int64, error)
	PruneExpiredManagedRealtimeInboxCursors(context.Context, int) (int64, error)
}

func managedRealtimeInboxKey(principal string) (string, error) {
	if strings.TrimSpace(principal) == "" || len(principal) > 256 || strings.ContainsAny(principal, "\x00\r\n") {
		return "", ErrManagedRealtimeHistoryInvalid
	}
	digest := sha256.Sum256([]byte(principal))
	return hex.EncodeToString(digest[:]), nil
}

func validateManagedRealtimeInboxMessageID(messageID string) error {
	if messageID == "" || len(messageID) > 128 {
		return ErrManagedRealtimeHistoryInvalid
	}
	for _, char := range messageID {
		if char <= 0x20 || char > 0x7e || strings.ContainsRune("/?#", char) {
			return ErrManagedRealtimeHistoryInvalid
		}
	}
	return nil
}

func (s *PgStore) AppendManagedRealtimeInboxMessage(ctx context.Context, endpointID, principal string, data []byte, binary bool, messageID string) (ManagedRealtimeChannelMessage, error) {
	key, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	if err := validateManagedRealtimeInboxMessageID(messageID); err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	if err := ctx.Err(); err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	return s.appendManagedRealtimeInboxMessage(ctx, endpointID, key, data, binary, messageID, 0)
}

func (s *PgStore) ReadManagedRealtimeInbox(ctx context.Context, endpointID, principal string, after int64, limit int) (ManagedRealtimeChannelHistory, error) {
	key, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return ManagedRealtimeChannelHistory{}, err
	}
	if err := ctx.Err(); err != nil {
		return ManagedRealtimeChannelHistory{}, err
	}
	return s.readManagedRealtimeInbox(ctx, endpointID, key, after, limit)
}

func (s *PgStore) LoadManagedRealtimeInboxCursor(ctx context.Context, endpointID, principal, consumer string, sequence int64) (int64, error) {
	key, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return 0, err
	}
	return s.loadManagedRealtimeInboxCursor(ctx, endpointID, key, consumer, key, sequence)
}

func (s *PgStore) AdvanceManagedRealtimeInboxCursor(ctx context.Context, endpointID, principal, consumer string, sequence int64) (int64, error) {
	key, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return 0, err
	}
	return s.advanceManagedRealtimeInboxCursor(ctx, endpointID, key, consumer, key, sequence)
}

func (s *PgStore) ResetManagedRealtimeInboxCursor(ctx context.Context, endpointID, principal, consumer string, sequence int64) (int64, error) {
	key, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return 0, err
	}
	return s.resetManagedRealtimeInboxCursor(ctx, endpointID, key, consumer, key, sequence)
}

func (s *MemStore) AppendManagedRealtimeInboxMessage(ctx context.Context, endpointID, principal string, data []byte, binary bool, messageID string) (ManagedRealtimeChannelMessage, error) {
	key, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	if err := validateManagedRealtimeInboxMessageID(messageID); err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	if err := ctx.Err(); err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	return s.appendManagedRealtimeInboxMessage(ctx, endpointID, key, data, binary, messageID, 0)
}

func (s *MemStore) ReadManagedRealtimeInbox(ctx context.Context, endpointID, principal string, after int64, limit int) (ManagedRealtimeChannelHistory, error) {
	key, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return ManagedRealtimeChannelHistory{}, err
	}
	if err := ctx.Err(); err != nil {
		return ManagedRealtimeChannelHistory{}, err
	}
	return s.readManagedRealtimeInbox(ctx, endpointID, key, after, limit)
}

func (s *MemStore) LoadManagedRealtimeInboxCursor(ctx context.Context, endpointID, principal, consumer string, sequence int64) (int64, error) {
	key, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return 0, err
	}
	return s.loadManagedRealtimeInboxCursor(ctx, endpointID, key, consumer, key, sequence)
}

func (s *MemStore) AdvanceManagedRealtimeInboxCursor(ctx context.Context, endpointID, principal, consumer string, sequence int64) (int64, error) {
	key, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return 0, err
	}
	return s.advanceManagedRealtimeInboxCursor(ctx, endpointID, key, consumer, key, sequence)
}

func (s *MemStore) ResetManagedRealtimeInboxCursor(ctx context.Context, endpointID, principal, consumer string, sequence int64) (int64, error) {
	key, err := managedRealtimeInboxKey(principal)
	if err != nil {
		return 0, err
	}
	return s.resetManagedRealtimeInboxCursor(ctx, endpointID, key, consumer, key, sequence)
}

var (
	_ ManagedRealtimeInboxStore  = (*PgStore)(nil)
	_ ManagedRealtimeInboxStore  = (*MemStore)(nil)
	_ ManagedRealtimeInboxReaper = (*PgStore)(nil)
	_ ManagedRealtimeInboxReaper = (*MemStore)(nil)
)
