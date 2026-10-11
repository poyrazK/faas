package realtime

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	ManagedRealtimePresenceStateMaxBytes = 512
	ManagedRealtimeSignalDataMaxBytes    = 2 << 10
)

// PresenceMember is the public presence projection shared across realtime
// nodes. Connection and node identifiers stay in the private control plane.
type PresenceMember struct {
	MemberID        string          `json:"member_id"`
	State           json.RawMessage `json:"state"`
	ConnectionCount int             `json:"connection_count,omitempty"`
	UpdatedAt       time.Time       `json:"updated_at,omitempty"`
}

// EphemeralFrame is a transient v2 presence or signal event. It is never
// written to retained channel history.
type EphemeralFrame struct {
	Inbox              bool            `json:"inbox,omitempty"`
	Unread             int64           `json:"unread,omitempty"`
	HistoryUnavailable bool            `json:"history_unavailable,omitempty"`
	Sequence           int64           `json:"sequence,omitempty"`
	OldestSequence     int64           `json:"oldest_sequence,omitempty"`
	LatestSequence     int64           `json:"latest_sequence,omitempty"`
	Name               string          `json:"name,omitempty"`
	ExpiresAt          *time.Time      `json:"expires_at,omitempty"`
	Type               string          `json:"type"`
	Event              string          `json:"event,omitempty"`
	MemberID           string          `json:"member_id,omitempty"`
	State              json.RawMessage `json:"state,omitempty"`
	Data               json.RawMessage `json:"data,omitempty"`
	ConnectionCount    int             `json:"connection_count,omitempty"`
	UpdatedAt          time.Time       `json:"updated_at,omitempty"`
}

// ManagedRealtimeFleetClient is the private apid seam for leased presence and
// node-to-node ephemeral delivery. It remains optional for embedded managers
// that intentionally run as a single process.
type ManagedRealtimeFleetClient interface {
	UpsertPresence(context.Context, string, string, string, string, string, json.RawMessage) (string, []PresenceMember, error)
	DeletePresence(context.Context, string, string, string) ([]PresenceMember, time.Time, error)
	ReadPresenceSnapshot(context.Context, string, string) ([]PresenceMember, error)
	RelayEphemeral(context.Context, string, string, EphemeralFrame) error
}

// ManagedRealtimeDirectMessageReceiptClient is an optional private apid seam
// for registering recipient snapshots and persisting v2 client acknowledgments.
type ManagedRealtimeDirectMessageReceiptClient interface {
	RegisterDirectMessageTargets(context.Context, string, string, []state.ManagedRealtimeDirectMessageTarget) ([]string, error)
	UpdateDirectMessageDeliveries(context.Context, string, string, []state.ManagedRealtimeDirectMessageDeliveryResult) error
	AcknowledgeDirectMessage(context.Context, string, string, string) error
}

// ValidateEphemeralFrame reports whether a control-plane relay contains a
// supported transient v2 event.
func ValidateEphemeralFrame(frame EphemeralFrame) bool {
	if frame.MemberID == "" || len(frame.MemberID) > 128 || strings.ContainsAny(frame.MemberID, "\x00\r\n") {
		return false
	}
	if frame.Type != "read_receipt" && (frame.Inbox || frame.Sequence != 0 || frame.Unread != 0 || frame.OldestSequence != 0 || frame.LatestSequence != 0 || frame.HistoryUnavailable) {
		return false
	}
	switch frame.Type {
	case "read_receipt":
		return validReadReader(frame.MemberID) && frame.Sequence >= 0 && frame.Sequence <= frame.LatestSequence && frame.OldestSequence >= 1 && frame.OldestSequence <= frame.LatestSequence+1 && frame.Unread >= 0 && frame.Unread <= 1024 && frame.Event == "" && len(frame.Data) == 0 && len(frame.State) == 0 && frame.Name == "" && frame.ExpiresAt == nil && frame.ConnectionCount == 0

	case "presence":
		if frame.Name != "" || frame.ExpiresAt != nil {
			return false
		}
		switch frame.Event {
		case "joined", "updated":
			var state map[string]json.RawMessage
			return len(frame.State) > 0 && len(frame.State) <= ManagedRealtimePresenceStateMaxBytes &&
				json.Valid(frame.State) && json.Unmarshal(frame.State, &state) == nil && state != nil &&
				frame.ConnectionCount >= 1 && frame.ConnectionCount <= 512 && len(frame.Data) == 0
		case "left":
			return len(frame.State) == 0 && len(frame.Data) == 0 && frame.ConnectionCount == 0
		default:
			return false
		}
	case "signal":
		return validSignalExpiry(frame.Name, frame.UpdatedAt, frame.ExpiresAt) && frame.Event == "" && len(frame.Data) > 0 && len(frame.Data) <= ManagedRealtimeSignalDataMaxBytes &&
			json.Valid(frame.Data) && len(frame.State) == 0 && frame.ConnectionCount == 0
	default:
		return false
	}
}
