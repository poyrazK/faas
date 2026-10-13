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
	// errCodeVMLost marks a running database whose VM vmmd no longer has
	// (for example after a database node rebooted).
	errCodeVMLost = "vm_lost"
)

// ErrVMNotRunning reports that vmmd has no running VM for a database.
var ErrVMNotRunning = errors.New("dbsched: database VM is not running")

// ProvisionRequest describes a new native database.
type ProvisionRequest struct {
	DatabaseID  string
	Major       int
	VcpuCount   int
	MemMiB      int
	VolumeBytes int64
	// AlwaysOn keeps the database running (scale_to_zero = false).
	AlwaysOn bool
	// RestoreWindowSeconds is how far back the database may be restored.
	RestoreWindowSeconds int64
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
	case r.RestoreWindowSeconds < 0:
		return fmt.Errorf("dbsched: restore window %d out of range", r.RestoreWindowSeconds)
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
	// BackupInterval is how old a running database's last base backup
	// may get before the backup sweep takes another.
	BackupInterval time.Duration
	// BackupTimeout bounds one base backup.
	BackupTimeout time.Duration
	// RestoreTimeout bounds one point-in-time restore attempt.
	RestoreTimeout time.Duration
	// RestoreLease is how long a restore's lease lasts unrenewed; another
	// replica resumes the restore once it lapses.
	RestoreLease time.Duration
	// SharedArchive says every database node's dbbackupd uploads to the
	// same bucket and prefix, so any node can read any database's archive:
	// a restore may then run off the source's node, and after that node is
	// lost.
	SharedArchive bool
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
	// restores are the restores this process runs, by database id.
	restores  sync.Map // database id -> *restoreRun
	restoreWG sync.WaitGroup
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
	if cfg.BackupInterval <= 0 {
		cfg.BackupInterval = 24 * time.Hour
	}
	if cfg.BackupTimeout <= 0 {
		cfg.BackupTimeout = time.Hour
	}
	if cfg.RestoreTimeout <= 0 {
		cfg.RestoreTimeout = 2 * time.Hour
	}
	if cfg.RestoreLease <= 0 {
		cfg.RestoreLease = 2 * time.Minute
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
	vm, err := s.store.Place(ctx, VM{DatabaseID: req.DatabaseID, Major: req.Major, VcpuCount: req.VcpuCount, MemMiB: req.MemMiB,
		VolumeBytes: req.VolumeBytes, AlwaysOn: req.AlwaysOn, RestoreWindowSeconds: req.RestoreWindowSeconds})
	if err != nil || vm.State != StateProvisioning {
		return vm, err
	}
	if vm.RestoreSource != "" {
		// A restore provisions itself; bootstrapping it would init a new
		// cluster over the recovery.
		return vm, ErrConflict
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
	// The first base backup makes the database restorable from its first
	// minute. It is best-effort: a node without archiving, or a failed
	// backup, does not fail provisioning; the backup sweep retries.
	s.takeBackup(ctx, node, vm.DatabaseID)
	if err := s.vmm.Stop(ctx, node, vm.DatabaseID); err != nil {
		return errCodeStop, err
	}
	return "", nil
}

// takeBackup runs one base backup on a running database and records it.
// It reports whether a backup was stored.
func (s *Scheduler) takeBackup(ctx context.Context, node Node, id string) bool {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.BackupTimeout)
	defer cancel()
	resp, err := s.vmm.Control(ctx, node, id, dbguest.Request{Op: dbguest.OpBaseBackup})
	if err == nil && resp.Error == dbguest.ErrCodeArchiveOff {
		return false
	}
	if err != nil || resp.Error != "" || resp.Backup == "" {
		s.log.Warn("dbsched: base backup failed", "database_id", id, "err", errors.Join(err, guestError(resp)))
		return false
	}
	if err := s.store.RecordBackup(context.WithoutCancel(ctx), id, s.now()); err != nil {
		s.log.Warn("dbsched: record backup", "database_id", id, "err", err)
	}
	s.log.Info("dbsched: base backup stored", "database_id", id, "backup", resp.Backup)
	return true
}

// BackupDue backs up, one at a time, running databases whose last base
// backup is older than BackupInterval. Parked databases are not woken for
// it: their WAL is archived, and a backup follows their next wake.
func (s *Scheduler) BackupDue(ctx context.Context) (int, error) {
	running, err := s.store.List(ctx, StateRunning)
	if err != nil {
		return 0, err
	}
	cutoff := s.now().Add(-s.cfg.BackupInterval)
	taken := 0
	for _, vm := range running {
		if !vm.Bootstrapped || vm.LastBackupAt.After(cutoff) {
			continue
		}
		node, err := s.node(ctx, vm.NodeID)
		if err != nil {
			continue
		}
		// A backup is activity: keep the idle sweep from parking it mid-stream.
		_ = s.store.Touch(ctx, vm.DatabaseID, s.now())
		if s.takeBackup(ctx, node, vm.DatabaseID) {
			taken++
		}
	}
	return taken, nil
}

// RunBackupLoop runs BackupDue every interval until ctx ends. It is apart
// from the idle loop: a backup can take minutes and must not delay parking.
func (s *Scheduler) RunBackupLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := s.BackupDue(ctx); err != nil {
				s.log.Warn("dbsched: backup sweep failed", "err", err)
			} else if n > 0 {
				s.log.Info("dbsched: base backups taken", "count", n)
			}
		}
	}
}

func guestError(resp dbguest.Response) error {
	if resp.Error == "" {
		return nil
	}
	return fmt.Errorf("guest error %s", resp.Error)
}

func (s *Scheduler) waitReady(ctx context.Context, node Node, id string) error {
	return s.pollReady(ctx, node, id, s.cfg.ReadyTimeout, 20*time.Millisecond)
}

// pollReady polls the guest every interval until it is ready or failed.
func (s *Scheduler) pollReady(ctx context.Context, node Node, id string, timeout, every time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
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
		case <-time.After(every):
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
		return s.withTarget(ctx, vm), nil
	case StateWaking:
		vm, err := s.awaitRunning(ctx, id)
		return s.withTarget(ctx, vm), err
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
	return s.Get(ctx, id)
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

// Get returns the database's runtime record with its node target.
func (s *Scheduler) Get(ctx context.Context, id string) (VM, error) {
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return vm, err
	}
	return s.withTarget(ctx, vm), nil
}

func (s *Scheduler) withTarget(ctx context.Context, vm VM) VM {
	if node, err := s.node(ctx, vm.NodeID); err == nil {
		vm.NodeTarget = node.TargetURL
	}
	return vm
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
	s.stopRestore(id)
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
		if vm.AlwaysOn || vm.LastActiveAt.After(cutoff) {
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

// SetAlwaysOn changes whether a database scales to zero. Turning it on
// leaves a parked database to the keep-warm sweep, which wakes it.
func (s *Scheduler) SetAlwaysOn(ctx context.Context, id string, alwaysOn bool) (VM, error) {
	unlock := s.lock(id)
	defer unlock()
	if err := s.store.SetAlwaysOn(ctx, id, alwaysOn); err != nil {
		return VM{}, err
	}
	if !alwaysOn {
		// Restart the idle clock: scale-to-zero applies from now, not from
		// the last query of the always-on period.
		_ = s.store.Touch(ctx, id, s.now())
	}
	return s.store.Get(ctx, id)
}

// KeepWarm keeps always-on databases running: it wakes those found parked
// and recovers those whose VM vmmd no longer runs (a node reboot). It
// reports how many it woke.
func (s *Scheduler) KeepWarm(ctx context.Context) (int, error) {
	vms, err := s.store.List(ctx, StateParked, StateRunning)
	if err != nil {
		return 0, err
	}
	woke := 0
	for _, vm := range vms {
		if !vm.AlwaysOn || !vm.Bootstrapped {
			continue
		}
		if vm.State == StateRunning && !s.vmLost(ctx, vm) {
			continue
		}
		if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
			s.log.Warn("dbsched: keep-warm wake failed", "database_id", vm.DatabaseID, "err", err)
			continue
		}
		woke++
	}
	return woke, nil
}

// vmLost probes a running database and, when vmmd reports no VM, parks the
// record so the next wake boots it again. Any other failure (an
// unreachable node) leaves it alone: restarting a healthy database on a
// network blip would be worse than waiting.
func (s *Scheduler) vmLost(ctx context.Context, vm VM) bool {
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return false
	}
	_, err = s.vmm.Control(ctx, node, vm.DatabaseID, dbguest.Request{Op: dbguest.OpStatus})
	if !errors.Is(err, ErrVMNotRunning) {
		return false
	}
	unlock := s.lock(vm.DatabaseID)
	defer unlock()
	if err := s.store.Transition(ctx, vm.DatabaseID, []State{StateRunning}, StateParked, TransitionOptions{LastError: errCodeVMLost}); err != nil {
		return false
	}
	s.log.Warn("dbsched: running database had no VM; rebooting it", "database_id", vm.DatabaseID, "node_id", vm.NodeID)
	return true
}

// Recover settles transitions a previous schedd process left in flight:
// a waking or parking database is stopped and parked. Its next wake boots
// it again; PostgreSQL recovers from its WAL if the stop was unclean.
// Restores resume in the background.
func (s *Scheduler) Recover(ctx context.Context) error {
	if err := s.ResumeRestores(ctx); err != nil {
		return err
	}
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
			if n, err := s.KeepWarm(ctx); err != nil {
				s.log.Warn("dbsched: keep-warm sweep failed", "err", err)
			} else if n > 0 {
				s.log.Info("dbsched: woke always-on databases", "count", n)
			}
			if err := s.ResumeRestores(ctx); err != nil {
				s.log.Warn("dbsched: restore sweep failed", "err", err)
			}
		}
	}
}
