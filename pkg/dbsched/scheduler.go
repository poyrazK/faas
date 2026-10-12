package dbsched

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// VMM is the per-node vmmd surface the scheduler drives. Implementations
// map "already exists" on volume creation and "not running" on stop to nil,
// so every step is safe to retry.
type VMM interface {
	CreateVolume(ctx context.Context, node Node, id string, sizeBytes int64) error
	DeleteVolume(ctx context.Context, node Node, id string) error
	Start(ctx context.Context, node Node, vm VM) error
	Stop(ctx context.Context, node Node, id string) error
	Control(ctx context.Context, node Node, id string, req dbguest.Request) (dbguest.Response, error)
}

// Stable last_error codes.
const (
	errCodeVolume    = "volume_failed"
	errCodeBoot      = "boot_failed"
	errCodeBootstrap = "bootstrap_failed"
	errCodeNotReady  = "not_ready"
	errCodeStop      = "stop_failed"
)

// ProvisionRequest describes a new native database.
type ProvisionRequest struct {
	DatabaseID  string
	Major       int
	VcpuCount   int
	MemMiB      int
	VolumeBytes int64
}

// Validate rejects a request that cannot become a database VM.
func (r ProvisionRequest) Validate() error {
	if _, err := uuid.Parse(r.DatabaseID); err != nil {
		return fmt.Errorf("dbsched: invalid database id: %w", err)
	}
	switch {
	case r.Major < dbguest.MinMajor || r.Major > dbguest.MaxMajor:
		return fmt.Errorf("dbsched: unsupported postgres major %d", r.Major)
	case r.VcpuCount < 1 || r.VcpuCount > 32:
		return fmt.Errorf("dbsched: vcpu count %d out of range", r.VcpuCount)
	case r.MemMiB < 128 || r.MemMiB > 65536:
		return fmt.Errorf("dbsched: memory %d MiB out of range", r.MemMiB)
	case r.VolumeBytes < minVolumeBytes || r.VolumeBytes > maxVolumeBytes:
		return fmt.Errorf("dbsched: volume size %d out of range", r.VolumeBytes)
	}
	return nil
}

// Volume bounds mirror vmmd's (pkg/dbvm); plan storage limits are enforced
// by the caller.
const (
	minVolumeBytes int64 = 256 << 20
	maxVolumeBytes int64 = 2 << 40
)

// Config tunes the scheduler.
type Config struct {
	// IdleTimeout parks a running database with no recorded activity.
	IdleTimeout time.Duration
	// ReadyTimeout bounds a boot until PostgreSQL accepts connections.
	ReadyTimeout time.Duration
	// WakeWait bounds how long a wake waits for another replica's wake.
	WakeWait time.Duration
}

// Scheduler is schedd's database scheduler. Operations on one database are
// serialized in-process; the store's compare-and-set transitions serialize
// them across replicas.
type Scheduler struct {
	store Store
	vmm   VMM
	cfg   Config
	log   *slog.Logger
	now   func() time.Time
	locks sync.Map // database id -> *sync.Mutex
}

// New builds a Scheduler with defaults for zero Config fields.
func New(store Store, vmm VMM, cfg Config, log *slog.Logger) *Scheduler {
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 5 * time.Minute
	}
	if cfg.ReadyTimeout <= 0 {
		cfg.ReadyTimeout = time.Minute
	}
	if cfg.WakeWait <= 0 {
		cfg.WakeWait = time.Minute
	}
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{store: store, vmm: vmm, cfg: cfg, log: log, now: time.Now}
}

func (s *Scheduler) lock(id string) func() {
	m, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Scheduler) node(ctx context.Context, id string) (Node, error) {
	nodes, err := s.store.Nodes(ctx)
	if err != nil {
		return Node{}, err
	}
	for _, n := range nodes {
		if n.ID == id {
			return n, nil
		}
	}
	return Node{}, ErrNodeUnavailable
}

// Provision places a database, creates and bootstraps its volume, and parks
// it. Retrying a provisioning database resumes it; any other existing
// database is returned unchanged.
func (s *Scheduler) Provision(ctx context.Context, req ProvisionRequest) (VM, error) {
	if err := req.Validate(); err != nil {
		return VM{}, err
	}
	unlock := s.lock(req.DatabaseID)
	defer unlock()
	vm, err := s.store.Place(ctx, VM{DatabaseID: req.DatabaseID, Major: req.Major, VcpuCount: req.VcpuCount, MemMiB: req.MemMiB, VolumeBytes: req.VolumeBytes})
	if err != nil || vm.State != StateProvisioning {
		return vm, err
	}
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return vm, err
	}
	if code, err := s.bootstrap(ctx, node, vm); err != nil {
		_ = s.vmm.Stop(context.WithoutCancel(ctx), node, vm.DatabaseID)
		_ = s.store.Transition(context.WithoutCancel(ctx), vm.DatabaseID, []State{StateProvisioning}, StateFailed, TransitionOptions{LastError: code})
		return vm, fmt.Errorf("dbsched: provision %s: %w", vm.DatabaseID, err)
	}
	if err := s.store.Transition(ctx, vm.DatabaseID, []State{StateProvisioning}, StateParked, TransitionOptions{Bootstrapped: true}); err != nil {
		return vm, err
	}
	return s.store.Get(ctx, vm.DatabaseID)
}

func (s *Scheduler) bootstrap(ctx context.Context, node Node, vm VM) (string, error) {
	if err := s.vmm.CreateVolume(ctx, node, vm.DatabaseID, vm.VolumeBytes); err != nil {
		return errCodeVolume, err
	}
	if err := s.vmm.Start(ctx, node, vm); err != nil {
		return errCodeBoot, err
	}
	resp, err := s.vmm.Control(ctx, node, vm.DatabaseID, dbguest.Request{Op: dbguest.OpStatus})
	if err != nil {
		return errCodeBoot, err
	}
	if resp.State == dbguest.StateNeedsBootstrap {
		if resp, err = s.vmm.Control(ctx, node, vm.DatabaseID, dbguest.Request{Op: dbguest.OpBootstrap}); err != nil || resp.Error != "" {
			return errCodeBootstrap, errors.Join(err, guestError(resp))
		}
	}
	if err := s.waitReady(ctx, node, vm.DatabaseID); err != nil {
		return errCodeNotReady, err
	}
	if err := s.vmm.Stop(ctx, node, vm.DatabaseID); err != nil {
		return errCodeStop, err
	}
	return "", nil
}

func guestError(resp dbguest.Response) error {
	if resp.Error == "" {
		return nil
	}
	return fmt.Errorf("guest error %s", resp.Error)
}

func (s *Scheduler) waitReady(ctx context.Context, node Node, id string) error {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.ReadyTimeout)
	defer cancel()
	for {
		resp, err := s.vmm.Control(ctx, node, id, dbguest.Request{Op: dbguest.OpStatus})
		if err == nil && resp.State == dbguest.StateReady {
			return nil
		}
		if err == nil && resp.State == dbguest.StateFailed {
			return guestError(resp)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("dbsched: %s not ready: %w", id, errors.Join(ctx.Err(), err))
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// Wake makes a database running and returns it. A running database is
// touched and returned at once; a parked one is admitted against its node's
// RAM ceiling, booted and awaited.
func (s *Scheduler) Wake(ctx context.Context, id string) (VM, error) {
	unlock := s.lock(id)
	defer unlock()
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return VM{}, err
	}
	switch vm.State {
	case StateRunning:
		_ = s.store.Touch(ctx, id, s.now())
		return vm, nil
	case StateWaking:
		return s.awaitRunning(ctx, id)
	case StateParked:
	default:
		return vm, ErrConflict
	}
	if vm, err = s.store.AdmitWake(ctx, id); err != nil {
		return vm, err
	}
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		_ = s.store.Transition(context.WithoutCancel(ctx), id, []State{StateWaking}, StateParked, TransitionOptions{LastError: errCodeBoot})
		return vm, fmt.Errorf("dbsched: wake %s: %w", id, err)
	}
	if err = s.vmm.Start(ctx, node, vm); err == nil {
		err = s.waitReady(ctx, node, id)
	}
	if err != nil {
		_ = s.vmm.Stop(context.WithoutCancel(ctx), node, id)
		_ = s.store.Transition(context.WithoutCancel(ctx), id, []State{StateWaking}, StateParked, TransitionOptions{LastError: errCodeBoot})
		return vm, fmt.Errorf("dbsched: wake %s: %w", id, err)
	}
	if err := s.store.Transition(ctx, id, []State{StateWaking}, StateRunning, TransitionOptions{}); err != nil {
		return vm, err
	}
	return s.store.Get(ctx, id)
}

// awaitRunning waits for a wake another replica started.
func (s *Scheduler) awaitRunning(ctx context.Context, id string) (VM, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.WakeWait)
	defer cancel()
	for {
		vm, err := s.store.Get(ctx, id)
		if err != nil || vm.State == StateRunning {
			return vm, err
		}
		if vm.State != StateWaking {
			return vm, ErrConflict
		}
		select {
		case <-ctx.Done():
			return vm, fmt.Errorf("dbsched: waiting for %s to wake: %w", id, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Park stops a running database. Parking a parked database is a no-op.
func (s *Scheduler) Park(ctx context.Context, id string) error {
	unlock := s.lock(id)
	defer unlock()
	return s.parkLocked(ctx, id)
}

func (s *Scheduler) parkLocked(ctx context.Context, id string) error {
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if vm.State == StateParked {
		return nil
	}
	if err := s.store.Transition(ctx, id, []State{StateRunning, StateParking}, StateParking, TransitionOptions{}); err != nil {
		return err
	}
	node, err := s.node(ctx, vm.NodeID)
	if err == nil {
		err = s.vmm.Stop(ctx, node, id)
	}
	if err != nil {
		_ = s.store.Transition(context.WithoutCancel(ctx), id, []State{StateParking}, StateFailed, TransitionOptions{LastError: errCodeStop})
		return fmt.Errorf("dbsched: park %s: %w", id, err)
	}
	return s.store.Transition(ctx, id, []State{StateParking}, StateParked, TransitionOptions{})
}

// Get returns the database's runtime record.
func (s *Scheduler) Get(ctx context.Context, id string) (VM, error) {
	return s.store.Get(ctx, id)
}

// Touch records client activity, deferring idle parking.
func (s *Scheduler) Touch(ctx context.Context, id string) error {
	return s.store.Touch(ctx, id, s.now())
}

// Admin runs a control-plane SQL batch as the superuser, waking the
// database first. The SQL can carry role passwords and is never logged.
func (s *Scheduler) Admin(ctx context.Context, id, sql string) (dbguest.Response, error) {
	vm, err := s.Wake(ctx, id)
	if err != nil {
		return dbguest.Response{}, err
	}
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return dbguest.Response{}, err
	}
	resp, err := s.vmm.Control(ctx, node, id, dbguest.Request{Op: dbguest.OpAdminSQL, SQL: sql})
	_ = s.Touch(ctx, id)
	return resp, err
}

// Delete stops a database if needed and removes its volume and record.
// Deleting a missing database succeeds.
func (s *Scheduler) Delete(ctx context.Context, id string) error {
	unlock := s.lock(id)
	defer unlock()
	vm, err := s.store.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	all := []State{StateProvisioning, StateParked, StateWaking, StateRunning, StateParking, StateFailed, StateDeleting}
	if err := s.store.Transition(ctx, id, all, StateDeleting, TransitionOptions{}); err != nil {
		return err
	}
	node, err := s.node(ctx, vm.NodeID)
	if err == nil {
		err = s.vmm.Stop(ctx, node, id)
	}
	if err == nil {
		err = s.vmm.DeleteVolume(ctx, node, id)
	}
	if err != nil {
		return fmt.Errorf("dbsched: delete %s: %w", id, err)
	}
	return s.store.Delete(ctx, id)
}

// ParkIdle parks running databases idle longer than IdleTimeout and
// reports how many it parked.
func (s *Scheduler) ParkIdle(ctx context.Context) (int, error) {
	running, err := s.store.List(ctx, StateRunning)
	if err != nil {
		return 0, err
	}
	cutoff := s.now().Add(-s.cfg.IdleTimeout)
	parked := 0
	for _, vm := range running {
		if vm.LastActiveAt.After(cutoff) {
			continue
		}
		if err := s.Park(ctx, vm.DatabaseID); err != nil {
			s.log.Warn("dbsched: idle park failed", "database_id", vm.DatabaseID, "err", err)
			continue
		}
		parked++
	}
	return parked, nil
}

// Recover settles transitions a previous schedd process left in flight:
// a waking or parking database is stopped and parked. Its next wake boots
// it again; PostgreSQL recovers from its WAL if the stop was unclean.
func (s *Scheduler) Recover(ctx context.Context) error {
	stuck, err := s.store.List(ctx, StateWaking, StateParking)
	if err != nil {
		return err
	}
	for _, vm := range stuck {
		unlock := s.lock(vm.DatabaseID)
		node, err := s.node(ctx, vm.NodeID)
		if err == nil {
			err = s.vmm.Stop(ctx, node, vm.DatabaseID)
		}
		if err == nil {
			err = s.store.Transition(ctx, vm.DatabaseID, []State{StateWaking, StateParking}, StateParked, TransitionOptions{})
		}
		unlock()
		if err != nil {
			s.log.Warn("dbsched: recover failed", "database_id", vm.DatabaseID, "state", vm.State, "err", err)
		}
	}
	return nil
}

// RunIdleLoop parks idle databases every interval until ctx ends.
func (s *Scheduler) RunIdleLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := s.ParkIdle(ctx); err != nil {
				s.log.Warn("dbsched: idle sweep failed", "err", err)
			} else if n > 0 {
				s.log.Info("dbsched: parked idle databases", "count", n)
			}
		}
	}
}
