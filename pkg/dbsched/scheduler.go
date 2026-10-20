package dbsched

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
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
	// DeleteVolume deletes a deleted database's volume and archive.
	DeleteVolume(ctx context.Context, node Node, id string) error
	// DropArchive deletes only the archive, through node's dbbackupd; with
	// a shared archive any node reaches every database's bucket objects.
	DropArchive(ctx context.Context, node Node, id string) error
	// ListVolumes reports the database volumes on node.
	ListVolumes(ctx context.Context, node Node) ([]NodeVolume, error)
	// PruneArchive prunes a database's archive in the shared bucket to its
	// restore window, through node's dbbackupd.
	PruneArchive(ctx context.Context, node Node, id string, windowSeconds int64) error
	// ArchiveBytes sizes the databases' archives on node; ones it could
	// not size are absent.
	ArchiveBytes(ctx context.Context, node Node, ids []string) (map[string]int64, error)
	// GrowVolume enlarges a stopped database's volume and filesystem to
	// sizeBytes; already that size is a cheap no-op.
	GrowVolume(ctx context.Context, node Node, id string, sizeBytes int64) error
	// CopyVolume copies a stopped database's volume from one node to
	// another, which installs it only if it arrived whole (move.go).
	CopyVolume(ctx context.Context, from, to Node, id string) error
	// SyncArchive ships the database's archive files node still holds to
	// the shared bucket.
	SyncArchive(ctx context.Context, node Node, id string) error
	// DiscardVolume deletes a volume and nothing else: a failed move's
	// copy.
	DiscardVolume(ctx context.Context, node Node, id string) error
	// ReleaseVolume deletes a moved database's old volume and hands that
	// node's copy of its archive to the bucket.
	ReleaseVolume(ctx context.Context, node Node, id string) error
	Start(ctx context.Context, node Node, vm VM) error
	// StartStandby boots a replicated database's standby on node,
	// streaming from its primary on primary (standby.go).
	StartStandby(ctx context.Context, node, primary Node, vm VM) error
	// Replication sets a running primary's replication mode (empty mode
	// only reports) and returns its view of its standby; standbyNode is
	// the node allowed to stream from it, required with async and sync.
	Replication(ctx context.Context, node Node, id string, mode dbguest.ReplicationMode, standbyNode string) (dbguest.ReplicationStatus, error)
	Stop(ctx context.Context, node Node, id string) error
	Control(ctx context.Context, node Node, id string, req dbguest.Request) (dbguest.Response, error)
	// Import starts a running database's import of another PostgreSQL
	// database (import.go).
	Import(ctx context.Context, node Node, id string, spec ImportSpec) (dbguest.ImportStatus, error)
	// PutHold, GetHold and DeleteHold manage the holds on a database's
	// archive through node's dbbackupd (snapshot.go). GetHold of a missing
	// hold is ErrNotFound; PutHold of a name holding another point is
	// ErrConflict; DeleteHold of a missing one succeeds.
	PutHold(ctx context.Context, node Node, id, name string, point time.Time) (ArchiveHold, error)
	GetHold(ctx context.Context, node Node, id, name string) (ArchiveHold, error)
	DeleteHold(ctx context.Context, node Node, id, name string) error
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
	// errCodeNodeLost marks a database parked because the operator marked
	// its node unavailable.
	errCodeNodeLost = "node_lost"
)

// NodeVolume is one database volume on a node.
type NodeVolume struct {
	DatabaseID string
	Running    bool
}

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
	// Replicated keeps a hot standby on a second node (production class).
	Replicated bool
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
	// WakeQueueWait bounds how long a wake waits for its node to admit
	// it, while the node is at its RAM ceiling or boot slots (a wake
	// storm). Past it the wake fails with that admission error.
	WakeQueueWait time.Duration
	// EvictIdleAfter: a wake over its node's RAM ceiling first parks the
	// node's scale-to-zero databases that have had no activity for this
	// long, longest idle first, before its idle timeout. pgproxyd touches
	// a database every 30 s while a session is open, so the default (two
	// minutes) parks only databases nobody is connected to. Negative
	// disables it.
	EvictIdleAfter time.Duration
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
	// MoveTimeout bounds one database move's copy; MoveLease is how long
	// a move's lease lasts unrenewed before another replica may abort it.
	MoveTimeout time.Duration
	MoveLease   time.Duration
	// StartMoveWait bounds how long an operator's move waits for the
	// database's lock (default 30 s); a database already moving holds it
	// for the whole copy.
	StartMoveWait time.Duration
	// EvacuateDraining moves databases off draining nodes (needs
	// SharedArchive).
	EvacuateDraining bool
	// StandbyCheckInterval is how often the standby monitor checks
	// replicated databases (default 2 s). A standby that stops keeping up
	// holds its primary's commits until the next check.
	StandbyCheckInterval time.Duration
	// StandbySeedTimeout bounds a standby's boot, including copying its
	// primary onto a new volume (default 30 min).
	StandbySeedTimeout time.Duration
	// StandbySyncTimeout bounds a booted standby becoming synchronous
	// (default 2 min).
	StandbySyncTimeout time.Duration
	// StandbyRetry spaces one database's attach attempts (default 30 s).
	StandbyRetry time.Duration
	// StandbyResumeTimeout bounds resuming a production-class database's
	// synchronized standby after a wake (default 30 s; negative turns
	// resuming off, so every wake starts asynchronous). The primary is
	// synchronous before its first client, so commits wait for the
	// standby meanwhile; past it the primary turns asynchronous.
	StandbyResumeTimeout time.Duration
	// StandbyStaleClaim: a running standby that never became synchronous
	// and is not being attached by this process is given up after this
	// long (default 15 min); another replica may still be seeding it.
	StandbyStaleClaim time.Duration
	// StorageSampleInterval spaces the idle sweep's storage samples of
	// running databases (default 2 min).
	StorageSampleInterval time.Duration
	// StaleIdle is how long a stale always-on database must have been
	// idle before a sample restarts it onto the current base (default
	// 2 min); StaleRestarts caps the restarts per sample (default 2;
	// negative restarts none).
	StaleIdle     time.Duration
	StaleRestarts int
	// DrillEvery spaces each node's restore drills (default 24 h; negative
	// turns drills off); DrillTimeout fails a drill still restoring after
	// it (default 3 h, beyond RestoreTimeout).
	DrillEvery   time.Duration
	DrillTimeout time.Duration
	// AutoFailoverAfter fails a running replicated database over to its
	// synchronized standby once its primary's node has not answered for
	// this long (default 30 s; negative leaves failover to the operator's
	// lost-node mark).
	AutoFailoverAfter time.Duration
	// UpgradeTimeout bounds an in-place major upgrade from its start,
	// across the replicas that resume it (default 2.5 h, beyond the 2 h
	// guest-init gives pg_upgrade); UpgradeLease is how long its lease
	// lasts unrenewed before another replica resumes it (default 2 min).
	UpgradeTimeout time.Duration
	UpgradeLease   time.Duration
	// Metrics, when set, records failovers, standby drops and moves, and
	// the idle sweep observes the catalog into it.
	Metrics *Metrics
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
	// lostPruned is when each lost node's database was last pruned.
	lostPruned sync.Map // database id -> time.Time
	// freed wakes this process's queued wakes when a node frees a boot
	// slot or RAM; other replicas' are found by polling.
	freed nodeEvents
	// Standbys (standby.go): the databases this process is attaching a
	// standby to, when each was last tried, and since when a running
	// standby has looked abandoned.
	attaching     sync.Map // database id -> struct{}
	attachTried   sync.Map // database id -> time.Time
	unsyncedSince sync.Map // database id -> time.Time
	standbyWG     sync.WaitGroup
	// nodeFailing is since when each node's vmmd has not answered.
	nodeFailing sync.Map // node id -> time.Time
	// moveWG tracks operators' moves running in the background.
	moveWG sync.WaitGroup
	// upgrades are the major upgrades this process runs, by database id.
	upgrades  sync.Map // database id -> *upgradeRun
	upgradeWG sync.WaitGroup
}

// nodeEvents is a per-node broadcast: wait returns a channel the next
// signal for that node closes.
type nodeEvents struct {
	mu sync.Mutex
	ch map[string]chan struct{}
}

func (e *nodeEvents) wait(node string) <-chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ch == nil {
		e.ch = map[string]chan struct{}{}
	}
	c, ok := e.ch[node]
	if !ok {
		c = make(chan struct{})
		e.ch[node] = c
	}
	return c
}

func (e *nodeEvents) signal(node string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.ch[node]; ok {
		close(c)
		delete(e.ch, node)
	}
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
	if cfg.WakeQueueWait <= 0 {
		cfg.WakeQueueWait = 30 * time.Second
	}
	if cfg.EvictIdleAfter == 0 {
		cfg.EvictIdleAfter = 2 * time.Minute
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
	if cfg.MoveTimeout <= 0 {
		cfg.MoveTimeout = 2 * time.Hour
	}
	if cfg.MoveLease <= 0 {
		cfg.MoveLease = 2 * time.Minute
	}
	if cfg.StartMoveWait <= 0 {
		cfg.StartMoveWait = 30 * time.Second
	}
	if cfg.StandbyCheckInterval <= 0 {
		cfg.StandbyCheckInterval = 2 * time.Second
	}
	if cfg.StandbySeedTimeout <= 0 {
		cfg.StandbySeedTimeout = 30 * time.Minute
	}
	if cfg.StandbySyncTimeout <= 0 {
		cfg.StandbySyncTimeout = 2 * time.Minute
	}
	if cfg.StandbyRetry <= 0 {
		cfg.StandbyRetry = 30 * time.Second
	}
	if cfg.StandbyResumeTimeout == 0 {
		cfg.StandbyResumeTimeout = 30 * time.Second
	}
	if cfg.StandbyStaleClaim <= 0 {
		cfg.StandbyStaleClaim = 15 * time.Minute
	}
	if cfg.StorageSampleInterval <= 0 {
		cfg.StorageSampleInterval = 2 * time.Minute
	}
	if cfg.StaleIdle <= 0 {
		cfg.StaleIdle = 2 * time.Minute
	}
	if cfg.StaleRestarts == 0 {
		cfg.StaleRestarts = 2
	}
	if cfg.DrillEvery == 0 {
		cfg.DrillEvery = 24 * time.Hour
	}
	if cfg.DrillTimeout <= 0 {
		cfg.DrillTimeout = cfg.RestoreTimeout + time.Hour
	}
	if cfg.UpgradeTimeout <= 0 {
		cfg.UpgradeTimeout = 150 * time.Minute
	}
	if cfg.UpgradeLease <= 0 {
		cfg.UpgradeLease = 2 * time.Minute
	}
	if cfg.AutoFailoverAfter == 0 {
		cfg.AutoFailoverAfter = 30 * time.Second
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

// tryLock takes the database's lock only if it is free.
func (s *Scheduler) tryLock(id string) (func(), bool) {
	m, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, false
	}
	return mu.Unlock, true
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
		VolumeBytes: req.VolumeBytes, AlwaysOn: req.AlwaysOn, RestoreWindowSeconds: req.RestoreWindowSeconds, Replicated: req.Replicated})
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
	at, ok := s.baseBackup(ctx, node, id)
	if !ok {
		return false
	}
	if err := s.store.RecordBackup(context.WithoutCancel(ctx), id, at); err != nil {
		s.log.Warn("dbsched: record backup", "database_id", id, "err", err)
	}
	return true
}

// baseBackup runs one base backup on a running database and reports when
// it was stored; the caller records it.
func (s *Scheduler) baseBackup(ctx context.Context, node Node, id string) (time.Time, bool) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.BackupTimeout)
	defer cancel()
	resp, err := s.vmm.Control(ctx, node, id, dbguest.Request{Op: dbguest.OpBaseBackup})
	if err == nil && resp.Error == dbguest.ErrCodeArchiveOff {
		return time.Time{}, false
	}
	if err != nil || resp.Error != "" || resp.Backup == "" {
		s.log.Warn("dbsched: base backup failed", "database_id", id, "err", errors.Join(err, guestError(resp)))
		return time.Time{}, false
	}
	s.log.Info("dbsched: base backup stored", "database_id", id, "backup", resp.Backup)
	return s.now(), true
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

// RunBackupLoop runs BackupDue, CollectOrphans and PruneLostArchives every
// interval until ctx ends. It is apart from the idle loop: a backup can take minutes and must
// not delay parking.
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
			if _, err := s.CollectOrphans(ctx); err != nil {
				s.log.Warn("dbsched: orphan sweep failed", "err", err)
			}
			if n, err := s.EvacuateDraining(ctx); err != nil {
				s.log.Warn("dbsched: evacuation sweep failed", "err", err)
			} else if n > 0 {
				s.log.Info("dbsched: moved databases off draining nodes", "count", n)
			}
			if _, err := s.PruneLostArchives(ctx); err != nil {
				s.log.Warn("dbsched: lost-node prune sweep failed", "err", err)
			}
			if _, err := s.SampleArchives(ctx); err != nil {
				s.log.Warn("dbsched: archive size sweep failed", "err", err)
			}
			if _, err := s.store.PruneUsage(ctx, s.now().Add(-usageRetention)); err != nil {
				s.log.Warn("dbsched: usage ledger prune failed", "err", err)
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
	return s.wakeLocked(ctx, id)
}

func (s *Scheduler) wakeLocked(ctx context.Context, id string) (VM, error) {
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
	resume := s.resumesStandby(ctx, vm)
	if vm, err = s.admitWake(ctx, vm, resume); err != nil {
		return vm, err
	}
	// Leaving waking frees a boot slot, and a failed boot its RAM too.
	defer s.freed.signal(vm.NodeID)
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
	if vm.PromotePending {
		// The boot promoted a standby that took over while parked.
		if err := s.store.FinishPromotion(ctx, id); err != nil {
			s.log.Warn("dbsched: record a finished promotion", "database_id", id, "err", err)
		}
	}
	if resume {
		if err := s.resumeSynchronous(ctx, node, vm); err != nil {
			_ = s.vmm.Stop(context.WithoutCancel(ctx), node, id)
			_ = s.store.Transition(context.WithoutCancel(ctx), id, []State{StateWaking}, StateParked, TransitionOptions{LastError: errCodeBoot})
			return vm, fmt.Errorf("dbsched: wake %s: %w", id, err)
		}
	}
	if err := s.store.Transition(ctx, id, []State{StateWaking}, StateRunning, TransitionOptions{}); err != nil {
		return vm, err
	}
	if vm.Replicated {
		s.kickStandby(ctx, id)
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
	// The standby stops after its primary, so a clean stop of both leaves
	// it holding every commit.
	s.stopStandby(context.WithoutCancel(ctx), vm, err == nil)
	if err != nil {
		_ = s.store.Transition(context.WithoutCancel(ctx), id, []State{StateParking}, StateFailed, TransitionOptions{LastError: errCodeStop})
		return fmt.Errorf("dbsched: park %s: %w", id, err)
	}
	defer s.freed.signal(vm.NodeID)
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

// Delete stops a database if needed and removes its volume, archive and
// record. Deleting a missing database succeeds. On a node the operator has
// marked unavailable (lost), the VM and volume went with the node, so only
// the archive's bucket copy is deleted; CollectOrphans removes the volume
// if the node ever comes back.
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
	all := []State{StateProvisioning, StateParked, StateWaking, StateRunning, StateParking, StateUpgrading, StateFailed, StateDeleting}
	if err := s.store.Transition(ctx, id, all, StateDeleting, TransitionOptions{}); err != nil {
		return err
	}
	s.deleteStandby(ctx, vm)
	node, err := s.node(ctx, vm.NodeID)
	switch {
	case errors.Is(err, ErrNodeUnavailable) || (err == nil && node.Lifecycle == NodeUnavailable):
		err = s.dropLostArchive(ctx, id)
	case err == nil:
		if err = s.vmm.Stop(ctx, node, id); err == nil {
			err = s.vmm.DeleteVolume(ctx, node, id)
		}
	}
	if err != nil {
		return fmt.Errorf("dbsched: delete %s: %w", id, err)
	}
	return s.store.Delete(ctx, id)
}

// dropLostArchive deletes the bucket copy of a lost node's database through
// any other node. Without a shared archive no other node reaches it; the
// lost node drops it if it returns (CollectOrphans).
func (s *Scheduler) dropLostArchive(ctx context.Context, id string) error {
	if !s.cfg.SharedArchive {
		s.log.Warn("dbsched: deleted a database on a lost node; its archive stays until the node returns", "database_id", id)
		return nil
	}
	nodes, err := s.store.Nodes(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, n := range nodes {
		if n.Lifecycle == NodeUnavailable {
			continue
		}
		err := s.vmm.DropArchive(ctx, n, id)
		if err == nil {
			return nil
		}
		errs = append(errs, err)
	}
	return fmt.Errorf("%w: no node dropped the archive: %w", ErrNodeUnavailable, errors.Join(errs...))
}

// lostPruneInterval spaces bucket prunes of one lost node's database: its
// archive no longer grows, and each prune lists and reads the bucket.
const lostPruneInterval = time.Hour

// PruneLostArchives prunes, through a live node, the bucket archives of
// databases whose node is lost: each node's dbbackupd prunes only from its
// own disk, so nothing else would. A database restorable from its archive
// keeps max(restore window, the node's minimum) of it, and at least its
// newest base backup and the WAL after it. It reports how many it pruned.
func (s *Scheduler) PruneLostArchives(ctx context.Context) (int, error) {
	if !s.cfg.SharedArchive {
		return 0, nil
	}
	nodes, err := s.store.Nodes(ctx)
	if err != nil {
		return 0, err
	}
	lost, live := map[string]bool{}, []Node{}
	for _, n := range nodes {
		if n.Lifecycle == NodeUnavailable {
			lost[n.ID] = true
		} else {
			live = append(live, n)
		}
	}
	if len(live) == 0 {
		return 0, nil
	}
	vms, err := s.store.List(ctx, StateParked, StateWaking, StateRunning, StateParking, StateFailed)
	if err != nil {
		return 0, err
	}
	known := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		known[n.ID] = true
	}
	pruned := 0
	for _, vm := range vms {
		if !lost[vm.NodeID] && known[vm.NodeID] {
			continue
		}
		if last, ok := s.lostPruned.Load(vm.DatabaseID); ok && s.now().Sub(last.(time.Time)) < lostPruneInterval {
			continue
		}
		var errs []error
		for _, n := range live {
			err := s.vmm.PruneArchive(ctx, n, vm.DatabaseID, vm.RestoreWindowSeconds)
			if err == nil {
				s.lostPruned.Store(vm.DatabaseID, s.now())
				pruned++
				errs = nil
				break
			}
			errs = append(errs, err)
		}
		if len(errs) > 0 {
			s.log.Warn("dbsched: prune a lost node's archive", "database_id", vm.DatabaseID, "node_id", vm.NodeID, "err", errors.Join(errs...))
		}
	}
	return pruned, nil
}

// CollectOrphans removes database volumes no record names: those of
// databases deleted while their node was lost, found once it is back. A
// record always exists before its volume and outlives it, so a volume
// without one is garbage. Their archives go with them. It also removes a
// volume whose database lives on another node and is not moving here: a
// moved database's old volume or a failed move's copy. That node's archive
// copy goes to the bucket, which keeps it. It reports how many it removed.
func (s *Scheduler) CollectOrphans(ctx context.Context) (int, error) {
	nodes, err := s.store.Nodes(ctx)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, n := range nodes {
		if n.Lifecycle == NodeUnavailable {
			continue
		}
		volumes, err := s.vmm.ListVolumes(ctx, n)
		if err != nil {
			s.log.Warn("dbsched: list volumes", "node_id", n.ID, "err", err)
			continue
		}
		// It answers: standbys may be placed there again.
		s.nodeFailing.Delete(n.ID)
		for _, v := range volumes {
			if ok, err := s.removeOrphan(ctx, n, v); err != nil {
				s.log.Warn("dbsched: remove orphaned volume", "node_id", n.ID, "database_id", v.DatabaseID, "err", err)
			} else if ok {
				s.log.Info("dbsched: removed orphaned volume", "node_id", n.ID, "database_id", v.DatabaseID)
				removed++
			}
		}
	}
	return removed, nil
}

func (s *Scheduler) removeOrphan(ctx context.Context, node Node, v NodeVolume) (bool, error) {
	unlock, ok := s.tryLock(v.DatabaseID)
	if !ok {
		return false, nil // busy here (a move, a delete); the next sweep looks again
	}
	defer unlock()
	vm, err := s.store.Get(ctx, v.DatabaseID)
	moved := err == nil && vm.NodeID != node.ID && vm.MoveNode != node.ID && vm.StandbyNode != node.ID
	if err != nil && !errors.Is(err, ErrNotFound) || err == nil && !moved {
		return false, err
	}
	if v.Running {
		if err := s.vmm.Stop(ctx, node, v.DatabaseID); err != nil {
			return false, err
		}
	}
	if moved {
		return true, s.vmm.ReleaseVolume(ctx, node, v.DatabaseID)
	}
	return true, s.vmm.DeleteVolume(ctx, node, v.DatabaseID)
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
		if s.importing(ctx, vm) {
			_ = s.store.Touch(ctx, vm.DatabaseID, s.now())
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

// SettleLostNodes settles the databases of a node the operator has marked
// unavailable. A replicated database whose standby is synchronized fails
// over to it (standby.go). Any other active one is parked: its VM went with
// the node, and an active record would keep billing compute (an always-on
// one forever); its next wake fails until the node returns. It reports how
// many it settled.
func (s *Scheduler) SettleLostNodes(ctx context.Context) (int, error) {
	nodes, err := s.store.Nodes(ctx)
	if err != nil {
		return 0, err
	}
	lost, known := map[string]bool{}, map[string]Node{}
	for _, n := range nodes {
		known[n.ID] = n
		lost[n.ID] = n.Lifecycle == NodeUnavailable
	}
	vms, err := s.store.List(ctx, append(slices.Clone(activeStates), StateParked, StateFailed)...)
	if err != nil {
		return 0, err
	}
	settled := 0
	for _, vm := range vms {
		if _, ok := known[vm.NodeID]; ok && !lost[vm.NodeID] {
			continue
		}
		if vm.Replicated && canFailover(vm) {
			lostNode, ok := known[vm.NodeID]
			if !ok {
				lostNode = Node{ID: vm.NodeID}
			}
			if err := s.failover(ctx, vm.DatabaseID, lostNode, failoverLostNode); err != nil {
				s.log.Warn("dbsched: fail over a lost node's database", "database_id", vm.DatabaseID, "err", err)
				continue
			}
			settled++
			continue
		}
		if !isActive(vm.State) {
			continue
		}
		unlock := s.lock(vm.DatabaseID)
		err := s.store.Transition(ctx, vm.DatabaseID, activeStates, StateParked, TransitionOptions{LastError: errCodeNodeLost})
		if err == nil {
			s.stopStandby(ctx, vm, false)
		}
		unlock()
		if err != nil {
			s.log.Warn("dbsched: park a lost node's database", "database_id", vm.DatabaseID, "err", err)
			continue
		}
		s.log.Warn("dbsched: parked a database whose node is lost", "database_id", vm.DatabaseID, "node_id", vm.NodeID)
		settled++
	}
	return settled, nil
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
	s.stopStandby(ctx, vm, false)
	s.log.Warn("dbsched: running database had no VM; rebooting it", "database_id", vm.DatabaseID, "node_id", vm.NodeID)
	return true
}

// Recover settles transitions a previous schedd process left in flight:
// a waking or parking database is stopped and parked. Its next wake boots
// it again; PostgreSQL recovers from its WAL if the stop was unclean.
// Restores and upgrades whose lease lapsed resume in the background; moves
// whose lease lapsed are aborted.
func (s *Scheduler) Recover(ctx context.Context) error {
	if err := s.ResumeRestores(ctx); err != nil {
		return err
	}
	if _, err := s.SettleMoves(ctx); err != nil {
		return err
	}
	if err := s.ResumeUpgrades(ctx); err != nil {
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
			s.stopStandby(ctx, vm, false)
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
	if err := s.Observe(ctx); err != nil {
		s.log.Warn("dbsched: catalog observation failed", "err", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var sampled time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if now := s.now(); now.Sub(sampled) >= s.cfg.StorageSampleInterval {
				sampled = now
				if _, err := s.SampleStorage(ctx); err != nil {
					s.log.Warn("dbsched: storage sample failed", "err", err)
				}
				if _, err := s.RunDrills(ctx); err != nil {
					s.log.Warn("dbsched: restore drills failed", "err", err)
				}
			}
			if _, err := s.SettleLostNodes(ctx); err != nil {
				s.log.Warn("dbsched: lost-node sweep failed", "err", err)
			}
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
			if _, err := s.SettleMoves(ctx); err != nil {
				s.log.Warn("dbsched: move settle sweep failed", "err", err)
			}
			if err := s.ResumeUpgrades(ctx); err != nil {
				s.log.Warn("dbsched: upgrade sweep failed", "err", err)
			}
			if err := s.Observe(ctx); err != nil {
				s.log.Warn("dbsched: catalog observation failed", "err", err)
			}
		}
	}
}

// admitWake admits a parked database's wake (ADR-1027 wake storms). While
// its node is at its RAM ceiling or boot slots, the wake waits, up to
// WakeQueueWait, and retries when this process frees a slot or RAM on the
// node, or after a jittered backoff for other replicas'; over the ceiling
// it first parks a database nobody is connected to. Waiting wakes retry
// independently, so admission is not first-come-first-served.
func (s *Scheduler) admitWake(ctx context.Context, vm VM, keepStandbySynced bool) (VM, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.WakeQueueWait)
	defer cancel()
	started, delay := s.now(), 20*time.Millisecond
	for {
		freed := s.freed.wait(vm.NodeID) // before admission, so no signal is missed
		admitted, err := s.store.AdmitWake(ctx, vm.DatabaseID, keepStandbySynced)
		if errors.Is(err, ErrNoCapacity) && s.evictIdle(ctx, vm.NodeID, vm.DatabaseID) {
			continue
		}
		if !errors.Is(err, ErrNoCapacity) && !errors.Is(err, ErrBootSlotsBusy) {
			if err == nil && s.now().Sub(started) > time.Second {
				s.log.Info("dbsched: wake admitted after queueing", "database_id", vm.DatabaseID, "node_id", vm.NodeID, "waited", s.now().Sub(started))
			}
			return admitted, err
		}
		select {
		case <-ctx.Done():
			s.log.Warn("dbsched: wake not admitted", "database_id", vm.DatabaseID, "node_id", vm.NodeID, "err", err)
			return vm, err
		case <-freed:
			continue
		case <-time.After(delay + time.Duration(rand.Int64N(int64(delay)))): //nolint:gosec // jitter, not security
		}
		delay = min(2*delay, 500*time.Millisecond)
	}
}

// evictIdle parks the node's longest-idle scale-to-zero database that has
// had no activity for EvictIdleAfter, to make room for another's wake. A
// database whose lock is busy, or that turned active meanwhile, is
// skipped. It reports whether one was parked.
func (s *Scheduler) evictIdle(ctx context.Context, nodeID, except string) bool {
	if s.cfg.EvictIdleAfter < 0 {
		return false
	}
	running, err := s.store.List(ctx, StateRunning)
	if err != nil {
		return false
	}
	cutoff := s.now().Add(-s.cfg.EvictIdleAfter)
	idle := func(vm VM) bool {
		return vm.NodeID == nodeID && vm.DatabaseID != except && vm.State == StateRunning && !vm.AlwaysOn &&
			!vm.ResizePending && vm.LastActiveAt.Before(cutoff)
	}
	slices.SortFunc(running, func(a, b VM) int { return a.LastActiveAt.Compare(b.LastActiveAt) })
	for _, candidate := range running {
		if !idle(candidate) || s.importing(ctx, candidate) {
			continue
		}
		unlock, ok := s.tryLock(candidate.DatabaseID)
		if !ok {
			continue
		}
		parked := s.evictLocked(ctx, candidate.DatabaseID, idle)
		unlock()
		if parked {
			s.log.Info("dbsched: parked an idle database for a wake", "database_id", candidate.DatabaseID, "node_id", nodeID, "for", except)
			return true
		}
	}
	return false
}

// evictLocked parks a database if it is still idle under its lock.
func (s *Scheduler) evictLocked(ctx context.Context, id string, idle func(VM) bool) bool {
	vm, err := s.store.Get(ctx, id)
	if err != nil || !idle(vm) {
		return false
	}
	if err := s.parkLocked(ctx, id); err != nil {
		s.log.Warn("dbsched: eviction park failed", "database_id", id, "err", err)
		return false
	}
	return true
}
