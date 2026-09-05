package cgroupstats

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// defaultRoot is the cgroup v2 unified mount; production callers use
// New(root, now) explicitly, but NewWithDefaults exists for vmmd
// wiring which has no need to substitute a fake root in tests — its
// tests inject via the fcvm cgroupRoot var instead.
const defaultRoot = "/sys/fs/cgroup"

// linuxGOOS is the runtime.GOOS string for Linux hosts. Extracted
// to a const so golangci-lint v2.4.0 goconst stops flagging the
// two runtime.GOOS guards in this file as a duplicate literal
// (occurrences collide with reader_test.go's per-test skips and
// trip the ≥3 min-occurrences threshold).
const linuxGOOS = "linux"

// cpuStatField is the cgroup v2 cpu.stat key that represents the
// cumulative CPU time consumed by this scope, in microseconds. The
// poller does the delta math against its previous cumulative, so this
// package intentionally returns the raw counter — the rate belongs to
// the consumer. Other cpu.stat fields (user_usec, system_usec,
// nr_periods, nr_throttled, …) are NOT surfaced by this field — see
// throttledStatField below for the throttle counterpart introduced
// in issue #301 / ADR-044.
const cpuStatField = "usage_usec"

// throttledStatField is the cgroup v2 cpu.stat key that represents
// the cumulative CPU time THIS SCOPE spent throttled, in
// microseconds (issue #301, ADR-044). The poller uses this as the
// throttle counter's source — the Sample struct carries the raw
// counter, and the wire side converts to seconds for the
// vmmd_cpu_throttle_seconds_total Prometheus counter. Same raw-
// counter contract as cpuStatField: the rate belongs to the consumer.
const throttledStatField = "throttled_usec"

// Sample is the cumulative CPU and current memory charge for one
// instance, read once from cgroup v2. CPUPct and rate are computed by
// the poller across two Samples; the package does not compute a rate
// to keep the wire-stable contract simple (a future per-cgroup split
// into user/system would change the field, not the API).
type Sample struct {
	// CPUUsageUsec is the cumulative host CPU time consumed by this
	// cgroup scope since instantiation, in microseconds. Reads
	// monotonically increase across the lifetime of the scope; on
	// regression (cgroup recreated under us) the poller detects and
	// resets its baseline.
	CPUUsageUsec uint64

	// ThrottledUsec is the cumulative CPU time this scope spent
	// throttled, in microseconds (issue #301, ADR-044). Read from
	// the second line of the same cpu.stat file as CPUUsageUsec.
	// The poller computes the per-tick delta against the previous
	// sample and the wire side converts to seconds for
	// vmmd_cpu_throttle_seconds_total. The throttle ratio gauge
	// (vmmd_cpu_throttle_ratio{slice}) is derived from the same
	// delta divided by (throttle_delta + usage_delta) over the 5s
	// sampler window — that's the FaasCpuStarvation alert source.
	ThrottledUsec uint64

	// RSSBytes is the current cgroup memory charge in bytes, read
	// from memory.current. Includes the Firecracker process and VM
	// pages charged to the scope. On non-Linux or missing files the
	// Reader returns ok=false and Sample is the zero value — the
	// caller MUST NOT default to zero resident bytes (that would
	// under-report capacity and silently look like free RAM).
	RSSBytes int64
}

// Reader reads per-instance cgroup v2 counters. Construct with
// New(root, now) when the caller needs to inject a fake root for
// tests, or with NewWithDefaults() in production vmmd wiring.
//
// The Reader is safe for concurrent use; it holds no per-instance
// state. Pollers call Sample once per Tick; the package makes no
// attempt to dedupe — if two schedd pollers targeted the same node,
// they would each get their own consistent samples.
type Reader struct {
	root string
	now  func() time.Time //nolint:unused // reserved for future freshness windows
}

// New returns a Reader that reads from the given cgroup v2 root. Pass
// "/" for the production mount; tests pass t.TempDir() via this
// argument. now is reserved for a future staleness window and is not
// yet consulted; it is plumbed today so adding it later does not
// require a signature change at every call site.
//
// Pass nil for now to use time.Now.
func New(root string, now func() time.Time) *Reader {
	if now == nil {
		now = time.Now
	}
	return &Reader{root: root, now: now}
}

// NewWithDefaults returns a Reader pointed at the production
// /sys/fs/cgroup root. Use this from cmd/vmmd wiring; tests use New.
func NewWithDefaults() *Reader { return New(defaultRoot, nil) }

// Sample reads cpu.stat and memory.current for one instance's cgroup
// scope. Returns ok=false on:
//
//   - non-Linux hosts (runtime.GOOS != "linux") — cgroup v2 is Linux-only,
//   - missing scope directory (jailer has not yet joined, or already
//     torn down),
//   - malformed cpu.stat or memory.current (partial file during
//     destroy; kernel can briefly leave a stale leaf).
//
// On ok=false the Sample is the zero value — callers MUST treat that
// as "no data", not as a real zero reading. The schedd poller uses
// ok=false to mark the row Unknown in InstanceStat.
//
// The function does not log on the not-found / malformed path: those
// are normal during the wake/destroy lifecycle and the poller
// explicitly prefers partial snapshots to error spam.
//
// plan is the apps row's owning plan tier (issue #301, ADR-044); it
// resolves the 3-level cgroup hierarchy
// (faas-tenant.slice/<plan-slice>/<instance>) so the reader knows
// which sub-slice to walk. An empty plan falls back to the legacy
// 2-level path (ParentCgroupRoot/<instance>) for pre-issue-301
// callers; new callers must always pass the real plan.
func (r *Reader) Sample(instance string, plan api.Plan) (Sample, bool) {
	if runtime.GOOS != linuxGOOS {
		return Sample{}, false
	}
	parents := []string{fcvm.ParentCgroupFor(plan)}
	if legacy := fcvm.LegacyParentCgroupFor(plan); legacy != parents[0] {
		parents = append(parents, legacy)
	}
	for _, parent := range parents {
		scope := filepath.Join(r.root, parent, fcvm.PerInstanceScope(instance))
		if _, err := os.Stat(scope); err != nil {
			continue
		}
		cpu, throttled, cpuOK := readCPUStat(filepath.Join(scope, "cpu.stat"))
		if !cpuOK {
			continue
		}
		rss, rssOK := readMemoryCurrent(filepath.Join(scope, "memory.current"))
		if !rssOK {
			continue
		}
		return Sample{CPUUsageUsec: cpu, ThrottledUsec: throttled, RSSBytes: rss}, true
	}
	return Sample{}, false
}

// Instances enumerates the per-VM cgroup leaves under the per-plan
// sub-slices of faas-tenant.slice. Issue #301 / ADR-044 introduced
// the 3-level hierarchy (faas-tenant.slice/<plan-slice>/<instance>);
// this function walks each plan sub-slice and returns the
// consolidated bare instance names. Returns InstanceInfo entries
// carrying the owning plan so the caller can pass the right plan
// back into Sample.
//
// The filter — no '.', no '..' — mirrors pkg/fcvm/leakcheck's
// listTenantScopes' so systemd-installed siblings (init.scope,
// user.slice, *.mount) are excluded.
//
// The slice is sorted lexicographically by instance id (ties broken
// by plan) so callers get deterministic ordering across calls.
// This matters for the poller: its CPU-baseline map is keyed by
// instance id, and a stable order makes the per-tick dial loop
// easier to reason about in logs.
func (r *Reader) Instances() ([]InstanceInfo, error) {
	if runtime.GOOS != linuxGOOS {
		return nil, nil
	}
	base := filepath.Join(r.root, fcvm.CgroupMountRoot)
	entries, err := os.ReadDir(base)
	if err != nil {
		// Missing slice (cold-boot race, transient teardown) is
		// not an error — the poller renders an empty snapshot
		// and the wire rollup collapses. Other errors propagate
		// so the caller can log them.
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]InstanceInfo, 0, len(entries))
	seen := make(map[string]struct{})
	plans := api.Plans
	for _, plan := range plans {
		// Walk each per-plan sub-slice and enumerate its leaves.
		// A missing sub-slice (cold-boot race for that plan tier,
		// no VMs yet on Free) is not an error — the enumeration
		// just yields zero leaves for that plan. systemd drops the
		// sub-slices at boot, so once any VM has woken on a plan
		// the dir is sticky for the daemon's lifetime.
		parents := []string{fcvm.ParentCgroupFor(plan)}
		if legacy := fcvm.LegacyParentCgroupFor(plan); legacy != parents[0] {
			parents = append(parents, legacy)
		}
		for _, parent := range parents {
			planDir := filepath.Join(r.root, parent)
			planEntries, perr := os.ReadDir(planDir)
			if perr != nil {
				if errors.Is(perr, fs.ErrNotExist) {
					continue
				}
				return nil, perr
			}
			for _, e := range planEntries {
				if !e.IsDir() {
					continue
				}
				name := e.Name()
				if strings.Contains(name, ".") || strings.Contains(name, "..") {
					continue
				}
				key := string(plan) + "\x00" + name
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, InstanceInfo{Instance: name, Plan: plan})
			}
		}
	}
	// Deterministic order — sort lexicographically by (instance, plan).
	sortInstanceInfos(out)
	return out, nil
}

// InstanceInfo carries one cgroup leaf's identity. Returned by
// Instances and consumed by the poller (cmd/vmmd/cpu_poller.go) so
// each Sample call receives the correct plan to resolve the
// 3-level scope path.
type InstanceInfo struct {
	Instance string
	Plan     api.Plan
}

// readCPUStat parses one cgroup v2 cpu.stat file. The file is a
// newline-separated key-value list:
//
//	usage_usec 1234567890
//	throttled_usec 987654
//	user_usec ...
//	system_usec ...
//	nr_periods ...
//	nr_throttled ...
//	...
//
// Returns both usage_usec (the cumulative CPU time consumed by the
// scope) and throttledUsec (the cumulative CPU time spent
// throttled). Both are read from the same file in a single pass —
// splitting into two readers would race against the kernel's
// writes. Returns ok=false on a malformed file (missing usage_usec,
// non-numeric value, scanner error) so the poller marks the row
// Unknown rather than emitting a partial Sample.
//
// usage_usec is REQUIRED. The cgroup v2 kernel always emits it on
// every scope that has any CPU activity; absence means the file is
// garbled (mid-write torn page, a manually-edited test fixture, or
// a non-cgroup file opened by mistake). Returning ok=false on
// missing usage_usec is the load-bearing guard against emitting a
// zero-CPU reading that would silently under-report capacity.
//
// Throttled_usec may be missing on kernels older than 5.14
// (cpu.stat throttling accounting landed in 5.14). When absent
// the parser returns 0 + ok=true; the FaasCpuStarvation alert
// treats a zero throttle ratio as healthy so a missing
// throttled_usec reads as "no throttling" rather than alerting.
//
// Path is vetted by the caller: it lives under
// /sys/fs/cgroup/<slice>/<instance>/, where <instance> is the
// jailer's per-VM directory name. The instance id is not
// customer-supplied (it is the cgroup directory the jailer created
// at VM boot and tore down at VM destroy), so bare os.Open is
// safe — the symlink/non-regular guard that openCustomerFile
// enforces is irrelevant on the host's cgroup v2 mount. The
// errcheck ignore below pairs with the doc: we cannot meaningfully
// act on a Close error from a /sys read.
func readCPUStat(path string) (usageUsec, throttledUsec uint64, ok bool) {
	f, err := os.Open(path) //nolint:forbidigo // vetted cgroup path, see comment above
	if err != nil {
		return 0, 0, false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	// cpu.stat lines are short; the default 64 KiB buffer is plenty.
	// usageSeen tracks whether the parser actually consumed a
	// usage_usec line — distinguishing "VM just started, zero CPU
	// time yet" (usageSeen=true, value=0, ok=true) from "the file
	// is missing usage_usec entirely" (usageSeen=false, ok=false).
	var usageSeen bool
	for sc.Scan() {
		line := sc.Text()
		key, val, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		switch key {
		case cpuStatField:
			n, perr := strconv.ParseUint(strings.TrimSpace(val), 10, 64)
			if perr != nil {
				return 0, 0, false
			}
			usageUsec = n
			usageSeen = true
		case throttledStatField:
			n, perr := strconv.ParseUint(strings.TrimSpace(val), 10, 64)
			if perr != nil {
				return 0, 0, false
			}
			throttledUsec = n
		}
	}
	// Scanner error (file truncated / kernel panic halfway through)
	// is fail-closed regardless of the parsed values.
	if sc.Err() != nil {
		return 0, 0, false
	}
	// usage_usec is required — a missing key means the file is
	// garbled. The two failure cases the unit tests pin:
	//   - TestSampleReturnsFalseOnMalformedCpuStat: a non-cgroup
	//     file ("this is not a cgroup file") has no usage_usec line.
	//   - TestSampleReturnsFalseOnMissingUsageUsecField: a cgroup
	//     file with only user_usec/system_usec (no usage_usec).
	// Both must return ok=false so the poller doesn't emit a
	// zero-CPU row that would under-report capacity.
	if !usageSeen {
		return 0, 0, false
	}
	return usageUsec, throttledUsec, true
}

// readMemoryCurrent reads cgroup v2 memory.current — a single integer
// in bytes, no newline required (but tolerated).
func readMemoryCurrent(path string) (int64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// sortInstanceInfos is a tiny inlined sort wrapper to keep this
// file's imports list narrow. Hot path: only the instance names found
// in this tick — typically tens of entries, not millions. Sorts by
// (instance, plan) lex so the iteration order is deterministic.
func sortInstanceInfos(s []InstanceInfo) {
	// Insertion sort: small N, no allocation, predictable.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0; j-- {
			if s[j-1].Instance < s[j].Instance {
				break
			}
			if s[j-1].Instance == s[j].Instance && s[j-1].Plan <= s[j].Plan {
				break
			}
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
