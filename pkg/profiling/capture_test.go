package profiling

import (
	"bytes"
	"runtime"
	"runtime/pprof"
	"testing"

	"github.com/google/pprof/profile"
)

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
