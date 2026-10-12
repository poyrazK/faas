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

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/mod/semver"
)

const (
	pmemBaseImageName = "base-pmem.ext4"
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
	dir string
	// clone makes dst a copy-on-write clone of src; tests replace it.
	clone func(dst, src string) error
	mu    sync.Mutex
}

// WithPmemBase attaches app VMs' bases as per-account pmem copies kept in
// dir, which must be on the same reflink-capable filesystem as the cached
// bases. An empty dir keeps every base on virtio-blk. The caller must check
// PmemBaseSupported first.
func (v *JailerVMM) WithPmemBase(dir string) *JailerVMM {
	if dir == "" {
		v.pmemBase = nil
		return v
	}
	v.pmemBase = &pmemBaseStore{dir: dir, clone: reflinkFile}
	return v
}

// daxCapable checks what a DAX root mount needs: an ext4 filesystem with
// 4 KiB blocks (the kernel refuses `dax` on any other block size, which
// would leave the guest without a root) and a size Firecracker can map.
func daxCapable(base string, size int64) error {
	if size%pmemBaseAlign != 0 {
		return fmt.Errorf("%s is not a multiple of 2 MiB", base)
	}
	f, err := os.Open(base) //nolint:forbidigo // cache-owned base image; superblock read only.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sb := make([]byte, 64)
	if _, err := f.ReadAt(sb, 1024); err != nil {
		return fmt.Errorf("read ext4 superblock: %w", err)
	}
	if binary.LittleEndian.Uint16(sb[56:58]) != 0xEF53 {
		return fmt.Errorf("%s is not ext4", base)
	}
	if blockSize := 1024 << binary.LittleEndian.Uint32(sb[24:28]); blockSize != 4096 {
		return fmt.Errorf("%s has %d-byte blocks; DAX needs 4096", base, blockSize)
	}
	return nil
}

// accountCopy returns the account's clone of base, creating or refreshing it
// when the base is newer.
func (s *pmemBaseStore) accountCopy(account, base string) (string, error) {
	st, err := os.Stat(base)
	if err != nil {
		return "", fmt.Errorf("pmem base: %w", err)
	}
	if err := daxCapable(base, st.Size()); err != nil {
		return "", fmt.Errorf("pmem base: %w", err)
	}
	acct, src := sha256.Sum256([]byte(account)), sha256.Sum256([]byte(base))
	dir := filepath.Join(s.dir, hex.EncodeToString(acct[:8]))
	dst := filepath.Join(dir, hex.EncodeToString(src[:8])+".ext4")
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, err := os.Stat(dst); err == nil && cur.Size() == st.Size() && !cur.ModTime().Before(st.ModTime()) {
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
