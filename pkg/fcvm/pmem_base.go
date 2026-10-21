package fcvm

// Per-account pmem base (ADR-908). With a pmem base directory configured,
// an app VM's read-only base is attached as a virtio-pmem device and the
// guest mounts it with DAX, so it maps the host's page cache of the base
// instead of copying the runtime into its own RAM. Each account gets its own
// reflinked copy of the base: a copy has its own page cache, so no two
// accounts ever map the same physical pages.
//
// Inside the jail the copy is staged as base-pmem.ext4, a name only pmem
// snapshots refer to. A pmem snapshot restored by a VMM without a pmem base
// cannot find it, fails to load and takes the cold-boot path (ADR-005); it
// can never be pointed at the node-wide base.
//
// A copy's name covers the base's path, size and file identity (device,
// inode and, where the filesystem records it, birth time), so a replaced base
// gets a fresh copy. It deliberately leaves out the modification time: the
// node cache bumps a cached base's mtime on every read to keep its LRU order
// (storage.LocalCacheBackend.touchCacheFile), and an mtime-derived name gave
// every wake a new clone whose page cache was empty. On production-us
// (rc.285) that left ~200 stale clones a day per node, and every restored
// guest read its runtime base from disk through DAX: ~110 waits on I/O,
// ~100 ms of vCPU time, inside each restore's resume. Every writer of a base
// replaces it by renaming a new file into place (storage.LocalStorageBackend
// Put, LocalCacheBackend fills and Refresh), so new content always arrives
// with a new identity. Copies not used for pmemCopyMaxIdle are swept, at most
// once per pmemSweepEvery per directory.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"
)

const (
	pmemBaseImageName = "base-pmem.ext4"
	// PmemBaseLabel must match rootfs.BasePmemLabel: the base build stamps
	// it when it installs a guest-init that can boot from /dev/pmem0.
	PmemBaseLabel = "faas-base-pmem"
	// PmemBaseDirAuto keeps each account copy on the mount holding its
	// base, in pmemAutoDirName at the mount's root. FICLONE refuses to
	// clone across mounts, even across bind mounts of one filesystem.
	PmemBaseDirAuto = "auto"
	pmemAutoDirName = ".faas-pmem"
	pmemCopyMaxIdle = 7 * 24 * time.Hour
	pmemSweepEvery  = time.Hour
	// Firecracker maps a pmem backing file in 2 MiB units.
	pmemBaseAlign      = 2 << 20
	pmemMinFirecracker = "v1.16.0"
	blkRootArgs        = "root=/dev/vda ro "
	pmemRootArgs       = "root=/dev/pmem0 rootflags=dax ro "
)

// Pmem is a Firecracker virtio-pmem device (`pmem` in the config file).
type Pmem struct {
	ID         string `json:"id"`
	PathOnHost string `json:"path_on_host"`
	RootDevice bool   `json:"root_device"`
	ReadOnly   bool   `json:"read_only"`
}

// PmemBaseSupported reports whether a detected Firecracker version
// ("1.17.0") has virtio-pmem.
func PmemBaseSupported(fcVersion string) bool {
	v := "v" + fcVersion
	return semver.IsValid(v) && semver.Compare(v, pmemMinFirecracker) >= 0
}

type pmemBaseStore struct {
	// dir is PmemBaseDirAuto or a fixed directory.
	dir string
	// clone makes dst a copy-on-write clone of src; tests replace it.
	clone func(dst, src string) error
	// mountinfo is /proc/self/mountinfo; tests replace it.
	mountinfo string
	now       func() time.Time
	mu        sync.Mutex
	swept     map[string]time.Time // copy directory -> last sweep
}

// WithPmemBase attaches app VMs' bases as per-account pmem copies kept in
// dir, which must be on the same mount of a reflink-capable filesystem as
// the cached bases, or PmemBaseDirAuto. An empty dir or "off" keeps every
// base on virtio-blk. The caller must check PmemBaseSupported first.
func (v *JailerVMM) WithPmemBase(dir string) *JailerVMM {
	if dir == "" || dir == "off" {
		v.pmemBase = nil
		return v
	}
	v.pmemBase = &pmemBaseStore{dir: dir, clone: reflinkFile, mountinfo: "/proc/self/mountinfo",
		now: time.Now, swept: map[string]time.Time{}}
	return v
}

// copyDir is where base's account copies live.
func (s *pmemBaseStore) copyDir(base string) (string, error) {
	if s.dir != PmemBaseDirAuto {
		return s.dir, nil
	}
	path, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", err
	}
	mount, err := mountPointOf(s.mountinfo, path)
	if err != nil {
		return "", err
	}
	return filepath.Join(mount, pmemAutoDirName), nil
}

// mountPointOf returns the deepest mount point in mountinfo that holds path.
func mountPointOf(mountinfo, path string) (string, error) {
	raw, err := os.ReadFile(mountinfo) //nolint:forbidigo // kernel mount table.
	if err != nil {
		return "", err
	}
	best := ""
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		mp := unescapeMountinfo(f[4])
		if (path == mp || mp == "/" || strings.HasPrefix(path, mp+"/")) && len(mp) > len(best) {
			best = mp
		}
	}
	if best == "" {
		return "", fmt.Errorf("no mount holds %s", path)
	}
	return best, nil
}

// unescapeMountinfo decodes mountinfo's octal escapes (\040 for a space).
func unescapeMountinfo(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// daxCapable checks what a DAX root mount needs: an ext4 filesystem with
// 4 KiB blocks (the kernel refuses `dax` on any other block size, which
// would leave the guest without a root), a size Firecracker can map, and
// PmemBaseLabel, so its guest-init can boot from /dev/pmem0.
func daxCapable(base string, size int64) error {
	if size%pmemBaseAlign != 0 {
		return fmt.Errorf("%s is not a multiple of 2 MiB", base)
	}
	f, err := os.Open(base) //nolint:forbidigo // cache-owned base image; superblock read only.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sb := make([]byte, 136)
	if _, err := f.ReadAt(sb, 1024); err != nil {
		return fmt.Errorf("read ext4 superblock: %w", err)
	}
	if binary.LittleEndian.Uint16(sb[56:58]) != 0xEF53 {
		return fmt.Errorf("%s is not ext4", base)
	}
	if blockSize := 1024 << binary.LittleEndian.Uint32(sb[24:28]); blockSize != 4096 {
		return fmt.Errorf("%s has %d-byte blocks; DAX needs 4096", base, blockSize)
	}
	if label := strings.TrimRight(string(sb[120:136]), "\x00"); label != PmemBaseLabel {
		return fmt.Errorf("%s is labelled %q, not %q: built without a pmem-aware guest-init", base, label, PmemBaseLabel)
	}
	return nil
}

// accountCopy returns the account's clone of base, creating it on first use
// and whenever the base changes.
func (s *pmemBaseStore) accountCopy(account, base string) (string, error) {
	st, err := os.Stat(base)
	if err != nil {
		return "", fmt.Errorf("pmem base: %w", err)
	}
	if err := daxCapable(base, st.Size()); err != nil {
		return "", fmt.Errorf("pmem base: %w", err)
	}
	root, err := s.copyDir(base)
	if err != nil {
		return "", fmt.Errorf("pmem base: %w", err)
	}
	id, err := pmemBaseFileIdentity(base, st)
	if err != nil {
		return "", fmt.Errorf("pmem base: %w", err)
	}
	acct := sha256.Sum256([]byte(account))
	src := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s", base, st.Size(), id)))
	dir := filepath.Join(root, hex.EncodeToString(acct[:8]))
	dst := filepath.Join(dir, hex.EncodeToString(src[:8])+".ext4")
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	defer s.sweepLocked(root, now)
	if cur, err := os.Stat(dst); err == nil && cur.Size() == st.Size() {
		// Record the use in atime; mtime stays the clone's.
		_ = os.Chtimes(dst, now, cur.ModTime())
		return dst, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("pmem base: %w", err)
	}
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	if err := s.clone(tmp, base); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("pmem base: clone %s: %w", base, err)
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("pmem base: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("pmem base: %w", err)
	}
	return dst, nil
}

// pmemEligible reports whether this lease's VM may boot from a pmem base:
// an app VM of a known account, outside native recovery.
func (v *JailerVMM) pmemEligible(l Lease) bool {
	return v.pmemBase != nil && l.AccountID != "" && !l.IsBuilder && !l.Networkless && v.nativeRecovery == nil
}

// withPmemBase moves an eligible VM's read-only base from drive0 to a pmem
// device backed by its account's copy. Anything unexpected keeps virtio-blk.
func (v *JailerVMM) withPmemBase(l Lease, cfg VMConfig) VMConfig {
	if !v.pmemEligible(l) || cfg.Pmem != nil || len(cfg.Drives) == 0 ||
		cfg.Drives[0].DriveID != DriveBase || !cfg.Drives[0].IsReadOnly ||
		!strings.Contains(cfg.BootSource.BootArgs, blkRootArgs) {
		return cfg
	}
	copyPath, err := v.pmemBase.accountCopy(l.AccountID, cfg.Drives[0].PathOnHost)
	if err != nil {
		slog.Default().Warn("vmm: pmem base unavailable, booting the base from virtio-blk", "instance", l.Instance, "err", err)
		return cfg
	}
	cfg.Drives = append([]Drive(nil), cfg.Drives[1:]...)
	cfg.Pmem = []Pmem{{ID: DriveBase, PathOnHost: copyPath, RootDevice: true, ReadOnly: true}}
	cfg.BootSource.BootArgs = strings.Replace(cfg.BootSource.BootArgs, blkRootArgs, pmemRootArgs, 1)
	return cfg
}

// sweepLocked removes copies in root that no VM used for pmemCopyMaxIdle.
// A running VM keeps its mapping of a removed copy; its next wake clones
// the base again.
func (s *pmemBaseStore) sweepLocked(root string, now time.Time) {
	if last, ok := s.swept[root]; ok && now.Sub(last) < pmemSweepEvery {
		return
	}
	s.swept[root] = now
	accounts, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, a := range accounts {
		if !a.IsDir() {
			continue
		}
		dir := filepath.Join(root, a.Name())
		copies, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, c := range copies {
			info, err := c.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if now.Sub(lastUse(info)) > pmemCopyMaxIdle {
				_ = os.Remove(filepath.Join(dir, c.Name()))
			}
		}
		_ = os.Remove(dir) // only succeeds once empty
	}
}
