package state

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	ManagedRealtimeDirectMessageReceiptRetention     = time.Hour
	ManagedRealtimeDirectMessageDispatchLease        = 2 * time.Minute
	ManagedRealtimeDirectMessageAckTimeout           = 30 * time.Second
	ManagedRealtimeDirectMessageMaxTargets           = 10_000
	ManagedRealtimeDirectMessageMaxReceipts          = 10_000
	ManagedRealtimeDirectMessageMaxDeliveries        = 1_000_000
	ManagedRealtimeDirectMessageReceiptPruneMaxBatch = 1_000
)

var (
	ErrManagedRealtimeDirectMessageInvalid  = errors.New("state: invalid managed realtime direct message receipt")
	ErrManagedRealtimeDirectMessageConflict = errors.New("state: direct message ID was used with different content")
	ErrManagedRealtimeDirectMessageLimit    = errors.New("state: managed realtime direct message receipt limit reached")
)

const (
	ManagedRealtimeDirectQueuePending     = "pending"
	ManagedRealtimeDirectQueueQueued      = "queued"
	ManagedRealtimeDirectQueueUnsupported = "unsupported"
	ManagedRealtimeDirectQueueFull        = "queue_full"
	ManagedRealtimeDirectQueueFailed      = "failed"

	ManagedRealtimeDirectStatusPending      = "pending"
	ManagedRealtimeDirectStatusAcknowledged = "acknowledged"
	ManagedRealtimeDirectStatusTimedOut     = "timed_out"
	ManagedRealtimeDirectStatusUnsupported  = "unsupported"
	ManagedRealtimeDirectStatusQueueFull    = "queue_full"
	ManagedRealtimeDirectStatusFailed       = "failed"
)

type ManagedRealtimeDirectMessageSummary struct {
	Recipients       int  `json:"recipients"`
	Queued           int  `json:"queued"`
	Unsupported      int  `json:"unsupported"`
	QueueFull        int  `json:"queue_full"`
	Failed           int  `json:"failed"`
	NodesQueried     int  `json:"nodes_queried"`
	NodesUnavailable int  `json:"nodes_unavailable"`
	Partial          bool `json:"partial"`
}

type ManagedRealtimeDirectMessageTarget struct {
	ConnectionID string
	AckSupported bool
}

type ManagedRealtimeDirectMessageDeliveryResult struct {
	ConnectionID string
	QueueStatus  string
}

type ManagedRealtimeDirectMessageDelivery struct {
	ConnectionID   string
	AckSupported   bool
	QueueStatus    string
	Status         string
	CreatedAt      time.Time
	QueuedAt       *time.Time
	AcknowledgedAt *time.Time
}

type ManagedRealtimeDirectMessageReceipt struct {
	EndpointID       string
	MessageID        string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	DispatchComplete bool
	Summary          ManagedRealtimeDirectMessageSummary
	Deliveries       []ManagedRealtimeDirectMessageDelivery
}

// ManagedRealtimeDirectMessageReceiptStore keeps short-lived receipt and
// per-connection acknowledgement state. Begin returns dispatch=true only to
// the request that owns the dispatch lease; inFlight means another request
// currently owns it. Reusing an ID with a different fingerprint is rejected.
type ManagedRealtimeDirectMessageReceiptStore interface {
	BeginManagedRealtimeDirectMessageReceipt(context.Context, string, string, []byte) (dispatch, inFlight bool, err error)
	RegisterManagedRealtimeDirectMessageTargets(context.Context, string, string, string, []ManagedRealtimeDirectMessageTarget) ([]string, error)
	UpdateManagedRealtimeDirectMessageDeliveries(context.Context, string, string, string, []ManagedRealtimeDirectMessageDeliveryResult) error
	AcknowledgeManagedRealtimeDirectMessage(context.Context, string, string, string, string) error
	CompleteManagedRealtimeDirectMessageReceipt(context.Context, string, string, ManagedRealtimeDirectMessageSummary) error
	GetManagedRealtimeDirectMessageReceipt(context.Context, string, string) (ManagedRealtimeDirectMessageReceipt, error)
}

type ManagedRealtimeDirectMessageReceiptReaper interface {
	PruneExpiredManagedRealtimeDirectMessageReceipts(context.Context, int) (int64, error)
}

type managedRealtimeDirectMessageKey struct {
	endpointID string
	messageID  string
}

type managedRealtimeDirectMessageState struct {
	fingerprint        []byte
	createdAt          time.Time
	expiresAt          time.Time
	dispatchLeaseUntil time.Time
	dispatchComplete   bool
	summary            ManagedRealtimeDirectMessageSummary
	deliveries         map[string]managedRealtimeDirectMessageDeliveryState
}

type managedRealtimeDirectMessageDeliveryState struct {
	connectionID   string
	nodeID         string
	ackSupported   bool
	queueStatus    string
	createdAt      time.Time
	queuedAt       *time.Time
	acknowledgedAt *time.Time
}

func validateManagedRealtimeDirectMessageReceipt(endpointID, messageID string, fingerprint []byte) error {
	if endpointID == "" || messageID == "" || len(messageID) > 128 || strings.TrimSpace(messageID) != messageID || len(fingerprint) != 32 {
		return ErrManagedRealtimeDirectMessageInvalid
	}
	for _, char := range messageID {
		if char <= 0x20 || char > 0x7e || strings.ContainsRune("/?#", char) {
			return ErrManagedRealtimeDirectMessageInvalid
		}
	}
	return nil
}

func validateManagedRealtimeDirectMessageNode(nodeID string) error {
	if nodeID == "" || strings.TrimSpace(nodeID) != nodeID || strings.ContainsAny(nodeID, "\x00\r\n") || len(nodeID) > 128 {
		return ErrManagedRealtimeDirectMessageInvalid
	}
	return nil
}

func validateManagedRealtimeDirectMessageTargets(targets []ManagedRealtimeDirectMessageTarget) error {
	if len(targets) > ManagedRealtimeDirectMessageMaxTargets {
		return ErrManagedRealtimeDirectMessageLimit
	}
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if target.ConnectionID == "" || len(target.ConnectionID) > 128 || strings.TrimSpace(target.ConnectionID) != target.ConnectionID || strings.ContainsAny(target.ConnectionID, "\x00\r\n") {
			return ErrManagedRealtimeDirectMessageInvalid
		}
		if _, ok := seen[target.ConnectionID]; ok {
			return ErrManagedRealtimeDirectMessageInvalid
		}
		seen[target.ConnectionID] = struct{}{}
	}
	return nil
}

func validateManagedRealtimeDirectMessageResults(results []ManagedRealtimeDirectMessageDeliveryResult) error {
	if len(results) > ManagedRealtimeDirectMessageMaxTargets {
		return ErrManagedRealtimeDirectMessageLimit
	}
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		if result.ConnectionID == "" || len(result.ConnectionID) > 128 || strings.TrimSpace(result.ConnectionID) != result.ConnectionID || strings.ContainsAny(result.ConnectionID, "\x00\r\n") {
			return ErrManagedRealtimeDirectMessageInvalid
		}
		switch result.QueueStatus {
		case ManagedRealtimeDirectQueueQueued, ManagedRealtimeDirectQueueUnsupported, ManagedRealtimeDirectQueueFull, ManagedRealtimeDirectQueueFailed:
		default:
			return ErrManagedRealtimeDirectMessageInvalid
		}
		if _, ok := seen[result.ConnectionID]; ok {
			return ErrManagedRealtimeDirectMessageInvalid
		}
		seen[result.ConnectionID] = struct{}{}
	}
	return nil
}

func managedRealtimeDirectDeliveryProjection(state managedRealtimeDirectMessageDeliveryState, now time.Time) ManagedRealtimeDirectMessageDelivery {
	status := state.queueStatus
	switch {
	case state.acknowledgedAt != nil:
		status = ManagedRealtimeDirectStatusAcknowledged
	case state.queueStatus == ManagedRealtimeDirectQueueFull:
		status = ManagedRealtimeDirectStatusQueueFull
	case state.queueStatus == ManagedRealtimeDirectQueueFailed:
		status = ManagedRealtimeDirectStatusFailed
	case !state.ackSupported:
		status = ManagedRealtimeDirectStatusUnsupported
	case state.queueStatus == ManagedRealtimeDirectQueueQueued && state.queuedAt != nil && !now.Before(state.queuedAt.Add(ManagedRealtimeDirectMessageAckTimeout)):
		status = ManagedRealtimeDirectStatusTimedOut
	case state.queueStatus == ManagedRealtimeDirectQueuePending && !now.Before(state.createdAt.Add(ManagedRealtimeDirectMessageAckTimeout)):
		status = ManagedRealtimeDirectStatusTimedOut
	default:
		status = ManagedRealtimeDirectStatusPending
	}
	return ManagedRealtimeDirectMessageDelivery{
		ConnectionID: state.connectionID, AckSupported: state.ackSupported,
		QueueStatus: state.queueStatus, Status: status, CreatedAt: state.createdAt,
		QueuedAt: cloneRealtimeReceiptTime(state.queuedAt), AcknowledgedAt: cloneRealtimeReceiptTime(state.acknowledgedAt),
	}
}

func cloneRealtimeReceiptTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

var (
	_ ManagedRealtimeDirectMessageReceiptStore  = (*PgStore)(nil)
	_ ManagedRealtimeDirectMessageReceiptStore  = (*MemStore)(nil)
	_ ManagedRealtimeDirectMessageReceiptReaper = (*PgStore)(nil)
	_ ManagedRealtimeDirectMessageReceiptReaper = (*MemStore)(nil)
)
