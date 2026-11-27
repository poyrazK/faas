package builderslots

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// Cgroup paths, relative to the cgroup2 mount.
const (
	// TenantSlice holds every tenant VM; its memory.max is the node's
	// tenant fence (node sizing, ADR-203).
	TenantSlice = "faas.slice/faas-tenant.slice"
	// BorrowedSlice holds borrowed builder VMs. It sits beside
	// faas-cp.slice and faas-tenant.slice, not under faas-cp.slice, so the
	// control-plane fence does not move. The unit is faas-borrowed.slice: a
	// dash in a slice name nests it, so the name has only faas- in front.
	BorrowedSlice = "faas.slice/faas-borrowed.slice"

	// DefaultCgroupRoot and DefaultMemInfoPath are the production locations.
	DefaultCgroupRoot  = "/sys/fs/cgroup"
	DefaultMemInfoPath = "/proc/meminfo"
)

// Memory is one reading of a node's memory, in MiB.
type Memory struct {
	// TenantMB is faas-tenant.slice memory.current.
	TenantMB int64
	// TenantMaxMB is faas-tenant.slice memory.max, or MemTotal when the
	// slice is unbounded.
	TenantMaxMB int64
	// BorrowedMB is faas-borrowed.slice memory.current; zero when the
	// slice does not exist.
	BorrowedMB int64
	// AvailableMB is /proc/meminfo MemAvailable.
	AvailableMB int64
}

// withinPct reports whether tenant plus borrowed memory, plus extraMB, is at
// most pct percent of the tenant fence. An unknown fence is never within.
func (m Memory) withinPct(extraMB int64, pct int) bool {
	if m.TenantMaxMB <= 0 {
		return false
	}
	return (m.TenantMB+m.BorrowedMB+extraMB)*100 <= int64(pct)*m.TenantMaxMB
}

// CanAdmitBorrowed reports whether one more borrowed build fits: tenants,
// borrowed builders and the new build stay within BuilderBorrowAdmitPct of
// the tenant fence, and the host keeps BuilderBorrowHostFloorMB available
// after it.
func CanAdmitBorrowed(m Memory) bool {
	return m.withinPct(api.BorrowedBuildNeedMB, api.BuilderBorrowAdmitPct) &&
		m.AvailableMB >= api.BorrowedBuildNeedMB+api.BuilderBorrowHostFloorMB
}

// ShouldGiveBack reports whether builderd must cancel the newest borrowed
// build: tenants and borrowed builders above BuilderBorrowGiveBackPct of the
// tenant fence, or the host below BuilderBorrowHostFloorMB available.
func ShouldGiveBack(m Memory) bool {
	return !m.withinPct(0, api.BuilderBorrowGiveBackPct) || m.AvailableMB < api.BuilderBorrowHostFloorMB
}

// MustForce reports whether vmmd must destroy the newest borrowed builder
// itself once the condition has held for BuilderBorrowForceSustain.
func MustForce(m Memory) bool {
	return !m.withinPct(0, api.BuilderBorrowForcePct) || m.AvailableMB < api.BuilderBorrowHostCriticalMB
}

// Reader reads Memory from cgroupfs and /proc/meminfo.
type Reader struct {
	CgroupRoot  string
	MemInfoPath string
}

// NewReader reads the production locations.
func NewReader() Reader {
	return Reader{CgroupRoot: DefaultCgroupRoot, MemInfoPath: DefaultMemInfoPath}
}

// BorrowedSliceExists reports whether faas-borrowed.slice is in the
// cgroup tree. vmmd advertises borrowed builders only when it is.
func (r Reader) BorrowedSliceExists() bool {
	info, err := os.Stat(filepath.Join(r.CgroupRoot, BorrowedSlice))
	return err == nil && info.IsDir()
}

// Read takes one reading. A missing borrowed slice reads as zero; any other
// unreadable value is an error, and callers treat an error as pressure.
func (r Reader) Read() (Memory, error) {
	info, err := readMemInfo(r.MemInfoPath)
	if err != nil {
		return Memory{}, err
	}
	tenant := filepath.Join(r.CgroupRoot, TenantSlice)
	current, _, err := readCgroupMB(filepath.Join(tenant, "memory.current"))
	if err != nil {
		return Memory{}, err
	}
	limit, unbounded, err := readCgroupMB(filepath.Join(tenant, "memory.max"))
	if err != nil {
		return Memory{}, err
	}
	if unbounded {
		limit = info.totalMB
	}
	borrowed, _, err := readCgroupMB(filepath.Join(r.CgroupRoot, BorrowedSlice, "memory.current"))
	if errors.Is(err, fs.ErrNotExist) {
		borrowed, err = 0, nil
	}
	if err != nil {
		return Memory{}, err
	}
	return Memory{TenantMB: current, TenantMaxMB: limit, BorrowedMB: borrowed, AvailableMB: info.availableMB}, nil
}

// readCgroupMB reads a cgroup v2 byte count, reporting "max" as unbounded.
func readCgroupMB(path string) (int64, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false, fmt.Errorf("builderslots: read %s: %w", path, err)
	}
	value := strings.TrimSpace(string(raw))
	if value == "max" {
		return 0, true, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0, false, fmt.Errorf("builderslots: %s holds %q, not a byte count", path, value)
	}
	return n >> 20, false, nil
}

type memInfo struct {
	totalMB     int64
	availableMB int64
}

func readMemInfo(path string) (memInfo, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return memInfo{}, fmt.Errorf("builderslots: read %s: %w", path, err)
	}
	var info memInfo
	var haveTotal, haveAvailable bool
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			info.totalMB, haveTotal = kb>>10, true
		case "MemAvailable:":
			info.availableMB, haveAvailable = kb>>10, true
		}
	}
	if !haveTotal || !haveAvailable {
		return memInfo{}, fmt.Errorf("builderslots: %s has no MemTotal or MemAvailable", path)
	}
	return info, nil
}
