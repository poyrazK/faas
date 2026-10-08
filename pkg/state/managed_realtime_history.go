package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Initial bounded history limits. These are deliberately lower than the live
// frame limit: retained payloads consume durable control-plane storage.
const (
	ManagedRealtimeHistoryMaxPayloadBytes = api.RealtimeHistoryMaxPayloadBytes
	ManagedRealtimeHistoryMaxMessages     = api.RealtimeHistoryMaxMessagesPerChannel
	ManagedRealtimeHistoryMaxChannels     = api.RealtimeHistoryMaxChannelsPerEndpoint
	ManagedRealtimeHistoryMaxRead         = 100
	ManagedRealtimeHistoryRetention       = 24 * time.Hour
)

var ErrManagedRealtimeHistoryInvalid = errors.New("state: invalid managed realtime history request")
var ErrManagedRealtimeHistoryLimit = errors.New("state: managed realtime history channel limit reached")

// ManagedRealtimeHistoryQuotaError reports an append that would cross the
// account's retained payload cap.
type ManagedRealtimeHistoryQuotaError struct {
	LimitBytes     int64
	UsedBytes      int64
	RequestedBytes int64
}

func (e *ManagedRealtimeHistoryQuotaError) Error() string {
	return fmt.Sprintf("state: retained realtime payload quota exceeded: limit=%d used=%d requested=%d", e.LimitBytes, e.UsedBytes, e.RequestedBytes)
}

// ManagedRealtimeChannelMessage is one committed outbound channel message.
// Sequence is scoped to endpoint and channel; it is never a connection ID or
// the inbound callback sequence used by realtimed.
type ManagedRealtimeChannelMessage struct {
	EndpointID     string
	Channel        string
	Sequence       int64
	Data           []byte
	Binary         bool
	IdempotencyKey string
	CreatedAt      time.Time
}

// ManagedRealtimeChannelHistory describes a consistent page of the channel
// log. HistoryUnavailable is true if AfterSequence precedes retained history;
// callers must resynchronize instead of silently returning a partial page.
type ManagedRealtimeChannelHistory struct {
	Messages           []ManagedRealtimeChannelMessage
	OldestSequence     int64
	LatestSequence     int64
	HistoryUnavailable bool
}

// ManagedRealtimeHistoryStore is separate from the live connection directory.
// Implementations must serialize concurrent appends for one channel and commit
// the message before reporting its sequence to a publisher.
type ManagedRealtimeHistoryStore interface {
	AppendManagedRealtimeChannelMessage(context.Context, string, string, []byte, bool, string) (ManagedRealtimeChannelMessage, error)
	ReadManagedRealtimeChannelHistory(context.Context, string, string, int64, int) (ManagedRealtimeChannelHistory, error)
}

// ManagedRealtimeHistoryQuotaStore atomically enforces an account payload cap
// while appending retained messages.
type ManagedRealtimeHistoryQuotaStore interface {
	AppendManagedRealtimeChannelMessageWithQuota(context.Context, string, string, string, []byte, bool, string, int64) (ManagedRealtimeChannelMessage, error)
}

// ManagedRealtimeHistoryReaper removes rows that have passed the retention
// window. Read and append enforce that window even before this pass runs.
type ManagedRealtimeHistoryReaper interface {
	PruneExpiredManagedRealtimeChannelMessages(context.Context, int) (int64, error)
}

// ManagedRealtimeHistoryStorageStats measures physical PostgreSQL relation
// size, including indexes and dead tuples awaiting vacuum. It is an operator
// capacity observation, not a customer billing quantity.
type ManagedRealtimeHistoryStorageStats struct {
	HeadsRelationBytes    int64
	MessagesRelationBytes int64
	UsageRelationBytes    int64
}

type ManagedRealtimeHistoryStorageObserver interface {
	ObserveManagedRealtimeHistoryStorage(context.Context) (ManagedRealtimeHistoryStorageStats, error)
}

// ManagedRealtimeHistoryUsage is an account-scoped point-in-time count of
// retained rows and payload bytes. Stored includes expired rows awaiting
// cleanup; Replayable applies the same contiguous expiry floor as history
// reads. Bytes exclude row and index overhead and are not billing meters.
type ManagedRealtimeHistoryUsage struct {
	ObservedAt             time.Time
	EndpointCount          int64
	ChannelCount           int64
	StoredMessageCount     int64
	StoredPayloadBytes     int64
	ReplayableMessageCount int64
	ReplayablePayloadBytes int64
}

type ManagedRealtimeHistoryUsageReader interface {
	ReadManagedRealtimeHistoryUsage(context.Context, string) (ManagedRealtimeHistoryUsage, error)
}

var (
	_ ManagedRealtimeHistoryStore           = (*PgStore)(nil)
	_ ManagedRealtimeHistoryStore           = (*MemStore)(nil)
	_ ManagedRealtimeHistoryQuotaStore      = (*PgStore)(nil)
	_ ManagedRealtimeHistoryQuotaStore      = (*MemStore)(nil)
	_ ManagedRealtimeHistoryReaper          = (*PgStore)(nil)
	_ ManagedRealtimeHistoryReaper          = (*MemStore)(nil)
	_ ManagedRealtimeHistoryStorageObserver = (*PgStore)(nil)
	_ ManagedRealtimeHistoryUsageReader     = (*PgStore)(nil)
	_ ManagedRealtimeHistoryUsageReader     = (*MemStore)(nil)
)

func validateManagedRealtimeHistoryRequest(endpointID, channel string) error {
	if endpointID == "" || channel == "" || len(channel) > 256 ||
		strings.TrimSpace(channel) != channel || strings.ContainsAny(channel, "/?#\r\n") {
		return ErrManagedRealtimeHistoryInvalid
	}
	return nil
}

func validateManagedRealtimeHistoryAppend(endpointID, channel string, data []byte, idempotencyKey string) error {
	if err := validateManagedRealtimeHistoryRequest(endpointID, channel); err != nil {
		return err
	}
	if len(data) > ManagedRealtimeHistoryMaxPayloadBytes || len(idempotencyKey) > 128 {
		return ErrManagedRealtimeHistoryInvalid
	}
	return nil
}

func validateManagedRealtimeHistoryRead(endpointID, channel string, after int64, limit int) error {
	if err := validateManagedRealtimeHistoryRequest(endpointID, channel); err != nil {
		return err
	}
	if after < 0 || limit < 1 || limit > ManagedRealtimeHistoryMaxRead {
		return ErrManagedRealtimeHistoryInvalid
	}
	return nil
}
