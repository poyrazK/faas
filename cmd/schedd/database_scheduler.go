package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/dbsched"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// DatabaseSchedulerConfig enables schedd's database scheduler (ADR-1027):
//
//	[database_scheduler]
//	idle_timeout = "5m"
//	[[database_scheduler.nodes]]
//	id = "db-1"
//	target_url = "tcp://10.0.2.10:7443"
//	admission_ceiling_mb = 48000
//	volume_capacity_bytes = 3000000000000
//
// Listed nodes are upserted into database_nodes at startup. Without the
// table the database RPCs return UNIMPLEMENTED.
type DatabaseSchedulerConfig struct {
	IdleTimeout   time.Duration        `toml:"idle_timeout"`
	SweepInterval time.Duration        `toml:"sweep_interval"`
	Nodes         []DatabaseNodeConfig `toml:"nodes"`
}

// DatabaseNodeConfig is one database node.
type DatabaseNodeConfig struct {
	ID                  string `toml:"id"`
	TargetURL           string `toml:"target_url"`
	AdmissionCeilingMB  int    `toml:"admission_ceiling_mb"`
	VolumeCapacityBytes int64  `toml:"volume_capacity_bytes"`
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
	case lifecycle != dbsched.NodeActive && lifecycle != dbsched.NodeDraining && lifecycle != dbsched.NodeUnavailable:
		return dbsched.Node{}, fmt.Errorf("database_scheduler.nodes %s: unknown lifecycle %q", c.ID, lifecycle)
	}
	return dbsched.Node{ID: c.ID, TargetURL: c.TargetURL, AdmissionCeilingMB: c.AdmissionCeilingMB, VolumeCapacityBytes: c.VolumeCapacityBytes, Lifecycle: lifecycle}, nil
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
func enableDatabaseScheduler(ctx context.Context, cfg *DatabaseSchedulerConfig, pool *pgxpool.Pool, loadVMMTLS func(wire.NodeVerifier) (*tls.Config, error), handler *scheddgrpc.Server, log *slog.Logger) (func(), error) {
	if cfg == nil {
		return func() {}, nil
	}
	vmmTLS, err := loadVMMTLS(databaseNodeVerifier(cfg))
	if err != nil {
		return nil, fmt.Errorf("schedd: database node TLS: %w", err)
	}
	store, err := dbsched.NewPgStore(pool)
	if err != nil {
		return nil, fmt.Errorf("schedd: database store: %w", err)
	}
	for _, nc := range cfg.Nodes {
		node, err := nc.node()
		if err != nil {
			return nil, fmt.Errorf("schedd: %w", err)
		}
		if err := store.UpsertNode(ctx, node); err != nil {
			return nil, fmt.Errorf("schedd: register database node: %w", err)
		}
	}
	vmm := dbsched.NewGRPCVMM(vmmTLS)
	sched := dbsched.New(store, vmm, dbsched.Config{IdleTimeout: cfg.IdleTimeout}, log)
	if err := sched.Recover(ctx); err != nil {
		_ = vmm.Close()
		return nil, fmt.Errorf("schedd: recover databases: %w", err)
	}
	sweep := cfg.SweepInterval
	if sweep <= 0 {
		sweep = 30 * time.Second
	}
	loopCtx, stop := context.WithCancel(ctx)
	go sched.RunIdleLoop(loopCtx, sweep)
	go sched.RunBackupLoop(loopCtx, 10*time.Minute)
	handler.WithDatabases(sched)
	log.Info("schedd: database scheduler enabled", "nodes", len(cfg.Nodes), "idle_timeout", cfg.IdleTimeout, "sweep_interval", sweep)
	return func() {
		stop()
		_ = vmm.Close()
	}, nil
}
