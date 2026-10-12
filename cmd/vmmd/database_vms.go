package main

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

// DatabaseVMConfig enables database VMs on a database node (ADR-1027):
//
//	[database_vms]
//	kernel_path = "/srv/fc/kernel/1.7.0"
//	[database_vms.bases]
//	"17" = "/srv/fc/base/postgres-17.ext4"
//
// Without the table vmmd keeps the database RPCs UNIMPLEMENTED.
type DatabaseVMConfig struct {
	JailBase     string            `toml:"jail_base"`
	VolumeRoot   string            `toml:"volume_root"`
	KernelPath   string            `toml:"kernel_path"`
	Bases        map[string]string `toml:"bases"`
	ParentCgroup string            `toml:"parent_cgroup"`
}

const (
	defaultDatabaseJailBase     = "/srv/fc/dbjail"
	defaultDatabaseVolumeRoot   = "/srv/db/volumes"
	defaultDatabaseParentCgroup = "faas.slice/faas-db.slice"
)

func (c DatabaseVMConfig) runtimeConfig(leases dbvm.Leaser) (dbvm.RuntimeConfig, error) {
	if c.KernelPath == "" || len(c.Bases) == 0 {
		return dbvm.RuntimeConfig{}, errors.New("database_vms needs kernel_path and at least one base")
	}
	bases := make(map[int]string, len(c.Bases))
	for key, path := range c.Bases {
		major, err := strconv.Atoi(key)
		if err != nil || major < dbguest.MinMajor || major > dbguest.MaxMajor || path == "" {
			return dbvm.RuntimeConfig{}, fmt.Errorf("database_vms.bases: invalid entry %q", key)
		}
		bases[major] = path
	}
	return dbvm.RuntimeConfig{
		JailBase:     orDefault(c.JailBase, defaultDatabaseJailBase),
		VolumeRoot:   orDefault(c.VolumeRoot, defaultDatabaseVolumeRoot),
		KernelPath:   c.KernelPath,
		Bases:        bases,
		ParentCgroup: orDefault(c.ParentCgroup, defaultDatabaseParentCgroup),
		Leases:       leases,
	}, nil
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// enableDatabaseVMs builds the runtime, cleans up jails a previous vmmd
// left behind, and turns the database RPCs on.
func enableDatabaseVMs(impl *vmmdgrpc.Server, cfg *DatabaseVMConfig, leases dbvm.Leaser, log *slog.Logger) error {
	if cfg == nil {
		return nil
	}
	rcfg, err := cfg.runtimeConfig(leases)
	if err != nil {
		return fmt.Errorf("vmmd: %w", err)
	}
	rt, err := dbvm.NewRuntime(rcfg)
	if err != nil {
		return fmt.Errorf("vmmd: database runtime: %w", err)
	}
	if err := rt.Recover(); err != nil {
		return fmt.Errorf("vmmd: recover database jails: %w", err)
	}
	impl.WithDatabases(rt)
	log.Info("vmmd: database VMs enabled", "jail_base", rcfg.JailBase, "volume_root", rcfg.VolumeRoot, "majors", len(rcfg.Bases))
	return nil
}
