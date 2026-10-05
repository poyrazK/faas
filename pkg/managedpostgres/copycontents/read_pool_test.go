// adr: 585
package copycontents

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

func readPoolFixture(t *testing.T, readers int, memory, disk int64) (Config, *ReadPool) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := NewReadPool(dir, ReadPoolLimits{Readers: readers, MemoryBytes: memory, DiskBytes: disk, MinFreeBytes: api.PostgresCopyContentsSpoolFreeReserveMin})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	return Config{ReadPool: p, SpoolDir: dir, MaxBytes: 1024, SortMemoryBytes: 32, SortDiskBytes: 64}, p
}

func TestContentsReadPoolBoundsAndConcurrentAdmission(t *testing.T) {
	for _, dimension := range []string{"readers", "memory", "disk"} {
		t.Run(dimension, func(t *testing.T) {
			readers, memory, disk := 2, int64(64), int64(128)
			switch dimension {
			case "readers":
				readers = 1
			case "memory":
				memory = 32
			case "disk":
				disk = 64
			}
			cfg, pool := readPoolFixture(t, readers, memory, disk)
			var wg sync.WaitGroup
			var mu sync.Mutex
			var admitted []Config
			var releases []func()
			for range 12 {
				wg.Go(func() {
					c, release, err := cfg.ReserveReadForWorker(t.Context())
					mu.Lock()
					defer mu.Unlock()
					if err == nil {
						admitted = append(admitted, c)
						releases = append(releases, release)
					} else if !errors.Is(err, pgerrors.ErrQuotaExceeded) {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			if len(releases) != 1 {
				t.Fatalf("admitted %d beyond %s capacity", len(releases), dimension)
			}
			if !errors.Is(pool.Close(), pgerrors.ErrConflict) {
				t.Fatal("owner closed while read held")
			}
			for i, release := range releases {
				if err := admitted[i].CheckReadAdmissionForWorker(t.Context()); err != nil {
					t.Fatal(err)
				}
				release()
				release()
				if !errors.Is(admitted[i].CheckReadAdmissionForWorker(t.Context()), pgerrors.ErrConflict) {
					t.Fatal("released capability accepted")
				}
			}
			_, release, err := cfg.ReserveReadForWorker(t.Context())
			if err != nil {
				t.Fatal("capacity not returned", err)
			}
			release()
		})
	}
}

func TestContentsReadPoolExclusiveOwnershipAndCrashRelease(t *testing.T) {
	if dir := os.Getenv("FAAS_COPY_CONTENTS_POOL_TEST_DIR"); dir != "" {
		p, err := NewReadPool(dir, ReadPoolLimits{Readers: 1, MemoryBytes: 32, DiskBytes: 64, MinFreeBytes: api.PostgresCopyContentsSpoolFreeReserveMin})
		if os.Getenv("FAAS_COPY_CONTENTS_POOL_TEST_HOLD") != "" {
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0600); err != nil {
				t.Fatal(err)
			}
			// Keep a real unlinked sort descriptor and live reservation at exit.
			cfg := Config{ReadPool: p, SpoolDir: dir, MaxBytes: 1024, SortMemoryBytes: 32, SortDiskBytes: 64}
			admitted, _, err := cfg.ReserveReadForWorker(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			s, err := newDigestSorter(t.Context(), dir, 32, 64, [32]byte{1}, nil)
			if err != nil {
				t.Fatal(err)
			}
			s.admission = admitted.readLease
			if err := s.Add(sha256.Sum256([]byte("private row"))); err != nil {
				t.Fatal(err)
			}
			os.Exit(0)
		}
		if !errors.Is(err, pgerrors.ErrConflict) {
			if p != nil {
				_ = p.Close()
			}
			t.Fatal("second process admitted", err)
		}
		return
	}
	cfg, pool := readPoolFixture(t, 1, 32, 64)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(cfg.SpoolDir, alias); err != nil {
		t.Fatal(err)
	}
	if p, err := NewReadPool(alias, pool.limits); !errors.Is(err, pgerrors.ErrConflict) {
		if p != nil {
			_ = p.Close()
		}
		t.Fatal("path alias split ownership", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestContentsReadPoolExclusiveOwnershipAndCrashRelease$")
	cmd.Env = append(os.Environ(), "FAAS_COPY_CONTENTS_POOL_TEST_DIR="+cfg.SpoolDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cross-process lock: %v %s", err, out)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(os.Args[0], "-test.run=^TestContentsReadPoolExclusiveOwnershipAndCrashRelease$")
	cmd.Env = append(os.Environ(), "FAAS_COPY_CONTENTS_POOL_TEST_DIR="+cfg.SpoolDir, "FAAS_COPY_CONTENTS_POOL_TEST_HOLD=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("crash owner: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(cfg.SpoolDir, "ready")); err != nil {
		t.Fatal("child did not acquire ownership", err)
	}
	p, err := NewReadPool(cfg.SpoolDir, pool.limits)
	if err != nil {
		t.Fatal("process exit retained lock", err)
	}
	defer p.Close()
	for _, entry := range mustReadPoolDir(t, cfg.SpoolDir) {
		if entry.IsDir() {
			files := mustReadPoolDir(t, filepath.Join(cfg.SpoolDir, entry.Name()))
			if len(files) != 0 {
				t.Fatal("crashed sort retained data")
			}
		}
	}
}

func mustReadPoolDir(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestContentsReadPoolRequiresPrivateDirectoryAndFixedLimits(t *testing.T) {
	for _, mode := range []string{"relative", "readers", "memory", "disk", "free", "public", "marker_symlink"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			limits := ReadPoolLimits{Readers: 1, MemoryBytes: 32, DiskBytes: 64, MinFreeBytes: api.PostgresCopyContentsSpoolFreeReserveMin}
			switch mode {
			case "relative":
				dir = "."
			case "readers":
				limits.Readers = api.PostgresCopyContentsReadersPerWorkerMax + 1
			case "memory":
				limits.MemoryBytes = api.PostgresCopyContentsSortMemoryPerWorkerMax + 1
			case "disk":
				limits.DiskBytes = api.PostgresCopyContentsSortDiskPerWorkerMax + 1
			case "free":
				limits.MinFreeBytes--
			case "public":
				if err := os.Chmod(dir, 0755); err != nil {
					t.Fatal(err)
				}
			case "marker_symlink":
				if err := os.Symlink(filepath.Join(t.TempDir(), "unrelated"), filepath.Join(dir, readPoolLockName)); err != nil {
					t.Fatal(err)
				}
			}
			if p, err := NewReadPool(dir, limits); err == nil || p != nil {
				if p != nil {
					_ = p.Close()
				}
				t.Fatal("invalid pool policy acquired ownership", err)
			}
		})
	}
	cfg := Config{ReadPool: &ReadPool{}, SpoolDir: t.TempDir(), MaxBytes: 1, SortMemoryBytes: 32, SortDiskBytes: 32}
	if _, _, err := cfg.ReserveReadForWorker(t.Context()); !errors.Is(err, pgerrors.ErrUnavailable) {
		t.Fatal("zero pool admitted", err)
	}
}

func TestContentsReadPoolRejectsInvalidLostAndChangedAuthority(t *testing.T) {
	cfg, pool := readPoolFixture(t, 1, 32, 64)
	for _, change := range []string{"missing", "path", "cancelled", "free"} {
		t.Run(change, func(t *testing.T) {
			c := cfg
			ctx := t.Context()
			switch change {
			case "missing":
				c.ReadPool = nil
			case "path":
				c.SpoolDir = t.TempDir()
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "free":
				pool.limits.MinFreeBytes = int64(^uint64(0) >> 1)
				defer func() { pool.limits.MinFreeBytes = api.PostgresCopyContentsSpoolFreeReserveMin }()
			}
			if admitted, release, err := c.ReserveReadForWorker(ctx); err == nil || release != nil || admitted.readLease != nil {
				t.Fatal("invalid admission returned authority", err)
			}
		})
	}
	c, release, err := cfg.ReserveReadForWorker(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	changed := c
	changed.SortDiskBytes++
	if !errors.Is(changed.CheckReadAdmissionForWorker(t.Context()), pgerrors.ErrConflict) {
		t.Fatal("expanded reservation")
	}
	if _, _, err := c.ReserveReadForWorker(t.Context()); !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatal("one reservation shared by worker invocations", err)
	}
	if err := os.Rename(filepath.Join(cfg.SpoolDir, readPoolLockName), filepath.Join(cfg.SpoolDir, "moved-lock")); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(c.CheckReadAdmissionForWorker(t.Context()), pgerrors.ErrConflict) {
		t.Fatal("lost owner inode accepted")
	}
}

func TestContentsReadPoolSortHeadroomAndReleaseDuringRead(t *testing.T) {
	cfg, pool := readPoolFixture(t, 1, 32, 1024)
	cfg.SortDiskBytes = 1024
	c, release, err := cfg.ReserveReadForWorker(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.readLease.begin(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(c.readLease.begin(t.Context()), pgerrors.ErrConflict) {
		t.Fatal("one reservation funded parallel reads")
	}
	s, err := newDigestSorter(t.Context(), cfg.SpoolDir, 32, 1024, [32]byte{1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.admission = c.readLease
	pool.limits.MinFreeBytes = int64(^uint64(0) >> 1)
	if err := s.Add(sha256.Sum256([]byte("row"))); !errors.Is(err, pgerrors.ErrQuotaExceeded) {
		t.Fatal("sort ignored headroom", err)
	}
	pool.limits.MinFreeBytes = api.PostgresCopyContentsSpoolFreeReserveMin
	release()
	if !errors.Is(pool.Close(), pgerrors.ErrConflict) {
		t.Fatal("released while sort still running")
	}
	if _, _, err := cfg.ReserveReadForWorker(t.Context()); !errors.Is(err, pgerrors.ErrQuotaExceeded) {
		t.Fatal("in-flight capacity reused", err)
	}
	s.Close()
	c.readLease.end()
	_, release, err = cfg.ReserveReadForWorker(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	release()
}
