package profiling

import (
	"bytes"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestLinkCaptureSourcesFramesAndFunctions(t *testing.T) {
	view := api.ProfileCaptureView{Kind: "heap", Unit: "bytes",
		Functions:  []api.ProfileCaptureFunction{{Name: "grow", File: "/app/src/cache.js", Line: 12}, {Name: "fs", File: "node:fs", Line: 3}},
		Flamegraph: &api.ProfileCaptureStack{Name: "all", Children: []*api.ProfileCaptureStack{{Name: "grow", File: "/app/src/cache.js", Line: 12}}}}
	LinkCaptureSources(&view, sourceOrigin(), "node22")
	if view.Source == nil || !view.Source.Available {
		t.Fatalf("capture provenance missing: %+v", view.Source)
	}
	if got := view.Functions[0].Source; got == nil || got.Path != "apps/api/src/cache.js" || !strings.HasSuffix(got.URL, "#L12") {
		t.Fatalf("function link = %+v", got)
	}
	if view.Functions[1].Source != nil {
		t.Fatal("runtime-internal frame linked")
	}
	if view.Flamegraph.Children[0].Source == nil || view.Flamegraph.Source != nil {
		t.Fatal("flamegraph frames not linked like functions")
	}
	goView := api.ProfileCaptureView{Functions: []api.ProfileCaptureFunction{{Name: "runtime.mallocgc", File: "runtime/malloc.go", Line: 9}}}
	LinkCaptureSources(&goView, sourceOrigin(), "go124")
	if goView.Functions[0].Source != nil {
		t.Fatal("Go standard-library path linked to the repository")
	}
}

func heapProfile(t *testing.T, sampleType string, fn string, bytes int64) []byte {
	t.Helper()
	f := &profile.Function{ID: 1, Name: fn, Filename: "app.js"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: f, Line: 3}}}
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "objects", Unit: "count"}, {Type: sampleType, Unit: "bytes"}},
		PeriodType: &profile.ValueType{Type: "space", Unit: "bytes"}, Period: 524288,
		Sample:   []*profile.Sample{{Location: []*profile.Location{loc}, Value: []int64{2, bytes}, Label: map[string][]string{"secret": {"x"}}}},
		Location: []*profile.Location{loc}, Function: []*profile.Function{f},
	}
	raw, err := EncodeCapture(p)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMergeCaptureAcrossRuntimes(t *testing.T) {
	raws := [][]byte{heapProfile(t, "space", "grow", 4096), heapProfile(t, "inuse_space", "grow", 1024), []byte("garbage")}
	merged, skipped, err := MergeCapture(raws, "heap")
	if err != nil || skipped != 1 || merged == nil {
		t.Fatalf("merge: %v skipped=%d", err, skipped)
	}
	view, err := CaptureView(merged, "heap")
	if err != nil {
		t.Fatal(err)
	}
	if view.Unit != "bytes" || view.Total != 5120 || view.Empty || len(view.Functions) != 1 || view.Functions[0].Name != "grow" || view.Functions[0].Self != 5120 {
		t.Fatalf("unexpected view: %+v", view)
	}
	for _, s := range merged.Sample {
		if len(s.Label) != 0 {
			t.Fatal("customer labels survived normalization")
		}
	}
	if none, _, err := MergeCapture([][]byte{raws[0]}, "cpu"); err != nil || none != nil {
		t.Fatalf("heap profile accepted as CPU: %v", err)
	}
}

var goHeapSink [][]byte

func TestGoHeapProfileNormalizes(t *testing.T) {
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = 512 * 1024 }()
	for i := 0; i < 64; i++ {
		goHeapSink = append(goHeapSink, make([]byte, 1<<16))
	}
	runtime.GC()
	var buf bytes.Buffer
	if err := pprof.Lookup("heap").WriteTo(&buf, 0); err != nil {
		t.Fatal(err)
	}
	merged, skipped, err := MergeCapture([][]byte{buf.Bytes()}, "heap")
	if err != nil || skipped != 0 || merged == nil {
		t.Fatalf("Go heap profile rejected: %v skipped=%d", err, skipped)
	}
	view, err := CaptureView(merged, "heap")
	if err != nil || view.Total < 64<<16 {
		t.Fatalf("Go heap view total %d: %v", view.Total, err)
	}
}

//go:noinline
func parkedStackFixture(release <-chan struct{}, ready chan<- struct{}) {
	ready <- struct{}{}
	<-release
}

func TestGoGoroutineProfileNormalizesAsStacks(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	ready := make(chan struct{})
	for i := 0; i < 3; i++ {
		go parkedStackFixture(release, ready)
		<-ready
	}
	var buf bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&buf, 0); err != nil {
		t.Fatal(err)
	}
	merged, skipped, err := MergeCapture([][]byte{buf.Bytes(), heapProfile(t, "inuse_space", "grow", 1)}, api.ProfileKindStacks)
	if err != nil || skipped != 1 || merged == nil {
		t.Fatalf("goroutine profile rejected or heap accepted as stacks: %v skipped=%d", err, skipped)
	}
	view, err := CaptureView(merged, api.ProfileKindStacks)
	if err != nil || view.Unit != "count" || view.Total < 4 {
		t.Fatalf("stacks view unit=%q total=%d: %v", view.Unit, view.Total, err)
	}
	for _, f := range view.Functions {
		if strings.HasSuffix(f.Name, "parkedStackFixture") {
			if f.Total < 3 {
				t.Fatalf("parked goroutines = %d, want 3", f.Total)
			}
			return
		}
	}
	t.Fatalf("parked goroutines missing from the stacks view: %+v", view.Functions)
}

var contendedMu sync.Mutex

//go:noinline
func contendedLockFixture(hold time.Duration) {
	contendedMu.Lock()
	time.Sleep(hold)
	contendedMu.Unlock()
}

//go:noinline
func blockedReceiveFixture(ch <-chan struct{}) { <-ch }

func goContentionProfile(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := pprof.Lookup(name).WriteTo(&buf, 0); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func assertContentionView(t *testing.T, raw []byte, kind, want string) {
	t.Helper()
	merged, skipped, err := MergeCapture([][]byte{raw, heapProfile(t, "inuse_space", "grow", 1)}, kind)
	if err != nil || skipped != 1 || merged == nil {
		t.Fatalf("%s profile rejected or heap accepted: %v skipped=%d", kind, err, skipped)
	}
	view, err := CaptureView(merged, kind)
	if err != nil || view.Unit != "nanoseconds" || view.Total <= 0 || view.Empty {
		t.Fatalf("%s view unit=%q total=%d empty=%t: %v", kind, view.Unit, view.Total, view.Empty, err)
	}
	for _, f := range view.Functions {
		if strings.HasSuffix(f.Name, want) && f.Total > 0 {
			return
		}
	}
	t.Fatalf("%s view lacks %s: %+v", kind, want, view.Functions)
}

func TestGoMutexProfileNormalizesAsContentionDelay(t *testing.T) {
	previous := runtime.SetMutexProfileFraction(1)
	defer runtime.SetMutexProfileFraction(previous)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); contendedLockFixture(5 * time.Millisecond) }()
	}
	wg.Wait()
	// The mutex profile attributes the delay to the unlocking call site.
	assertContentionView(t, goContentionProfile(t, "mutex"), api.ProfileKindMutex, "contendedLockFixture")
}

func TestGoBlockProfileNormalizesAsContentionDelay(t *testing.T) {
	runtime.SetBlockProfileRate(1)
	defer runtime.SetBlockProfileRate(0)
	ch := make(chan struct{})
	done := make(chan struct{})
	go func() { blockedReceiveFixture(ch); close(done) }()
	time.Sleep(20 * time.Millisecond)
	close(ch)
	<-done
	assertContentionView(t, goContentionProfile(t, "block"), api.ProfileKindBlock, "blockedReceiveFixture")
}

func TestNormalizeContentionDropsCancelledSamples(t *testing.T) {
	f := &profile.Function{ID: 1, Name: "lock", Filename: "main.go"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: f, Line: 3}}}
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "contentions", Unit: "count"}, {Type: "delay", Unit: "nanoseconds"}},
		Sample: []*profile.Sample{{Location: []*profile.Location{loc}, Value: []int64{0, 0}},
			{Location: []*profile.Location{loc}, Value: []int64{2, 900}, Label: map[string][]string{"secret": {"x"}}}},
		Location: []*profile.Location{loc}, Function: []*profile.Function{f},
	}
	if err := NormalizeKind(p, api.ProfileKindBlock); err != nil {
		t.Fatal(err)
	}
	if len(p.Sample) != 1 || p.Sample[0].Value[0] != 900 || len(p.Sample[0].Label) != 0 || p.SampleType[0].Type != "delay" {
		t.Fatalf("normalized samples = %+v, want the one non-zero delay without labels", p.Sample)
	}
	p.Sample = append(p.Sample, &profile.Sample{Location: []*profile.Location{loc}, Value: []int64{-1}})
	p.SampleType = []*profile.ValueType{{Type: "delay", Unit: "nanoseconds"}}
	if err := NormalizeKind(p, api.ProfileKindMutex); err == nil {
		t.Fatal("negative contention sample accepted")
	}
}
