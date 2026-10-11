package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	ManagedRealtimePresenceStateMaxBytes = 512
	// Max connection leases per endpoint/channel. Principal scope may expose
	// fewer visible members by aggregating multiple leases.
	ManagedRealtimePresenceMaxMembers = 512
	ManagedRealtimePresenceLeaseTTL   = 45 * time.Second
)

var ErrManagedRealtimePresenceInvalid = errors.New("state: invalid managed realtime presence lease")
var ErrManagedRealtimePresenceLimit = errors.New("state: managed realtime channel presence limit reached")

// ManagedRealtimePresenceLease records one live connection. Principal-scoped
// connections share a member ID and are aggregated in snapshot reads. Presence
// state is short-lived and separate from retained channel history.
type ManagedRealtimePresenceLease struct {
	EndpointID   string
	Channel      string
	NodeID       string
	ConnectionID string
	MemberID     string
	// Principal is private grouping metadata. It is never included in the
	// client-facing presence projection.
	Principal       string
	State           []byte
	ExpiresAt       time.Time
	UpdatedAt       time.Time
	StateUpdatedAt  time.Time
	ConnectionCount int
}

// ManagedRealtimePresenceStore holds fleet presence state bounded by active
// connection leases. Expired leases must never be returned by a snapshot read.
type ManagedRealtimePresenceStore interface {
	UpsertManagedRealtimePresenceLease(context.Context, ManagedRealtimePresenceLease) ([]ManagedRealtimePresenceLease, error)
	ReadManagedRealtimePresenceSnapshot(context.Context, string, string) ([]ManagedRealtimePresenceLease, error)
	DeleteManagedRealtimePresenceLease(context.Context, string, string, string, string) ([]ManagedRealtimePresenceLease, time.Time, error)
}

type ManagedRealtimePresenceReaper interface {
	PruneExpiredManagedRealtimePresenceLeases(context.Context, int) (int64, error)
}

type managedRealtimePresenceKey struct {
	endpointID   string
	channel      string
	nodeID       string
	connectionID string
}

func validateManagedRealtimePresenceLease(lease ManagedRealtimePresenceLease) error {
	if validateManagedRealtimeHistoryRequest(lease.EndpointID, lease.Channel) != nil ||
		lease.NodeID == "" || lease.ConnectionID == "" || lease.MemberID == "" ||
		strings.TrimSpace(lease.NodeID) != lease.NodeID || strings.TrimSpace(lease.ConnectionID) != lease.ConnectionID ||
		strings.TrimSpace(lease.MemberID) != lease.MemberID || strings.ContainsAny(lease.NodeID+lease.ConnectionID+lease.MemberID, "\x00\r\n") ||
		strings.TrimSpace(lease.Principal) != lease.Principal || strings.ContainsAny(lease.Principal, "\x00\r\n") ||
		len(lease.ConnectionID) > 128 || len(lease.MemberID) > 128 || len(lease.Principal) > 256 || len(lease.State) > ManagedRealtimePresenceStateMaxBytes ||
		!json.Valid(lease.State) || lease.ExpiresAt.IsZero() || lease.ExpiresAt.Before(time.Now().UTC()) ||
		lease.ExpiresAt.After(time.Now().UTC().Add(2*ManagedRealtimePresenceLeaseTTL)) {
		return ErrManagedRealtimePresenceInvalid
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(lease.State, &state); err != nil || state == nil {
		return ErrManagedRealtimePresenceInvalid
	}
	return nil
}

func validateManagedRealtimePresenceKey(endpointID, channel, nodeID, connectionID string) error {
	if validateManagedRealtimeHistoryRequest(endpointID, channel) != nil || nodeID == "" || connectionID == "" ||
		strings.TrimSpace(nodeID) != nodeID || strings.TrimSpace(connectionID) != connectionID ||
		strings.ContainsAny(nodeID+connectionID, "\x00\r\n") || len(connectionID) > 128 {
		return ErrManagedRealtimePresenceInvalid
	}
	return nil
}

var (
	_ ManagedRealtimePresenceStore  = (*PgStore)(nil)
	_ ManagedRealtimePresenceStore  = (*MemStore)(nil)
	_ ManagedRealtimePresenceReaper = (*PgStore)(nil)
	_ ManagedRealtimePresenceReaper = (*MemStore)(nil)
)
