package copycontents

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"golang.org/x/sys/unix"
)

const readPoolLockName = ".gregale-contents-read-pool"

// ReadPoolLimits are explicit operator safety bounds for one private spool.
// MemoryBytes covers sort buffers, not total process or PostgreSQL RSS. Slots
// bound parallel readers but do not enforce a CPU quota.
type ReadPoolLimits struct {
	Readers                              int
	MemoryBytes, DiskBytes, MinFreeBytes int64
}

// ReadPool has one OS-locked owner. All contents readers on this worker must
// share it. A second process or pool cannot independently account for the same
// directory. The marker must never be unlinked, including during rollout.
// Unlinked digest files close on process death; the OS then releases ownership.
type ReadPool struct {
	mu           sync.Mutex
	dir          string
	root, lock   *os.File
	limits       ReadPoolLimits
	readers      int
	memory, disk int64
	closed       bool
}

func NewReadPool(dir string, limits ReadPoolLimits) (*ReadPool, error) {
	if !filepath.IsAbs(dir) || limits.Readers < 1 || limits.Readers > api.PostgresCopyContentsReadersPerWorkerMax ||
		limits.MemoryBytes < 32 || limits.MemoryBytes > api.PostgresCopyContentsSortMemoryPerWorkerMax ||
		limits.DiskBytes < 32 || limits.DiskBytes > api.PostgresCopyContentsSortDiskPerWorkerMax || limits.MinFreeBytes < api.PostgresCopyContentsSpoolFreeReserveMin {
		return nil, pgerrors.ErrInvalid
	}
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, pgerrors.ErrUnavailable
	}
	fd, err := unix.Open(canonical, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, pgerrors.ErrUnavailable
	}
	root := os.NewFile(uintptr(fd), canonical)
	p := &ReadPool{dir: canonical, root: root, limits: limits}
	ok := false
	defer func() {
		if !ok {
			if p.lock != nil {
				_ = p.lock.Close()
			}
			_ = root.Close()
		}
	}()
	info, err := root.Stat()
	var stat unix.Stat_t
	if err != nil || info.Mode().Perm() != 0700 || unix.Fstat(fd, &stat) != nil || stat.Uid != uint32(os.Geteuid()) {
		return nil, pgerrors.ErrInvalid
	}
	lockFD, err := unix.Openat(fd, readPoolLockName, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, pgerrors.ErrUnavailable
	}
	p.lock = os.NewFile(uintptr(lockFD), filepath.Join(canonical, readPoolLockName))
	lockInfo, err := p.lock.Stat()
	if err != nil || !lockInfo.Mode().IsRegular() || lockInfo.Mode().Perm() != 0600 || unix.Fstat(lockFD, &stat) != nil || stat.Uid != uint32(os.Geteuid()) {
		return nil, pgerrors.ErrInvalid
	}
	if err = unix.Flock(lockFD, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, pgerrors.ErrConflict
		}
		return nil, pgerrors.ErrUnavailable
	}
	if err = p.checkLocked(); err != nil {
		return nil, err
	}
	ok = true
	return p, nil
}

// Close refuses to surrender ownership while readers retain open sort files.
func (p *ReadPool) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	if p.readers != 0 {
		return pgerrors.ErrConflict
	}
	p.closed = true
	return errors.Join(p.lock.Close(), p.root.Close())
}

func (p *ReadPool) checkLocked() error {
	if p.closed || p.root == nil || p.lock == nil {
		return pgerrors.ErrUnavailable
	}
	for i, f := range []*os.File{p.root, p.lock} {
		actual, err := f.Stat()
		current, pathErr := os.Lstat(f.Name())
		if err != nil || pathErr != nil || !os.SameFile(actual, current) {
			return pgerrors.ErrConflict
		}
		want := os.FileMode(0700)
		if i == 1 {
			want = 0600
		}
		if actual.Mode().Perm() != want {
			return pgerrors.ErrConflict
		}
	}
	return nil
}

func (p *ReadPool) availableLocked() (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(p.root.Fd()), &stat); err != nil || stat.Bsize <= 0 {
		return 0, pgerrors.ErrUnavailable
	}
	blocks, size := uint64(stat.Bavail), uint64(stat.Bsize)
	if blocks > uint64(math.MaxInt64)/size {
		return math.MaxInt64, nil
	}
	return int64(blocks * size), nil
}

type readLease struct {
	pool               *ReadPool
	memory, disk, read int64
	released           bool
	busy, closing      bool
}

func (p *ReadPool) reserve(ctx context.Context, c Config) (*readLease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.checkLocked(); err != nil {
		return nil, err
	}
	dir, err := filepath.EvalSymlinks(c.SpoolDir)
	if err != nil || dir != p.dir {
		return nil, pgerrors.ErrConflict
	}
	if p.readers >= p.limits.Readers || int64(c.SortMemoryBytes) > p.limits.MemoryBytes-p.memory || c.SortDiskBytes > p.limits.DiskBytes-p.disk {
		return nil, pgerrors.ErrQuotaExceeded
	}
	available, err := p.availableLocked()
	if err != nil {
		return nil, err
	}
	// Charge complete caps, conservatively also retaining free space already
	// consumed by an admitted reader. No sparse file represents a reservation.
	if available < p.limits.MinFreeBytes || p.disk > available-p.limits.MinFreeBytes || c.SortDiskBytes > available-p.limits.MinFreeBytes-p.disk {
		return nil, pgerrors.ErrQuotaExceeded
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.readers++
	p.memory += int64(c.SortMemoryBytes)
	p.disk += c.SortDiskBytes
	return &readLease{pool: p, memory: int64(c.SortMemoryBytes), disk: c.SortDiskBytes, read: c.MaxBytes}, nil
}

func (l *readLease) release() {
	p := l.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if l.released {
		return
	}
	if l.busy {
		l.closing = true
		return
	}
	l.releaseLocked()
}

func (l *readLease) releaseLocked() {
	p := l.pool
	l.released = true
	p.readers--
	p.memory -= l.memory
	p.disk -= l.disk
}

func (l *readLease) begin(ctx context.Context) error {
	l.pool.mu.Lock()
	defer l.pool.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.busy || l.released || l.closing {
		return pgerrors.ErrConflict
	}
	l.busy = true
	return nil
}

func (l *readLease) end() {
	l.pool.mu.Lock()
	defer l.pool.mu.Unlock()
	l.busy = false
	if l.closing {
		l.releaseLocked()
	}
}

func (l *readLease) checkSpace(ctx context.Context, bytes int64) error {
	p := l.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.released || l.closing {
		return pgerrors.ErrConflict
	}
	if err := p.checkLocked(); err != nil {
		return err
	}
	available, err := p.availableLocked()
	if err != nil {
		return err
	}
	if bytes < 0 || bytes > l.disk || available < p.limits.MinFreeBytes || bytes > available-p.limits.MinFreeBytes {
		return pgerrors.ErrQuotaExceeded
	}
	return ctx.Err()
}

// ReserveReadForWorker must run before borrowing SQL or opening a verification
// window. Release only after the read, sort cleanup and provider postchecks end.
// An already admitted Config cannot reserve again or share one slot with another
// worker invocation. The result carries an opaque capability, not metadata.
func (c Config) ReserveReadForWorker(ctx context.Context) (Config, func(), error) {
	c, err := c.readConfig()
	if err != nil {
		return Config{}, nil, err
	}
	if c.readLease != nil {
		return Config{}, nil, pgerrors.ErrConflict
	}
	if c.ReadPool == nil {
		return Config{}, nil, pgerrors.ErrUnavailable
	}
	l, err := c.ReadPool.reserve(ctx, c)
	if err != nil {
		return Config{}, nil, err
	}
	c.SpoolDir, c.readLease = c.ReadPool.dir, l
	return c, l.release, nil
}

func (c Config) CheckReadAdmissionForWorker(ctx context.Context) error {
	if c.readLease == nil || c.ReadPool != c.readLease.pool || c.SpoolDir != c.readLease.pool.dir ||
		int64(c.SortMemoryBytes) != c.readLease.memory || c.SortDiskBytes != c.readLease.disk || c.MaxBytes != c.readLease.read {
		return pgerrors.ErrConflict
	}
	return c.readLease.checkSpace(ctx, 0)
}
