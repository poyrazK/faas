package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbsched"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// DatabaseSchedulerConfig enables schedd's database scheduler (ADR-1027):
//
//	[database_scheduler]
//	idle_timeout = "5m"
//	wake_queue_wait = "30s"
//	evict_idle_after = "2m"
//	[[database_scheduler.nodes]]
//	id = "db-1"
//	target_url = "tcp://10.0.2.10:7443"
//	admission_ceiling_mb = 48000
//	volume_capacity_bytes = 3000000000000
//	boot_slots = 8
//	postgres_majors = [14, 15, 16, 17, 18]
//
// Listed nodes are upserted into database_nodes at startup. Without the
// table the database RPCs return UNIMPLEMENTED.
type DatabaseSchedulerConfig struct {
	IdleTimeout   time.Duration `toml:"idle_timeout"`
	SweepInterval time.Duration `toml:"sweep_interval"`
	// WakeQueueWait bounds how long a wake waits for its node to admit it
	// (RAM ceiling, boot slots); EvictIdleAfter is how long a database
	// must have had no activity to be parked for another's wake ("-1s"
	// disables it). Zero takes dbsched's defaults.
	WakeQueueWait  time.Duration `toml:"wake_queue_wait"`
	EvictIdleAfter time.Duration `toml:"evict_idle_after"`
	// EvacuateDraining moves databases off nodes whose lifecycle is
	// draining (needs shared_archive); MoveTimeout bounds one move's copy.
	EvacuateDraining bool          `toml:"evacuate_draining"`
	MoveTimeout      time.Duration `toml:"move_timeout"`
	// AutoFailoverAfter fails a running production-class database over to
	// its synchronized standby once its primary's node has not answered
	// for this long (default 30s; negative leaves it to the operator's
	// lost-node mark).
	AutoFailoverAfter time.Duration `toml:"auto_failover_after"`
	// StandbyResumeTimeout bounds how long a woken production-class
	// database's commits wait for its synchronized standby to come back
	// before it turns asynchronous (default 30s; negative starts every
	// wake asynchronous, as before ADR-1027's resume).
	StandbyResumeTimeout time.Duration `toml:"standby_resume_timeout"`
	// SharedArchive: every database node's dbbackupd uploads to the same
	// bucket and prefix, so restores may run on any node (ADR-1027).
	SharedArchive bool                 `toml:"shared_archive"`
	Nodes         []DatabaseNodeConfig `toml:"nodes"`
}

// DatabaseNodeConfig is one database node.
type DatabaseNodeConfig struct {
	ID                  string `toml:"id"`
	TargetURL           string `toml:"target_url"`
	AdmissionCeilingMB  int    `toml:"admission_ceiling_mb"`
	VolumeCapacityBytes int64  `toml:"volume_capacity_bytes"`
	// BootSlots caps the databases booting on the node at once (default
	// dbsched.DefaultBootSlots); roughly its physical cores.
	BootSlots int `toml:"boot_slots"`
	// PostgresMajors lists the majors whose base drives the node's vmmd
	// builds (its [database_vms.images]); empty means any. Databases are
	// placed, moved and restored only onto nodes offering their major.
	PostgresMajors []int `toml:"postgres_majors"`
	// Lifecycle is active (default), draining or unavailable.
	Lifecycle string `toml:"lifecycle"`
}

func (c DatabaseNodeConfig) node() (dbsched.Node, error) {
	lifecycle := c.Lifecycle
	if lifecycle == "" {
		lifecycle = dbsched.NodeActive
	}
	switch {
	case c.ID == "" || c.TargetURL == "":
		return dbsched.Node{}, errors.New("database_scheduler.nodes: id and target_url are required")
	case c.AdmissionCeilingMB <= 0 || c.VolumeCapacityBytes <= 0:
		return dbsched.Node{}, fmt.Errorf("database_scheduler.nodes %s: admission_ceiling_mb and volume_capacity_bytes must be positive", c.ID)
	case c.BootSlots < 0 || c.BootSlots > 256:
		return dbsched.Node{}, fmt.Errorf("database_scheduler.nodes %s: boot_slots must be 1-256", c.ID)
	case slices.ContainsFunc(c.PostgresMajors, func(m int) bool { return m < dbguest.MinMajor || m > dbguest.MaxMajor }):
		return dbsched.Node{}, fmt.Errorf("database_scheduler.nodes %s: postgres_majors must be %d-%d", c.ID, dbguest.MinMajor, dbguest.MaxMajor)
	case lifecycle != dbsched.NodeActive && lifecycle != dbsched.NodeDraining && lifecycle != dbsched.NodeUnavailable:
		return dbsched.Node{}, fmt.Errorf("database_scheduler.nodes %s: unknown lifecycle %q", c.ID, lifecycle)
	}
	return dbsched.Node{ID: c.ID, TargetURL: c.TargetURL, AdmissionCeilingMB: c.AdmissionCeilingMB, VolumeCapacityBytes: c.VolumeCapacityBytes,
		BootSlots: c.BootSlots, Majors: c.PostgresMajors, Lifecycle: lifecycle}, nil
}

// databaseNodeVerifier binds database-node vmmd certificates to the
// configured nodes. A database node's vmmd leaf carries the node identity
// <id>.faas (`gregalectl pki issue-bundle --box-role database --cn <id>`).
func databaseNodeVerifier(cfg *DatabaseSchedulerConfig) wire.NodeVerifier {
	names := make([]string, 0, len(cfg.Nodes))
	for _, node := range cfg.Nodes {
		id := strings.TrimSpace(node.ID)
		if id == "" {
			continue
		}
		if !strings.HasSuffix(id, ".faas") {
			id += ".faas"
		}
		names = append(names, id)
	}
	verifier := wire.NewInmemNodeVerifier()
	verifier.Set(names)
	return verifier
}

// enableDatabaseScheduler registers the configured database nodes, settles
// transitions a previous schedd left in flight, starts the idle sweep and
// turns the database RPCs on. It returns a cleanup func.
//
// loadVMMTLS builds the client TLS for database-node vmmd. It receives
// databaseNodeVerifier(cfg): database nodes are not in compute_nodes, so
// the compute registry verifier would refuse their vmmd certificates.
func enableDatabaseScheduler(ctx context.Context, cfg *DatabaseSchedulerConfig, pool *pgxpool.Pool, loadVMMTLS func(wire.NodeVerifier) (*tls.Config, error), handler *scheddgrpc.Server, ops *wire.OpsMetrics, log *slog.Logger) (*dbsched.Scheduler, func(), error) {
	if cfg == nil {
		return nil, func() {}, nil
	}
	vmmTLS, err := loadVMMTLS(databaseNodeVerifier(cfg))
	if err != nil {
		return nil, nil, fmt.Errorf("schedd: database node TLS: %w", err)
	}
	store, err := dbsched.NewPgStore(pool)
	if err != nil {
		return nil, nil, fmt.Errorf("schedd: database store: %w", err)
	}
	for _, nc := range cfg.Nodes {
		node, err := nc.node()
		if err != nil {
			return nil, nil, fmt.Errorf("schedd: %w", err)
		}
		if err := store.UpsertNode(ctx, node); err != nil {
			return nil, nil, fmt.Errorf("schedd: register database node: %w", err)
		}
	}
	vmm := dbsched.NewGRPCVMM(vmmTLS)
	var metrics *dbsched.Metrics
	if ops != nil {
		metrics = dbsched.NewMetrics(ops.Registry(), ops.MetricPrefix())
	}
	sched := dbsched.New(store, vmm, dbsched.Config{IdleTimeout: cfg.IdleTimeout, SharedArchive: cfg.SharedArchive,
		WakeQueueWait: cfg.WakeQueueWait, EvictIdleAfter: cfg.EvictIdleAfter,
		EvacuateDraining: cfg.EvacuateDraining, MoveTimeout: cfg.MoveTimeout, AutoFailoverAfter: cfg.AutoFailoverAfter,
		StandbyResumeTimeout: cfg.StandbyResumeTimeout, Metrics: metrics}, log)
	if err := sched.Recover(ctx); err != nil {
		_ = vmm.Close()
		return nil, nil, fmt.Errorf("schedd: recover databases: %w", err)
	}
	sweep := cfg.SweepInterval
	if sweep <= 0 {
		sweep = 30 * time.Second
	}
	loopCtx, stop := context.WithCancel(ctx)
	go sched.RunIdleLoop(loopCtx, sweep)
	go sched.RunBackupLoop(loopCtx, 10*time.Minute)
	// Replicated databases' standbys (production class): a standby that
	// stops keeping up holds its primary's commits until the next check.
	go sched.RunStandbyLoop(loopCtx)
	handler.WithDatabases(sched)
	log.Info("schedd: database scheduler enabled", "nodes", len(cfg.Nodes), "idle_timeout", cfg.IdleTimeout, "sweep_interval", sweep)
	return sched, func() {
		stop()
		_ = vmm.Close()
	}, nil
}

// databaseMover starts operators' database moves (database_move intents)
// and runs their in-place restarts (database_restart) on the database
// scheduler.
type databaseMover struct{ sched *dbsched.Scheduler }

func (m databaseMover) StartDatabaseMove(ctx context.Context, databaseID, targetNode string) (sched.DatabaseMove, error) {
	vm, err := m.sched.StartMove(ctx, databaseID, targetNode)
	if err != nil {
		return sched.DatabaseMove{}, err
	}
	return sched.DatabaseMove{From: vm.NodeID, To: vm.MoveNode}, nil
}

func (m databaseMover) RestartDatabase(ctx context.Context, databaseID string) (string, error) {
	vm, err := m.sched.Restart(ctx, databaseID)
	return string(vm.State), err
}
