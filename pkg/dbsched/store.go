// Package dbsched is schedd's database scheduler (ADR-1027): it places native
// managed databases on database nodes, wakes them on demand within each
// node's RAM ceiling, parks idle ones, and deletes them. It is the only writer
// of database_vms; vmmd on the database node runs the VMs.
package dbsched

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	// StateMoving: the parked database's volume is being copied to
	// VM.MoveNode (move.go). It cannot wake, park or be deleted meanwhile.
	StateMoving State = "moving"
	// StateUpgrading: the database's VM runs pg_upgrade to VM.Upgrade.To
	// (upgrade.go). It cannot wake, park or move meanwhile.
	StateUpgrading State = "upgrading"
	StateDeleting  State = "deleting"
	StateFailed    State = "failed"
)

// Node lifecycles (database_nodes.lifecycle).
const (
	NodeActive      = "active"
	NodeDraining    = "draining"
	NodeUnavailable = "unavailable"
)

var (
	ErrNotFound   = errors.New("dbsched: database not found")
	ErrNoCapacity = errors.New("dbsched: no database node has capacity")
	// ErrBootSlotsBusy: the node already boots as many databases as it
	// has boot slots. It clears as those boots finish.
	ErrBootSlotsBusy   = errors.New("dbsched: database node boot slots are busy")
	ErrConflict        = errors.New("dbsched: database is in another state")
	ErrNodeUnavailable = errors.New("dbsched: database node unavailable")
	// ErrInvalid reports a request that cannot be carried out as asked.
	ErrInvalid = errors.New("dbsched: invalid request")
)

// Node is one database node.
type Node struct {
	ID                  string
	TargetURL           string
	AdmissionCeilingMB  int
	VolumeCapacityBytes int64
	// BootSlots caps the databases booting on the node at once; zero
	// stores DefaultBootSlots.
	BootSlots int
	// Majors are the PostgreSQL majors the node has base drives for;
	// empty means any.
	Majors    []int
	Lifecycle string
}

// offers reports whether the node can run a database of major.
func (n Node) offers(major int) bool {
	return len(n.Majors) == 0 || slices.Contains(n.Majors, major)
}

// DefaultBootSlots is a node's boot slots when none are configured.
const DefaultBootSlots = 4

// VM is the runtime record of one native database.
type VM struct {
	DatabaseID   string
	NodeID       string
	Major        int
	VcpuCount    int
	MemMiB       int
	VolumeBytes  int64
	State        State
	Bootstrapped bool
	// AlwaysOn databases are never parked for idleness; the keep-warm sweep
	// wakes them when found parked (scale_to_zero = false).
	AlwaysOn bool
	// LastBackupAt is the last successful base backup (zero: none).
	LastBackupAt time.Time
	// RestoreWindowSeconds is how far back the database may be restored.
	RestoreWindowSeconds int64
	// RestoreSource and RestoreTarget record a point-in-time restore: the
	// database recovered from and the instant. Both are empty otherwise.
	RestoreSource string
	RestoreTarget time.Time
	// RestoreRecoveredTo is the restore's last replayed commit, recorded
	// when it finished (zero: none replayed, or not recorded).
	RestoreRecoveredTo time.Time
	// ResizePending: the shape or volume changed and the VM has not been
	// restarted on it yet.
	ResizePending bool
	// MoveNode is where a moving database is going; MoveLeaseUntil is the
	// lease of the schedd replica moving it.
	MoveNode       string
	MoveLeaseUntil time.Time
	// Replicated databases keep a hot standby with its own volume on
	// StandbyNode (ADR-1027 production class; standby.go). StandbyRunning:
	// the standby's VM runs there. StandbySynced: the standby holds every
	// acknowledged commit and may be promoted; it is cleared before a
	// primary may commit without waiting for its standby. PromotePending:
	// the volume on NodeID is a standby promoted while parked; its next
	// boot promotes it onto a new timeline. DropStandbySlot: replication
	// was turned off and the primary still holds the standby's slot, which
	// the standby monitor drops once the primary runs.
	Replicated      bool
	StandbyNode     string
	StandbyRunning  bool
	StandbySynced   bool
	PromotePending  bool
	DropStandbySlot bool
	// RestoreUntilEnd lets a restore boot end at the end of the archive.
	// The restore job sets it per boot; stores do not persist it.
	RestoreUntilEnd bool
	// Upgrade is the database's current or last in-place major upgrade
	// (upgrade.go); an upgrading database holds the lease
	// UpgradeLeaseUntil. MajorHistory is the majors it ran before, oldest
	// first.
	Upgrade           Upgrade
	UpgradeLeaseUntil time.Time
	MajorHistory      []MajorSpan
	// UpgradeFrom boots the volume's cluster of that older major and
	// upgrades it to Major. The upgrade sets it per boot; stores do not
	// persist it.
	UpgradeFrom    int
	LastError      string
	LastActiveAt   time.Time
	StateChangedAt time.Time
	CreatedAt      time.Time
	// NodeTarget is the node's vmmd dial target. The scheduler fills it on
	// Wake and Get; stores do not persist it.
	NodeTarget string
}

// Upgrade results.
const (
	UpgradeOK      = "ok"
	UpgradeRefused = "refused"
	UpgradeFailed  = "failed"
)

// Upgrade is a database's current or last in-place major upgrade.
type Upgrade struct {
	// Operation names it: apid's journaled request, a lowercase UUID.
	Operation string
	From, To  int
	// Result is empty while it runs, then UpgradeOK, UpgradeRefused or
	// UpgradeFailed.
	Result string
}

// MajorSpan is a major a database ran until an upgrade.
type MajorSpan struct {
	Major int       `json:"major"`
	Until time.Time `json:"until"`
}

// MajorAt is the PostgreSQL major the database ran at t: a point-in-time
// restore to t recovers on it.
func (vm VM) MajorAt(t time.Time) int {
	for _, span := range vm.MajorHistory {
		if t.Before(span.Until) {
			return span.Major
		}
	}
	return vm.Major
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
	// Loads reports each node's committed volume and admitted memory, as
	// placement and wake admission count them.
	Loads(ctx context.Context) (map[string]NodeLoad, error)
	Get(ctx context.Context, id string) (VM, error)
	List(ctx context.Context, states ...State) ([]VM, error)
	// Place inserts vm as provisioning on the active node with the most
	// free volume capacity that fits it; a set vm.NodeID allows only that
	// node. An existing row is returned unchanged, so provisioning retries
	// converge.
	Place(ctx context.Context, vm VM) (VM, error)
	// AdmitWake moves a parked database to waking if its node's RAM
	// ceiling admits the VM's billable memory (else ErrNoCapacity) and a
	// boot slot is free (else ErrBootSlotsBusy). A database already
	// waking or running is returned unchanged. It clears StandbySynced
	// unless keepStandbySynced: the wake resumes the standby, making the
	// primary synchronous before its first client.
	//
	// Provisioning and restore boots take no boot slot.
	AdmitWake(ctx context.Context, id string, keepStandbySynced bool) (VM, error)
	// Transition changes state only from one of from (compare-and-set).
	Transition(ctx context.Context, id string, from []State, to State, opts TransitionOptions) error
	// Touch records activity on a running database.
	Touch(ctx context.Context, id string, at time.Time) error
	// SetAlwaysOn changes whether idle parking applies. A deleting
	// database is ErrConflict.
	SetAlwaysOn(ctx context.Context, id string, alwaysOn bool) error
	// RecordBackup stamps a successful base backup.
	RecordBackup(ctx context.Context, id string, at time.Time) error
	// ClaimRestore takes the lease on a provisioning restore (held zero)
	// or renews the lease held, returning the new lease. A lease another
	// replica holds, or a database no longer restoring, is ErrConflict.
	ClaimRestore(ctx context.Context, id string, held time.Time, lease time.Duration) (time.Time, error)
	// RecordRestore stamps a provisioning restore's last replayed commit.
	RecordRestore(ctx context.Context, id string, recoveredTo time.Time) error
	// Place opens the storage interval, compute opens on entering an
	// active state and closes on leaving it, and Delete closes every
	// interval; the ledger itself outlives the record (usage.go).
	//
	// SampleUsage moves a rate meter (history) to rate from at; an equal
	// rate is a no-op.
	SampleUsage(ctx context.Context, id, meter string, rate int64, at time.Time) error
	// AddEgress adds bytes to the hour at falls in.
	AddEgress(ctx context.Context, id string, bytes int64, at time.Time) error
	// Usage totals [from, to); a database with no ledger is ErrNotFound.
	Usage(ctx context.Context, id string, from, to time.Time) (Usage, error)
	// PruneUsage drops ledger rows that ended before before.
	PruneUsage(ctx context.Context, before time.Time) (int64, error)

	// Restore drills (drill.go). StartDrill records a running drill and
	// reports false when one with its id exists; FinishDrill ends a
	// running one; DrillsByNode summarizes each node's drills;
	// PruneDrills drops finished drills started before before.
	StartDrill(ctx context.Context, d Drill) (bool, error)
	RunningDrills(ctx context.Context) ([]Drill, error)
	FinishDrill(ctx context.Context, id string, result DrillResult, recoveredTo time.Time, errorCode string) error
	DrillsByNode(ctx context.Context) (map[string]DrillSummary, error)
	PruneDrills(ctx context.Context, before time.Time) (int64, error)
	// Resize records a parked or running database's new shape and marks
	// it pending, if the node fits it (volume capacity, and RAM while the
	// VM is active). The same shape again is a no-op; a smaller volume is
	// ErrInvalid. The storage ledger moves to the new volume size.
	// Turning replication on places the standby as Place does (else
	// ErrNoCapacity); turning it off needs the standby retired (stopped,
	// its volume gone; else ErrConflict) and owes the primary's slot
	// (DropStandbySlot).
	Resize(ctx context.Context, id string, shape Shape, at time.Time) error
	// ClearStandbySlotDrop records the primary's standby slot dropped.
	ClearStandbySlotDrop(ctx context.Context, id string) error
	// FinishResize clears the pending mark once the VM runs on the shape.
	FinishResize(ctx context.Context, id string) error
	// BeginMove marks a parked database moving to target, or to the active
	// node with the most free volume capacity when target is empty, if
	// that node has room for its volume. It returns the database with its
	// MoveNode and the move's lease. A resize-pending database is
	// ErrConflict.
	BeginMove(ctx context.Context, id, target string, lease time.Duration) (VM, error)
	// RenewMove renews the lease held; a lapsed or taken lease is
	// ErrConflict.
	RenewMove(ctx context.Context, id string, held time.Time, lease time.Duration) (time.Time, error)
	// FinishMove puts the moving database on its MoveNode, parked; only the
	// lease holder can.
	FinishMove(ctx context.Context, id string, held time.Time) error
	// AbortMove leaves a moving database parked on its node: by the lease
	// holder, or by anyone once the lease lapsed (held zero).
	AbortMove(ctx context.Context, id string, held time.Time, lastError string) error
	// Delete removes a database that is deleting.
	Delete(ctx context.Context, id string) error

	// Major upgrades (upgrade.go). BeginUpgrade marks a parked, bootstrapped
	// database upgrading to major as operation, under the upgrade's lease,
	// if its node's RAM ceiling admits its VM (else ErrNoCapacity; an
	// unavailable node is ErrNodeUnavailable). A database on major or a
	// later one is ErrInvalid; one resize- or promotion-pending is
	// ErrConflict.
	BeginUpgrade(ctx context.Context, id string, major int, operation string, lease time.Duration) (VM, error)
	// ClaimUpgrade renews the lease held, or takes one that lapsed (held
	// zero), returning the new lease; otherwise ErrConflict.
	ClaimUpgrade(ctx context.Context, id string, held time.Time, lease time.Duration) (time.Time, error)
	// FinishUpgrade makes the upgrade's major the database's, records
	// that it ran the old one until until, and ends the upgrade in to
	// (parked or running). backupAt is its first base backup on the new
	// major, zero when none was taken. Only the lease holder can.
	FinishUpgrade(ctx context.Context, id string, held time.Time, to State, until, backupAt time.Time) error
	// AbortUpgrade ends an upgrade on the database's old major, in to:
	// parked (UpgradeRefused) or failed (UpgradeFailed). By the lease
	// holder, or by anyone once the lease lapsed (held zero).
	AbortUpgrade(ctx context.Context, id string, held time.Time, to State, lastError string) error

	// Standbys (standby.go). Place gives a replicated database a standby
	// node besides its own, or fails with ErrNoCapacity.
	//
	// ClaimStandby marks a running replicated database's standby VM
	// running if the standby node is not unavailable and its RAM ceiling
	// admits it (else ErrNodeUnavailable or ErrNoCapacity). One already
	// running, or a database not running, is ErrConflict.
	ClaimStandby(ctx context.Context, id string) (VM, error)
	// ReleaseStandby marks the standby VM stopped. It stays synchronized
	// only if keepSynced: a clean stop after its primary's.
	ReleaseStandby(ctx context.Context, id string, keepSynced bool) error
	// MarkStandbySynced records whether the standby holds every
	// acknowledged commit. Setting it needs the database and its standby
	// running (else ErrConflict); clearing always succeeds.
	MarkStandbySynced(ctx context.Context, id string, synced bool) error
	// ReplaceStandby puts a replicated database's stopped standby on the
	// active node offering its major, other than its own, the current
	// standby's and avoid, with the most free volume capacity (else
	// ErrNoCapacity), not synchronized.
	ReplaceStandby(ctx context.Context, id string, avoid ...string) (VM, error)
	// Failover makes a synchronized standby's node the database's node:
	// running if the standby VM ran (it was promoted), else parked with
	// its promotion pending. A standby not synchronized, or a database
	// waking, moving or deleting, is ErrConflict.
	Failover(ctx context.Context, id string) (VM, error)
	// FinishPromotion clears a pending promotion once the promoted volume
	// booted.
	FinishPromotion(ctx context.Context, id string) error
}

// Shape is a database VM's size and volume, and whether it keeps a
// synchronous standby (the production class).
type Shape struct {
	VcpuCount   int
	MemMiB      int
	VolumeBytes int64
	Replicated  bool
}

func (vm VM) shape() Shape {
	return Shape{VcpuCount: vm.VcpuCount, MemMiB: vm.MemMiB, VolumeBytes: vm.VolumeBytes, Replicated: vm.Replicated}
}

// resizeFits checks a new shape against the node: the volume growth must
// fit its free volume capacity, and an active VM's new memory its RAM
// ceiling.
func resizeFits(node Node, u nodeUsage, vm VM, shape Shape) error {
	if node.Lifecycle == NodeUnavailable {
		return ErrNodeUnavailable
	}
	if node.VolumeCapacityBytes-u.volumeBytes+vm.VolumeBytes < shape.VolumeBytes {
		return ErrNoCapacity
	}
	if isActive(vm.State) && u.activeMB-billableMB(vm.MemMiB)+billableMB(shape.MemMiB) > int64(node.AdmissionCeilingMB) {
		return ErrNoCapacity
	}
	return nil
}

// standbyResizeFits checks a replicated database's grown volume against
// its standby's node, which holds a second copy. A standby node that is
// draining or unavailable is not checked: the standby monitor replaces
// that standby, on a node with room.
func standbyResizeFits(standby Node, u nodeUsage, vm VM, shape Shape) error {
	if !vm.Replicated || !shape.Replicated || vm.StandbyNode == "" || shape.VolumeBytes <= vm.VolumeBytes || standby.Lifecycle != NodeActive {
		return nil
	}
	if standby.VolumeCapacityBytes-u.volumeBytes+vm.VolumeBytes < shape.VolumeBytes {
		return fmt.Errorf("%w: the standby's node %s has no room for the larger volume", ErrNoCapacity, standby.ID)
	}
	return nil
}

// checkResize is what every store refuses before a resize.
func checkResize(vm VM, shape Shape) error {
	if vm.State != StateParked && vm.State != StateRunning {
		return ErrConflict
	}
	if shape.VolumeBytes < vm.VolumeBytes {
		return fmt.Errorf("%w: a volume cannot shrink", ErrInvalid)
	}
	if vm.Replicated && !shape.Replicated && vm.StandbyRunning {
		return fmt.Errorf("%w: the standby still runs", ErrConflict)
	}
	return nil
}

// replicationChange applies a resize's replication to vm: the standby node
// when it turns on, the owed slot when it turns off.
func replicationChange(vm *VM, shape Shape, standby string) {
	switch {
	case shape.Replicated && !vm.Replicated:
		vm.Replicated, vm.StandbyNode, vm.StandbySynced, vm.DropStandbySlot = true, standby, false, false
	case !shape.Replicated && vm.Replicated:
		vm.Replicated, vm.StandbyNode, vm.StandbySynced, vm.DropStandbySlot = false, "", false, true
	}
}

// activeStates hold RAM on their node.
var activeStates = []State{StateWaking, StateRunning, StateParking}

// holdsMemory reports whether a database in state has its VM admitted
// against its node's RAM ceiling: the active states, whose compute is
// metered, and an upgrade's VM, whose is not.
func holdsMemory(s State) bool {
	return isActive(s) || s == StateUpgrading
}

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

// NodeLoad is a node's committed volume (its databases, standbys and
// incoming moves) and admitted memory (active VMs and running standbys,
// with per-VM overhead).
type NodeLoad struct {
	VolumeBytes int64
	ActiveMB    int64
}

func loads(usage map[string]nodeUsage) map[string]NodeLoad {
	out := make(map[string]NodeLoad, len(usage))
	for id, u := range usage {
		out[id] = NodeLoad{VolumeBytes: u.volumeBytes, ActiveMB: u.activeMB}
	}
	return out
}

// nodeUsage is the committed volume, active memory and booting VMs on
// one node.
type nodeUsage struct {
	volumeBytes int64
	activeMB    int64
	waking      int64
}

// pickNode chooses the active node offering major with the most free
// volume capacity that still fits volumeBytes; a non-empty pin allows only
// that node. Ties break
// on node id for determinism.
func pickNode(nodes []Node, usage map[string]nodeUsage, volumeBytes int64, pin string, major int) (Node, bool) {
	var best Node
	var bestFree int64 = -1
	for _, n := range nodes {
		if n.Lifecycle != NodeActive || (pin != "" && n.ID != pin) || !n.offers(major) {
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

// admitWakeOn is AdmitWake's check of a parked VM against its node.
func admitWakeOn(node Node, usage nodeUsage, memMiB int) error {
	switch {
	case node.Lifecycle == NodeUnavailable:
		return ErrNodeUnavailable
	case !admits(node, usage, memMiB):
		return ErrNoCapacity
	case usage.waking >= int64(bootSlots(node)):
		return ErrBootSlotsBusy
	}
	return nil
}

// upgradeResultFor is the result of an upgrade that ended in to, on the
// database's old major.
func upgradeResultFor(to State) string {
	if to == StateParked {
		return UpgradeRefused
	}
	return UpgradeFailed
}

// admitUpgradeOn checks an upgrade of vm to major against its node's
// lifecycle and RAM ceiling; the upgrade's VM takes no boot slot.
func admitUpgradeOn(node Node, usage nodeUsage, vm VM, major int) error {
	switch {
	case major <= vm.Major:
		return fmt.Errorf("%w: the database runs PostgreSQL %d already", ErrInvalid, vm.Major)
	case node.Lifecycle == NodeUnavailable:
		return ErrNodeUnavailable
	case !admits(node, usage, vm.MemMiB):
		return ErrNoCapacity
	}
	return nil
}

func bootSlots(node Node) int {
	if node.BootSlots <= 0 {
		return DefaultBootSlots
	}
	return node.BootSlots
}

// moveTarget picks a move's destination among the other nodes: target if
// set, else the active node with the most free volume capacity. A
// replicated database never moves onto its standby's node.
func moveTarget(nodes []Node, usage map[string]nodeUsage, vm VM, target string) (Node, error) {
	others := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		if n.ID != vm.NodeID && n.ID != vm.StandbyNode {
			others = append(others, n)
		}
	}
	node, ok := pickNode(others, usage, vm.VolumeBytes, target, vm.Major)
	if !ok {
		if target == vm.NodeID {
			return Node{}, fmt.Errorf("%w: the database is already on %s", ErrInvalid, target)
		}
		return Node{}, ErrNoCapacity
	}
	return node, nil
}
