package state

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Initial bounded history limits. These are deliberately lower than the live
// frame limit: retained payloads consume durable control-plane storage.
const (
	ManagedRealtimeHistoryMaxPayloadBytes = 4 << 10
	ManagedRealtimeHistoryMaxMessages     = 1024
	ManagedRealtimeHistoryMaxChannels     = 32
	ManagedRealtimeHistoryMaxRead         = 100
	ManagedRealtimeHistoryRetention       = 24 * time.Hour
)

var ErrManagedRealtimeHistoryInvalid = errors.New("state: invalid managed realtime history request")
var ErrManagedRealtimeHistoryLimit = errors.New("state: managed realtime history channel limit reached")

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

// ManagedRealtimeHistoryReaper removes rows that have passed the retention
// window. Read and append enforce that window even before this pass runs.
type ManagedRealtimeHistoryReaper interface {
	PruneExpiredManagedRealtimeChannelMessages(context.Context, int) (int64, error)
}

var (
	_ ManagedRealtimeHistoryStore  = (*PgStore)(nil)
	_ ManagedRealtimeHistoryStore  = (*MemStore)(nil)
	_ ManagedRealtimeHistoryReaper = (*PgStore)(nil)
	_ ManagedRealtimeHistoryReaper = (*MemStore)(nil)
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
