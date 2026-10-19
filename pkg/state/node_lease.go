package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// NodeLeaseStore holds compute-node liveness leases (ADR-1344 §4, P3). The
// node-local schedd renews its node's lease; peers read it before treating a
// node as gone. Expiry is on database time, so every reader agrees on it.
type NodeLeaseStore interface {
	// RenewNodeLease renews holder's lease on nodeID for ttl, or takes the
	// lease over with the next epoch once the previous holder's lease has
	// expired. It returns ErrNodeLeaseHeld while another holder's lease is
	// still live.
	RenewNodeLease(ctx context.Context, nodeID, holder string, ttl time.Duration) (NodeLease, error)
	// GetNodeLease returns nodeID's lease, or ErrNotFound when the node has
	// never held one. A missing lease means "unknown", never "expired".
	GetNodeLease(ctx context.Context, nodeID string) (NodeLease, error)
}

// NodeLease is a compute node's liveness lease as of DBNow.
type NodeLease struct {
	NodeID    string
	Holder    string
	Epoch     int64
	RenewedAt time.Time
	ExpiresAt time.Time
	// DBNow is the database clock when the lease was read, so callers judge
	// expiry against the same clock that set it.
	DBNow time.Time
}

// ExpiredFor reports whether the lease expired at least margin ago by
// database time.
func (l NodeLease) ExpiredFor(margin time.Duration) bool {
	return !l.ExpiresAt.IsZero() && l.DBNow.Sub(l.ExpiresAt) >= margin
}

// ErrNodeLeaseHeld is returned when another holder's lease on the node is
// still live.
var ErrNodeLeaseHeld = fmt.Errorf("node lease held by another holder: %w", ErrConflict)

// MaxNodeLeaseTTL bounds a lease so a bad configuration cannot make a dead
// node look alive for long.
const MaxNodeLeaseTTL = api.NodeLeaseMaxTTL

func validateNodeLeaseRenewal(nodeID, holder string, ttl time.Duration) error {
	if err := validateSnapshotRecoveryIDs(nodeID); err != nil {
		return err
	}
	if strings.TrimSpace(holder) == "" || len(holder) > 255 {
		return errors.New("state: node lease holder must be 1-255 characters")
	}
	if ttl < time.Second || ttl > MaxNodeLeaseTTL {
		return fmt.Errorf("state: node lease ttl %s outside [1s, %s]", ttl, MaxNodeLeaseTTL)
	}
	return nil
}
