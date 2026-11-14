// adr: 20261107122928 — with compression on, vmmd ages a quiet-paused VM's
// memory after the guest ran, compresses all but the youngest generations at
// L3 and L4, reports those levels, charges each compressed wake its resume
// and refaults, and keeps more generations for an app whose wakes exceed
// their budget.
package fcvm

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// coldVMM is a pausing VMM with a scripted cold-page interface.
type coldVMM struct {
	*pausingVMM
	cmu         sync.Mutex
	unready     bool
	ages        int
	keeps       []int
	refaults    int64
	swap        []bool
	duringEvict func()
}

func (v *coldVMM) ColdPagesReady(Lease) bool {
	v.cmu.Lock()
	defer v.cmu.Unlock()
	return !v.unready
}

func (v *coldVMM) AgeVMMemory(Lease) error {
	v.cmu.Lock()
	defer v.cmu.Unlock()
	v.ages++
	return nil
}

func (v *coldVMM) EvictColdVMMemory(_ Lease, keep int, stop func() bool) (VMMemoryReclaim, error) {
	if v.duringEvict != nil {
		v.duringEvict()
	}
	v.cmu.Lock()
	v.keeps = append(v.keeps, keep)
	v.cmu.Unlock()
	if stop() {
		return VMMemoryReclaim{BeforeBytes: 600 << 20, AfterBytes: 590 << 20}, nil
	}
	return VMMemoryReclaim{BeforeBytes: 600 << 20, AfterBytes: 200 << 20}, nil
}

func (v *coldVMM) VMRefaultPages(Lease) (int64, error) {
	v.cmu.Lock()
	defer v.cmu.Unlock()
	return v.refaults, nil
}

func (v *coldVMM) SetFrozenSwap(_ Lease, open bool) error {
	v.cmu.Lock()
	defer v.cmu.Unlock()
	v.swap = append(v.swap, open)
	return nil
}

func (v *coldVMM) state() (ages int, keeps []int, swap []bool) {
	v.cmu.Lock()
	defer v.cmu.Unlock()
	return v.ages, append([]int(nil), v.keeps...), append([]bool(nil), v.swap...)
}

type coldHarness struct {
	m       *Manager
	v       *coldVMM
	act     *fakeQuietActivity
	metrics *IdleCompressMetrics
}

// newColdHarness wakes "q", a function the idle ladder holds, whose last
// request ended at t0, and quiet-pauses it.
func newColdHarness(t *testing.T, t0 time.Time) *coldHarness {
	t.Helper()
	act := newFakeQuietActivity()
	v := &coldVMM{pausingVMM: &pausingVMM{fakeVMM: &fakeVMM{}}}
	metrics := NewIdleCompressMetrics(prometheus.NewRegistry())
	m := newTestManager(&fakeRunner{}, v).
		WithQuietPause(QuietPauseConfig{Activity: act}).
		WithIdleLadder(IdleLadderConfig{Activity: act, On: true, Compress: true, CompressMetrics: metrics})
	req := quietWake("q")
	req.IdleLadderEligible = true
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(t.Context(), "q") })
	seenQuiet(m, "q", t0.Add(-time.Hour))
	h := &coldHarness{m: m, v: v, act: act, metrics: metrics}
	h.pauseAfterRequest(t, t0)
	return h
}

// pauseAfterRequest records a request that ended at at and quiet-pauses.
func (h *coldHarness) pauseAfterRequest(t *testing.T, at time.Time) {
	t.Helper()
	before := h.v.pauses()
	h.act.set("q", 0, at)
	h.m.quietPausePass(t.Context(), at.Add(2*time.Second))
	if h.v.pauses() != before+1 {
		t.Fatalf("pauses = %d, want %d", h.v.pauses(), before+1)
	}
}

// extra is the compression ceiling of q's app.
func (h *coldHarness) extra() int {
	h.m.mu.Lock()
	app := h.m.live["q"].AppID
	h.m.mu.Unlock()
	return h.m.coldExtra(app)
}

func (h *coldHarness) outcome(level IdleLevel, outcome string) float64 {
	return testutil.ToFloat64(h.metrics.compressions.WithLabelValues(level.String(), outcome))
}

func TestColdCompressionFollowsTheLadder(t *testing.T) {
	t0 := time.Now()
	h := newColdHarness(t, t0)
	h.m.idleCompressPass(t0.Add(3 * time.Second))
	if ages, keeps, _ := h.v.state(); ages != 1 || len(keeps) != 0 {
		t.Fatalf("after the pause: ages %d, evictions %v; want one aging, no eviction", ages, keeps)
	}
	h.m.idleCompressPass(t0.Add(61 * time.Second))
	if _, keeps, swap := h.v.state(); len(keeps) != 1 || keeps[0] != 3 || len(swap) != 1 || !swap[0] {
		t.Fatalf("at L3: evictions %v swap %v; want keep 3 with swap opened", keeps, swap)
	}
	if got := h.m.IdleLadderLevel("q", t0.Add(61*time.Second)); got != IdleCold1 {
		t.Fatalf("level at L3 = %v, want cold_1", got)
	}
	if got := testutil.ToFloat64(h.metrics.compressed); got != 400<<20 {
		t.Fatalf("compressed bytes = %v, want 400 MB", got)
	}
	h.m.idleCompressPass(t0.Add(62 * time.Second))
	if _, keeps, _ := h.v.state(); len(keeps) != 1 {
		t.Fatalf("evictions after a second L3 pass = %v, want still one", keeps)
	}
	h.m.idleCompressPass(t0.Add(181 * time.Second))
	if ages, keeps, _ := h.v.state(); ages != 1 || len(keeps) != 2 || keeps[1] != 2 {
		t.Fatalf("at L4: ages %d evictions %v; want no new aging, keep 2", ages, keeps)
	}
	if got := h.m.IdleLadderLevel("q", t0.Add(181*time.Second)); got != IdleCold2 {
		t.Fatalf("level at L4 = %v, want cold_2", got)
	}
	if err := h.m.QuietResume(t.Context(), "q"); err != nil {
		t.Fatal(err)
	}
	if _, _, swap := h.v.state(); swap[len(swap)-1] {
		t.Fatalf("swap after the resume = %v, want closed", swap)
	}
	if got := h.m.IdleLadderLevel("q", t0.Add(182*time.Second)); got != IdleRunning {
		t.Fatalf("level after the resume = %v, want running", got)
	}
}

// A wake whose refaults exceed the budget keeps one more generation at each
// level; enough cheap wakes give it back.
func TestColdWakeOverBudgetRaisesTheCeiling(t *testing.T) {
	t0 := time.Now()
	h := newColdHarness(t, t0)
	h.m.idleCompressPass(t0.Add(3 * time.Second))
	h.m.idleCompressPass(t0.Add(61 * time.Second))
	if err := h.m.QuietResume(t.Context(), "q"); err != nil {
		t.Fatal(err)
	}
	h.v.cmu.Lock()
	h.v.refaults += 10_000 // 10,000 pages × 21 µs = 210 ms
	h.v.cmu.Unlock()
	t1 := t0.Add(70 * time.Second)
	h.pauseAfterRequest(t, t1)
	h.m.idleCompressPass(t1.Add(3 * time.Second))
	if got := testutil.ToFloat64(h.metrics.exceeded.WithLabelValues(IdleCold1.String())); got != 1 {
		t.Fatalf("budget exceeded = %v, want 1", got)
	}
	if got := h.extra(); got != 1 {
		t.Fatalf("ceiling extra = %d, want 1", got)
	}
	// The guest ran, so this pause aged its memory again; L3 now keeps all
	// four generations and compresses nothing.
	h.m.idleCompressPass(t1.Add(63 * time.Second))
	if h.outcome(IdleCold1, "ceiling") != 1 {
		t.Fatalf("L3 outcome ceiling = %v, want 1", h.outcome(IdleCold1, "ceiling"))
	}
	h.m.idleCompressPass(t1.Add(181 * time.Second))
	if _, keeps, _ := h.v.state(); keeps[len(keeps)-1] != 3 {
		t.Fatalf("L4 eviction keeps %v, want 3 with the raised ceiling", keeps)
	}
	h.m.mu.Lock()
	app := h.m.live["q"].AppID
	h.m.mu.Unlock()
	for range 5 {
		h.m.moveColdCeiling(app, time.Millisecond, 100*time.Millisecond)
	}
	if got := h.extra(); got != 0 {
		t.Fatalf("ceiling extra after five cheap wakes = %d, want 0", got)
	}
}

// A request during the compression stops it: the level is not recorded.
func TestColdCompressionStopsOnResume(t *testing.T) {
	t0 := time.Now()
	h := newColdHarness(t, t0)
	h.m.idleCompressPass(t0.Add(3 * time.Second))
	h.v.duringEvict = func() {
		h.act.set("q", 1, t0.Add(61*time.Second))
		if err := h.m.QuietResume(t.Context(), "q"); err != nil {
			t.Error(err)
		}
	}
	h.m.idleCompressPass(t0.Add(61 * time.Second))
	if h.outcome(IdleCold1, "interrupted") != 1 {
		t.Fatalf("interrupted = %v, want 1", h.outcome(IdleCold1, "interrupted"))
	}
	if got := h.m.IdleLadderLevel("q", t0.Add(62*time.Second)); got != IdleRunning {
		t.Fatalf("level = %v, want running", got)
	}
}

// Without memory-only swap and the multi-gen LRU nothing is compressed.
func TestColdCompressionNeedsAReadyHost(t *testing.T) {
	t0 := time.Now()
	h := newColdHarness(t, t0)
	h.v.unready = true
	h.m.idleCompressPass(t0.Add(3 * time.Second))
	h.m.idleCompressPass(t0.Add(61 * time.Second))
	if _, keeps, swap := h.v.state(); len(keeps) != 0 || len(swap) != 0 {
		t.Fatalf("evictions %v swap %v on an unready host, want none", keeps, swap)
	}
	if h.outcome(IdleCold1, "unavailable") != 1 {
		t.Fatalf("unavailable = %v, want 1", h.outcome(IdleCold1, "unavailable"))
	}
}

const lruGenSample = `memcg     1 /
 node     0
          3      90000          10          20
          4      30000          30          40
          5       5000          50          60
memcg     7 /faas.slice/faas-pro.slice/fc-q.scope
 node     0
          8     120000        1000         100
          9      60000        2000           0
         10      30000        3000           0
         11       1000         400          10
 node     1
         20      70000          10           0
         21       1000          20           0
memcg     9 /faas.slice/other.scope
 node     0
          2       1000           1           1
`

func TestParseLRUGenFindsTheVMCgroup(t *testing.T) {
	memcg, ok, err := parseLRUGen(strings.NewReader(lruGenSample), "/faas.slice/faas-pro.slice/fc-q.scope")
	if err != nil || !ok {
		t.Fatalf("parse: ok %v err %v", ok, err)
	}
	if memcg.id != 7 || len(memcg.nodes) != 2 || len(memcg.nodes[0].gens) != 4 || memcg.nodes[1].node != 1 {
		t.Fatalf("memcg = %+v", memcg)
	}
	if seq, pages, ok := memcg.nodes[0].evictable(3); !ok || seq != 8 || pages != 1100 {
		t.Fatalf("evictable(3) = %d %d %v, want seq 8, 1100 pages", seq, pages, ok)
	}
	if seq, pages, ok := memcg.nodes[0].evictable(2); !ok || seq != 9 || pages != 3100 {
		t.Fatalf("evictable(2) = %d %d %v, want seq 9, 3100 pages", seq, pages, ok)
	}
	if _, _, ok := memcg.nodes[1].evictable(2); ok {
		t.Fatal("node with two generations is evictable, want not")
	}
	if _, ok, _ := parseLRUGen(strings.NewReader(lruGenSample), "/missing"); ok {
		t.Fatal("found a missing cgroup")
	}
}

func TestJailerColdPagesWritesLRUGenCommands(t *testing.T) {
	root := withFakeCgroupRoot(t)
	l := Lease{Instance: "q", Plan: "pro"}
	scope := vmCgroupScope(l)
	if err := os.MkdirAll(scope, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"memory.current": "629145600", "memory.swap.current": "0",
		"memory.stat": "anon 1\nzswpin 7\npswpin 2\nworkingset_refault_file 3\n"} {
		if err := os.WriteFile(filepath.Join(scope, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rel := "/" + filepath.ToSlash(strings.TrimPrefix(scope, root+string(filepath.Separator)))
	listing := "memcg 7 " + rel + "\n node 0\n 8 120000 1000 100\n 9 60000 2000 0\n 10 30000 3000 0\n 11 1000 400 10\n"
	gen := filepath.Join(t.TempDir(), "lru_gen")
	saved := lruGenPath
	lruGenPath = gen
	t.Cleanup(func() { lruGenPath = saved })
	write := func() {
		if err := os.WriteFile(gen, []byte(listing), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func() string {
		raw, err := os.ReadFile(gen)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	v := &JailerVMM{}
	write()
	if err := v.AgeVMMemory(l); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "+ 7 0 11 1 1" {
		t.Fatalf("aging command = %q", got)
	}
	write()
	if _, err := v.EvictColdVMMemory(l, 2, func() bool { return false }); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "- 7 0 9 200 4096" {
		t.Fatalf("eviction command = %q", got)
	}
	if n, err := v.VMRefaultPages(l); err != nil || n != 12 {
		t.Fatalf("refaults = %d %v, want 12", n, err)
	}
}
