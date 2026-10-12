// Package dbsched is schedd's database scheduler (ADR-1027): it places native
// managed databases on database nodes, wakes them on demand within each
// node's RAM ceiling, parks idle ones, and deletes them. It is the only writer
// of database_vms; vmmd on the database node runs the VMs.
package dbsched

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// State is a database VM's runtime state (database_vms.state).
type State string

const (
	StateProvisioning State = "provisioning"
	StateParked       State = "parked"
	StateWaking       State = "waking"
	StateRunning      State = "running"
	StateParking      State = "parking"
	StateDeleting     State = "deleting"
	StateFailed       State = "failed"
)

// Node lifecycles (database_nodes.lifecycle).
const (
	NodeActive      = "active"
	NodeDraining    = "draining"
	NodeUnavailable = "unavailable"
)

var (
	ErrNotFound        = errors.New("dbsched: database not found")
	ErrNoCapacity      = errors.New("dbsched: no database node has capacity")
	ErrConflict        = errors.New("dbsched: database is in another state")
	ErrNodeUnavailable = errors.New("dbsched: database node unavailable")
)

// Node is one database node.
type Node struct {
	ID                  string
	TargetURL           string
	AdmissionCeilingMB  int
	VolumeCapacityBytes int64
	Lifecycle           string
}

// VM is the runtime record of one native database.
type VM struct {
	DatabaseID     string
	NodeID         string
	Major          int
	VcpuCount      int
	MemMiB         int
	VolumeBytes    int64
	State          State
	Bootstrapped   bool
	LastError      string
	LastActiveAt   time.Time
	StateChangedAt time.Time
	CreatedAt      time.Time
	// NodeTarget is the node's vmmd dial target. The scheduler fills it on
	// Wake and Get; stores do not persist it.
	NodeTarget string
}

// TransitionOptions annotate a state change.
type TransitionOptions struct {
	// LastError is a stable error code, or empty to clear it.
	LastError string
	// Bootstrapped marks the volume as initialized; it never reverts.
	Bootstrapped bool
}

// Store persists database nodes and VMs. Implementations must make Place
// and AdmitWake safe across concurrent schedd replicas.
type Store interface {
	UpsertNode(ctx context.Context, node Node) error
	Nodes(ctx context.Context) ([]Node, error)
	Get(ctx context.Context, id string) (VM, error)
	List(ctx context.Context, states ...State) ([]VM, error)
	// Place inserts vm as provisioning on the active node with the most
	// free volume capacity that fits it. An existing row is returned
	// unchanged, so provisioning retries converge.
	Place(ctx context.Context, vm VM) (VM, error)
	// AdmitWake moves a parked database to waking if its node's RAM
	// ceiling admits the VM's billable memory. A database already waking
	// or running is returned unchanged.
	AdmitWake(ctx context.Context, id string) (VM, error)
	// Transition changes state only from one of from (compare-and-set).
	Transition(ctx context.Context, id string, from []State, to State, opts TransitionOptions) error
	// Touch records activity on a running database.
	Touch(ctx context.Context, id string, at time.Time) error
	// Delete removes a database that is deleting.
	Delete(ctx context.Context, id string) error
}

// activeStates hold RAM on their node.
var activeStates = []State{StateWaking, StateRunning, StateParking}

func isActive(s State) bool {
	for _, a := range activeStates {
		if s == a {
			return true
		}
	}
	return false
}

// billableMB is what a running database VM charges against its node's
// admission ceiling: guest memory plus the per-VM overhead.
func billableMB(memMiB int) int64 {
	return int64(api.BillableRAMMB(memMiB))
}

// nodeUsage is the committed volume and active memory on one node.
type nodeUsage struct {
	volumeBytes int64
	activeMB    int64
}

// pickNode chooses the active node with the most free volume capacity that
// still fits volumeBytes. Ties break on node id for determinism.
func pickNode(nodes []Node, usage map[string]nodeUsage, volumeBytes int64) (Node, bool) {
	var best Node
	var bestFree int64 = -1
	for _, n := range nodes {
		if n.Lifecycle != NodeActive {
			continue
		}
		free := n.VolumeCapacityBytes - usage[n.ID].volumeBytes
		if free < volumeBytes {
			continue
		}
		if free > bestFree || (free == bestFree && n.ID < best.ID) {
			best, bestFree = n, free
		}
	}
	return best, bestFree >= 0
}

// admits reports whether node can take another VM of memMiB.
func admits(node Node, usage nodeUsage, memMiB int) bool {
	return usage.activeMB+billableMB(memMiB) <= int64(node.AdmissionCeilingMB)
}
