package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// DatabaseVMConfig enables database VMs (ADR-1027). On a database node
// (role = "database") it is required; there vmmd runs nothing else.
//
//	[database_vms]
//	kernel_path = "/srv/fc/kernel/1.7.0"
//	volume_root = "/srv/db/volumes"
//	[database_vms.images]
//	"17" = "docker.io/library/postgres@sha256:<linux/amd64 manifest>"
//
// images are built into base drives on the node, keyed by the image and
// the release's guest-init, so a release with a new guest-init rebuilds
// them. bases names prebuilt drives instead (development).
type DatabaseVMConfig struct {
	JailBase     string            `toml:"jail_base"`
	VolumeRoot   string            `toml:"volume_root"`
	KernelPath   string            `toml:"kernel_path"`
	Bases        map[string]string `toml:"bases"`
	Images       map[string]string `toml:"images"`
	BaseDir      string            `toml:"base_dir"`
	GuestInit    string            `toml:"guest_init"`
	ParentCgroup string            `toml:"parent_cgroup"`
	// ArchiveSocket is dbbackupd's socket. When set, every database VM
	// archives its WAL there through vmmd's relay; empty leaves archiving
	// off.
	ArchiveSocket string `toml:"archive_socket"`
	// Spares keeps booted VMs a wake adopts instead of cold-booting
	// (ADR-1027), per major and class shape:
	//
	//	[[database_vms.spares]]
	//	postgres_major = 17
	//	vcpu_count = 1
	//	mem_mib = 512
	//	count = 2
	//
	// Their memory is outside schedd's accounting: lower the node's
	// admission_ceiling_mb by count x (mem_mib + 8) for every shape.
	Spares []DatabaseSpareConfig `toml:"spares"`
	// ArchiveQuotaFactor caps each database's archive on the node at that
	// many times its volume size (default 10; negative is no cap).
	ArchiveQuotaFactor int `toml:"archive_quota_factor"`
	// PeerTLSCertPath and PeerTLSKeyPath are the node's own identity
	// (<id>.faas) for dialing other database nodes, which a standby here
	// needs to stream from its primary there (ADR-1027 production class).
	// Without them the node runs no standbys.
	PeerTLSCertPath string `toml:"peer_tls_cert_path"`
	PeerTLSKeyPath  string `toml:"peer_tls_key_path"`
}

// DatabaseSpareConfig is one spare pool.
type DatabaseSpareConfig struct {
	PostgresMajor int `toml:"postgres_major"`
	VcpuCount     int `toml:"vcpu_count"`
	MemMiB        int `toml:"mem_mib"`
	Count         int `toml:"count"`
}

// databaseSpareInterval is how often vmmd tops the spare pools up; a wake
// that adopts a spare refills its pool at once.
const databaseSpareInterval = 10 * time.Second

const (
	defaultDatabaseJailBase     = "/srv/fc/dbjail"
	defaultDatabaseVolumeRoot   = "/srv/db/volumes"
	defaultDatabaseParentCgroup = "faas.slice/faas-db.slice"
	defaultDatabaseBaseDir      = "/srv/fc/dbbase"
	defaultDatabaseGuestInit    = "/opt/faas/current/bin/init"
	databaseBaseRetry           = time.Minute
)

// databaseImage is one major built from a pinned image.
type databaseImage struct {
	major int
	image string
}

func parseMajor(key string) (int, error) {
	major, err := strconv.Atoi(key)
	if err != nil || major < dbguest.MinMajor || major > dbguest.MaxMajor {
		return 0, fmt.Errorf("database_vms: invalid PostgreSQL major %q", key)
	}
	return major, nil
}

func (c DatabaseVMConfig) runtimeConfig(leases dbvm.Leaser) (dbvm.RuntimeConfig, []databaseImage, error) {
	if c.KernelPath == "" || len(c.Bases)+len(c.Images) == 0 {
		return dbvm.RuntimeConfig{}, nil, errors.New("database_vms needs kernel_path and at least one base or image")
	}
	bases := make(map[int]string, len(c.Bases))
	for key, path := range c.Bases {
		major, err := parseMajor(key)
		if err != nil {
			return dbvm.RuntimeConfig{}, nil, err
		}
		if path == "" {
			return dbvm.RuntimeConfig{}, nil, fmt.Errorf("database_vms.bases: empty path for %d", major)
		}
		bases[major] = path
	}
	var images []databaseImage
	for key, image := range c.Images {
		major, err := parseMajor(key)
		if err != nil {
			return dbvm.RuntimeConfig{}, nil, err
		}
		if _, dup := bases[major]; dup {
			return dbvm.RuntimeConfig{}, nil, fmt.Errorf("database_vms: PostgreSQL %d has both a base and an image", major)
		}
		if err := dbvm.ValidateImage(image); err != nil {
			return dbvm.RuntimeConfig{}, nil, err
		}
		images = append(images, databaseImage{major: major, image: image})
	}
	slices.SortFunc(images, func(a, b databaseImage) int { return a.major - b.major })
	spares, err := c.spareShapes()
	if err != nil {
		return dbvm.RuntimeConfig{}, nil, err
	}
	return dbvm.RuntimeConfig{
		JailBase:           orDefault(c.JailBase, defaultDatabaseJailBase),
		VolumeRoot:         orDefault(c.VolumeRoot, defaultDatabaseVolumeRoot),
		KernelPath:         c.KernelPath,
		Bases:              bases,
		ParentCgroup:       orDefault(c.ParentCgroup, defaultDatabaseParentCgroup),
		Leases:             leases,
		ArchiveSocket:      c.ArchiveSocket,
		Spares:             spares,
		ArchiveQuotaFactor: c.ArchiveQuotaFactor,
	}, images, nil
}

func (c DatabaseVMConfig) spareShapes() (map[dbvm.SpareShape]int, error) {
	if len(c.Spares) == 0 {
		return nil, nil
	}
	out := make(map[dbvm.SpareShape]int, len(c.Spares))
	for _, s := range c.Spares {
		shape := dbvm.SpareShape{Major: s.PostgresMajor, VcpuCount: s.VcpuCount, MemSizeMiB: s.MemMiB}
		switch {
		case s.PostgresMajor < dbguest.MinMajor || s.PostgresMajor > dbguest.MaxMajor:
			return nil, fmt.Errorf("database_vms.spares: invalid PostgreSQL major %d", s.PostgresMajor)
		case s.VcpuCount < 1 || s.VcpuCount > 32 || s.MemMiB < 128 || s.MemMiB > 65536:
			return nil, fmt.Errorf("database_vms.spares: invalid shape %d vCPU / %d MiB", s.VcpuCount, s.MemMiB)
		case s.Count < 1 || s.Count > 64:
			return nil, fmt.Errorf("database_vms.spares: count %d must be 1-64", s.Count)
		}
		if _, dup := out[shape]; dup {
			return nil, fmt.Errorf("database_vms.spares: PostgreSQL %d %d vCPU / %d MiB listed twice", s.PostgresMajor, s.VcpuCount, s.MemMiB)
		}
		out[shape] = s.Count
	}
	return out, nil
}

// keepDatabaseSpares tops the spare pools up until ctx ends, then stops
// the spares no database adopted. A major whose base is still building
// fails to boot spares until it is ready.
func keepDatabaseSpares(ctx context.Context, rt *dbvm.Runtime, log *slog.Logger) {
	ticker := time.NewTicker(databaseSpareInterval)
	defer ticker.Stop()
	for {
		if n, err := rt.FillSpares(ctx); err != nil && ctx.Err() == nil {
			log.Warn("vmmd: database spares", "booted", n, "err", err)
		} else if n > 0 {
			log.Info("vmmd: booted database spares", "count", n)
		}
		select {
		case <-ctx.Done():
			stopped := rt.StopSpares(context.WithoutCancel(ctx))
			log.Info("vmmd: stopped database spares", "count", stopped)
			return
		case <-ticker.C:
		}
	}
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// newDatabaseRuntime builds the runtime, adopts the database VMs a previous
// vmmd left running and cleans up the rest of its jails (ADR-1027).
func newDatabaseRuntime(cfg *DatabaseVMConfig, leases dbvm.Leaser, log *slog.Logger) (*dbvm.Runtime, []databaseImage, error) {
	rcfg, images, err := cfg.runtimeConfig(leases)
	if err != nil {
		return nil, nil, fmt.Errorf("vmmd: %w", err)
	}
	rcfg.Log = log
	rt, err := dbvm.NewRuntime(rcfg)
	if err != nil {
		return nil, nil, fmt.Errorf("vmmd: database runtime: %w", err)
	}
	if err := rt.Recover(); err != nil {
		return nil, nil, fmt.Errorf("vmmd: recover database jails: %w", err)
	}
	return rt, images, nil
}

// buildDatabaseBases makes every image's base available to the runtime,
// building the missing ones, and reports through ready. A failed build is
// retried; until it succeeds that major cannot boot.
func buildDatabaseBases(ctx context.Context, rt *dbvm.Runtime, cfg *DatabaseVMConfig, images []databaseImage, ready *wire.ReadySignal, log *slog.Logger) {
	if len(images) == 0 {
		ready.Set(true, "")
		return
	}
	guestInit := orDefault(cfg.GuestInit, defaultDatabaseGuestInit)
	dir := orDefault(cfg.BaseDir, defaultDatabaseBaseDir)
	for _, img := range images {
		for {
			err := ensureDatabaseBase(ctx, rt, dir, guestInit, img, ready, log)
			if err == nil {
				break
			}
			log.Error("vmmd: database base build failed", "major", img.major, "image", img.image, "err", err)
			ready.Set(false, fmt.Sprintf("PostgreSQL %d base build failed: %v", img.major, err))
			select {
			case <-ctx.Done():
				return
			case <-time.After(databaseBaseRetry):
			}
		}
	}
	ready.Set(true, "")
}

func ensureDatabaseBase(ctx context.Context, rt *dbvm.Runtime, dir, guestInit string, img databaseImage, ready *wire.ReadySignal, log *slog.Logger) error {
	digest, err := dbvm.FileDigest(guestInit)
	if err != nil {
		return fmt.Errorf("guest-init %s: %w", guestInit, err)
	}
	path := dbvm.BasePath(dir, img.major, img.image, digest)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		ready.Set(false, fmt.Sprintf("building PostgreSQL %d base", img.major))
		started := time.Now()
		if err := dbvm.BuildBase(ctx, img.image, guestInit, path); err != nil {
			return err
		}
		log.Info("vmmd: built database base", "major", img.major, "path", path, "duration", time.Since(started).Round(time.Millisecond))
	} else if err != nil {
		return err
	}
	rt.SetBase(img.major, path)
	if err := dbvm.PruneBases(dir, img.major, path); err != nil {
		log.Warn("vmmd: prune old database bases", "major", img.major, "err", err)
	}
	return nil
}

// enableDatabaseVMs turns the database RPCs on beside compute (single-box
// development). Database nodes use runDatabaseNode instead.
func enableDatabaseVMs(ctx context.Context, impl *vmmdgrpc.Server, cfg *DatabaseVMConfig, leases dbvm.Leaser, log *slog.Logger) error {
	if cfg == nil {
		return nil
	}
	rt, images, err := newDatabaseRuntime(cfg, leases, log) //nolint:contextcheck // adopted VMs' relays outlive startup
	if err != nil {
		return err
	}
	var probe wire.ReadyzProbe
	go buildDatabaseBases(ctx, rt, cfg, images, probe.Register(), log)
	if len(cfg.Spares) > 0 {
		go keepDatabaseSpares(ctx, rt, log)
	}
	impl.WithDatabases(rt)
	log.Info("vmmd: database VMs enabled", "images", len(images), "bases", len(cfg.Bases))
	return nil
}
