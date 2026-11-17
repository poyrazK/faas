package fcvm

// Cold-page compression through the multi-gen LRU (ADR-20261107122928 §1,
// §2; Documentation/admin-guide/mm/multigen_lru.rst). The kernel keeps each
// memory cgroup's pages in up to four generations. An aging pass ("+") opens
// a new youngest generation and moves every page accessed since the last
// pass into it, so a page in an older generation has not been touched since
// the pass that opened the generation after it. Eviction ("-") reclaims the
// generations up to a given one, oldest first; with swap open to the VM's
// scope and memory-only zswap, its anonymous pages are compressed.
//
// vmmd ages a VM's cgroup at a quiet pause, after the guest ran, at most
// once per api.IdleLadderColdAge1Seconds, so generations stand for windows
// of guest activity at least that long. Compressing "pages untouched for a
// window" is then evicting every generation but the youngest few.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// lruGenPath and lruGenEnabledPath are replaced in tests.
var (
	lruGenPath        = "/sys/kernel/debug/lru_gen"
	lruGenEnabledPath = "/sys/kernel/mm/lru_gen/enabled"
)

// lruGenMinGens is how many of the youngest generations eviction can never
// take: the kernel refuses min_gen above max_gen-2.
const lruGenMinGens = 2

// lruGenMaxGens is the kernel's MAX_NR_GENS: aging past it merges the oldest
// two generations.
const lruGenMaxGens = 4

const (
	// lruGenEvictChunkPages bounds one eviction write (16 MB of 4 KB pages),
	// so a resume never waits on more than one chunk's worth of work.
	lruGenEvictChunkPages = 4096
	// lruGenEvictBudget bounds one compression of a VM.
	lruGenEvictBudget = 2 * time.Second
	// lruGenWriteTimeout bounds one aging or eviction write.
	lruGenWriteTimeout = 500 * time.Millisecond
	pageBytes          = 4096
)

// lruGen is one generation of one NUMA node's LRU for a cgroup.
type lruGen struct {
	seq        uint64
	ageMS      int64
	anon, file int64 // pages
}

// lruGenNode is one NUMA node's generations, oldest first.
type lruGenNode struct {
	node int
	gens []lruGen
}

// maxSeq is the youngest generation's sequence number.
func (n lruGenNode) maxSeq() uint64 {
	if len(n.gens) == 0 {
		return 0
	}
	return n.gens[len(n.gens)-1].seq
}

// evictable returns the newest sequence eviction may take when keep
// generations stay, and the pages in it and older. ok is false when keep
// leaves nothing to evict.
func (n lruGenNode) evictable(keep int) (minSeq uint64, pages int64, ok bool) {
	keep = max(keep, lruGenMinGens)
	if len(n.gens) <= keep {
		return 0, 0, false
	}
	last := n.gens[len(n.gens)-1-keep]
	for _, g := range n.gens[:len(n.gens)-keep] {
		pages += g.anon + g.file
	}
	return last.seq, pages, pages > 0
}

// lruGenMemcg is one memory cgroup in the lru_gen listing.
type lruGenMemcg struct {
	id    int
	path  string
	nodes []lruGenNode
}

// parseLRUGen reads the lru_gen listing and returns the cgroup at path
// (relative to the cgroup root, with a leading slash).
func parseLRUGen(r io.Reader, path string) (lruGenMemcg, bool, error) {
	var cur *lruGenMemcg
	var found lruGenMemcg
	ok := false
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "memcg":
			if ok {
				return found, true, nil
			}
			cur = nil
			if len(fields) < 2 {
				continue
			}
			id, err := strconv.Atoi(fields[1])
			if err != nil {
				return lruGenMemcg{}, false, fmt.Errorf("lru_gen: memcg id %q: %w", fields[1], err)
			}
			p := ""
			if len(fields) > 2 {
				p = fields[2]
			}
			if p == path {
				found, ok = lruGenMemcg{id: id, path: p}, true
				cur = &found
			}
		case "node":
			if cur == nil || len(fields) < 2 {
				continue
			}
			node, err := strconv.Atoi(fields[1])
			if err != nil {
				return lruGenMemcg{}, false, fmt.Errorf("lru_gen: node %q: %w", fields[1], err)
			}
			cur.nodes = append(cur.nodes, lruGenNode{node: node})
		default:
			if cur == nil || len(cur.nodes) == 0 {
				continue
			}
			g, gok := parseLRUGenLine(fields)
			if !gok {
				continue
			}
			n := &cur.nodes[len(cur.nodes)-1]
			n.gens = append(n.gens, g)
		}
	}
	if err := sc.Err(); err != nil {
		return lruGenMemcg{}, false, fmt.Errorf("lru_gen: read: %w", err)
	}
	return found, ok, nil
}

// parseLRUGenLine parses "seq age_ms anon_pages file_pages".
func parseLRUGenLine(fields []string) (lruGen, bool) {
	if len(fields) < 4 {
		return lruGen{}, false
	}
	var nums [4]int64
	for i := range nums {
		n, err := strconv.ParseInt(fields[i], 10, 64)
		if err != nil {
			return lruGen{}, false
		}
		nums[i] = n
	}
	return lruGen{seq: uint64(nums[0]), ageMS: nums[1], anon: nums[2], file: nums[3]}, true
}

// lruGenAvailable reports whether the kernel's multi-gen LRU is on and its
// debugfs interface is reachable.
func lruGenAvailable() bool {
	raw, err := os.ReadFile(lruGenEnabledPath)
	if err != nil {
		return false
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(string(raw)), "0x"), 16, 32)
	if err != nil || v == 0 {
		return false
	}
	_, err = os.Stat(lruGenPath)
	return err == nil
}

// vmLRUGen reads the generations of a VM's cgroup.
func vmLRUGen(l Lease) (lruGenMemcg, error) {
	f, err := os.Open(lruGenPath)
	if err != nil {
		return lruGenMemcg{}, fmt.Errorf("vmm: open lru_gen: %w", err)
	}
	defer func() { _ = f.Close() }()
	path := "/" + filepath.ToSlash(filepath.Join(vmCgroupParent(l), PerInstanceScope(l.Instance)))
	memcg, ok, err := parseLRUGen(f, path)
	if err != nil {
		return lruGenMemcg{}, err
	}
	if !ok {
		return lruGenMemcg{}, fmt.Errorf("vmm: lru_gen: no memcg %s", path)
	}
	return memcg, nil
}

// ColdPagesReady reports whether this host can compress a quiet VM's cold
// pages: memory-only compressed swap and the multi-gen LRU.
func (v *JailerVMM) ColdPagesReady(l Lease) bool {
	return v.FrozenSwapReady(l) && lruGenAvailable()
}

// AgeVMMemory opens a new youngest generation on every NUMA node of the VM's
// cgroup, moving the pages the guest touched since the last pass into it.
func (v *JailerVMM) AgeVMMemory(l Lease) error {
	memcg, err := vmLRUGen(l)
	if err != nil {
		return err
	}
	for _, n := range memcg.nodes {
		// can_swap=1 ages anonymous pages although running VMs' swap is
		// closed; force_scan=1 walks every page table.
		cmd := fmt.Sprintf("+ %d %d %d 1 1", memcg.id, n.node, n.maxSeq())
		if err := writeWithDeadline(lruGenPath, cmd, lruGenWriteTimeout); err != nil {
			return fmt.Errorf("vmm: age %s: %w", l.Instance, err)
		}
	}
	return nil
}

// EvictColdVMMemory compresses every generation of the VM's cgroup but the
// youngest keep, in chunks, until they are empty, lruGenEvictBudget runs
// out, or stop reports true. The caller has opened the scope's swap.
func (v *JailerVMM) EvictColdVMMemory(l Lease, keep int, stop func() bool) (VMMemoryReclaim, error) {
	scope := vmCgroupScope(l)
	var r VMMemoryReclaim
	var err error
	if r.BeforeBytes, err = readCgroupInt(scope, "memory.current"); err != nil {
		return r, err
	}
	memcg, err := vmLRUGen(l)
	if err != nil {
		return r, err
	}
	deadline := time.Now().Add(lruGenEvictBudget)
	for _, n := range memcg.nodes {
		minSeq, pages, ok := n.evictable(keep)
		for ok && pages > 0 && !stop() {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				break
			}
			// swappiness 200 takes anonymous pages first: guest memory.
			cmd := fmt.Sprintf("- %d %d %d 200 %d", memcg.id, n.node, minSeq, lruGenEvictChunkPages)
			err := writeWithDeadline(lruGenPath, cmd, min(remaining, lruGenWriteTimeout))
			if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINVAL) {
				// Out of time, nothing left, or the generations moved on.
				break
			}
			if err != nil {
				return r, fmt.Errorf("vmm: evict %s: %w", l.Instance, err)
			}
			pages -= lruGenEvictChunkPages
		}
	}
	if r.AfterBytes, err = readCgroupInt(scope, "memory.current"); err != nil {
		return r, err
	}
	r.SwappedBytes, _ = readCgroupInt(scope, "memory.swap.current")
	return r, nil
}

// VMRefaultPages counts the pages the VM's cgroup has brought back from
// compressed swap, swap or a dropped page cache: the faults a guest pays
// after compression.
func (v *JailerVMM) VMRefaultPages(l Lease) (int64, error) {
	stat, err := readCgroupStat(vmCgroupScope(l))
	if err != nil {
		return 0, err
	}
	return stat["zswpin"] + stat["pswpin"] + stat["workingset_refault_file"], nil
}
