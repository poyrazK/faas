package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Initial bounded history limits. These are deliberately lower than the live
// frame limit: retained payloads consume durable control-plane storage.
const (
	ManagedRealtimeHistoryMaxPayloadBytes      = 4 << 10
	ManagedRealtimeHistoryMaxMessages          = 1024
	ManagedRealtimeHistoryMaxChannels          = 32
	ManagedRealtimeHistoryMaxRead              = 100
	ManagedRealtimeHistoryRetention            = 24 * time.Hour
	ManagedRealtimeDurableCursorMaxPerEndpoint = 256
	ManagedRealtimeDurableCursorRetention      = 30 * 24 * time.Hour
)

var ErrManagedRealtimeHistoryInvalid = errors.New("state: invalid managed realtime history request")
var ErrManagedRealtimeHistoryLimit = errors.New("state: managed realtime history channel limit reached")
var ErrManagedRealtimeDurableCursorInvalid = errors.New("state: invalid managed realtime durable cursor")
var ErrManagedRealtimeDurableCursorLimit = errors.New("state: managed realtime durable cursor limit reached")
var ErrManagedRealtimeDurableCursorExpired = errors.New("state: managed realtime durable cursor is outside retained history")

// ManagedRealtimeChannelMessage is one committed outbound channel message.
// Sequence is scoped to endpoint and channel; it is never a connection ID or
// the inbound callback sequence used by realtimed.
type ManagedRealtimeChannelMessage struct {
	Metadata                map[string]string `json:"metadata,omitempty"`
	TargetMessageID         string
	Version                 int64
	MessageEvent            string
	Deleted                 bool
	FallbackAfterSeconds    int    `json:"-"`
	NotificationNotBefore   string `json:"-"`
	NotificationCollapseKey string `json:"-"`
	NotificationTTLSeconds  int    `json:"-"`
	NotificationPriority    string `json:"-"`
	NotificationGroupKey    string `json:"-"`
	NotificationGroupLabel  string `json:"-"`
	NotificationCategory    string `json:"-"`
	EndpointID              string
	Channel                 string
	Sequence                int64
	Data                    []byte
	Binary                  bool
	IdempotencyKey          string
	CreatedAt               time.Time
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

// ManagedRealtimeDurableCursorStore stores one checkpoint for each named
// logical consumer and channel. Consumer names must be distinct across
// devices that need independent progress. Load creates a missing cursor at
// initialSequence; an existing cursor always takes precedence. Advance is
// monotonic, while Reset is an explicit recovery operation bounded by retained
// channel history.
type ManagedRealtimeDurableCursorStore interface {
	LoadManagedRealtimeDurableCursor(context.Context, string, string, string, string, int64) (int64, error)
	AdvanceManagedRealtimeDurableCursor(context.Context, string, string, string, string, int64) (int64, error)
	ResetManagedRealtimeDurableCursor(context.Context, string, string, string, string, int64) (int64, error)
}

type ManagedRealtimeDurableCursorReaper interface {
	PruneExpiredManagedRealtimeDurableCursors(context.Context, int) (int64, error)
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
	_ ManagedRealtimeDurableCursorStore     = (*PgStore)(nil)
	_ ManagedRealtimeDurableCursorStore     = (*MemStore)(nil)
	_ ManagedRealtimeDurableCursorReaper    = (*PgStore)(nil)
	_ ManagedRealtimeDurableCursorReaper    = (*MemStore)(nil)
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

func validateManagedRealtimeDurableCursor(endpointID, principal, subscription, channel string, sequence int64) error {
	if validateManagedRealtimeHistoryRequest(endpointID, channel) != nil ||
		principal == "" || len(principal) > 256 || strings.TrimSpace(principal) != principal ||
		subscription == "" || len(subscription) > 128 || strings.TrimSpace(subscription) != subscription ||
		strings.ContainsAny(principal+subscription, "\x00\r\n") || sequence < 0 {
		return ErrManagedRealtimeDurableCursorInvalid
	}
	return nil
}

type ManagedRealtimeMetadataStore interface {
	AppendManagedRealtimeChannelMetadata(context.Context, string, string, []byte, bool, string, map[string]string) (ManagedRealtimeChannelMessage, error)
}

func metadataJSON(v map[string]string) []byte {
	if v == nil {
		return []byte("{}")
	}
	raw, _ := json.Marshal(v)
	return raw
}
func equalRealtimeMetadata(a, b map[string]string) bool {
	return string(metadataJSON(a)) == string(metadataJSON(b))
}
func cloneRealtimeMetadata(v map[string]string) map[string]string {
	if v == nil {
		return nil
	}
	out := map[string]string{}
	for k, value := range v {
		out[k] = value
	}
	return out
}
