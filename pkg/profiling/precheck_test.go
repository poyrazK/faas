package profiling

import (
	"bytes"
	"compress/gzip"
	"runtime"
	"strconv"
	"testing"

	"github.com/google/pprof/profile"

	"github.com/onebox-faas/faas/pkg/api"
)

// A tiny compressed profile with far more samples than the node limit is
// refused before the parser builds it (R4-1/R4-2); a normal one still parses.
func TestParseRefusesTooManySamplesBeforeBuilding(t *testing.T) {
	fn := &profile.Function{ID: 1, Name: "main.f", Filename: "f.go"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn, Line: 1}}}
	build := func(samples int) []byte {
		p := &profile.Profile{SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
			Function: []*profile.Function{fn}, Location: []*profile.Location{loc}}
		for range samples {
			p.Sample = append(p.Sample, &profile.Sample{Location: []*profile.Location{loc}, Value: []int64{1}})
		}
		var raw bytes.Buffer
		if err := p.WriteUncompressed(&raw); err != nil {
			t.Fatal(err)
		}
		var gz bytes.Buffer
		w := gzip.NewWriter(&gz)
		_, _ = w.Write(raw.Bytes())
		_ = w.Close()
		return gz.Bytes()
	}
	if _, err := Parse(build(10)); err != nil {
		t.Fatalf("a small profile was refused: %v", err)
	}
	if _, err := Parse(build(api.ProfileMaxNodes + 1)); err == nil {
		t.Fatal("a profile over the sample limit parsed")
	}
	if err := precheckProfile([]byte{0x12, 0x7f}); err == nil {
		t.Fatal("a truncated length-delimited field passed the precheck")
	}
}

// One sample is objects too: its packed location ids and labels are built by
// the parser before its depth check, so the precheck bounds them per sample.
func TestPrecheckBoundsEachSample(t *testing.T) {
	cpu := []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}}
	fn := &profile.Function{ID: 1, Name: "main.f"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn}}}
	encode := func(p *profile.Profile) []byte {
		var raw bytes.Buffer
		if err := p.WriteUncompressed(&raw); err != nil {
			t.Fatal(err)
		}
		return raw.Bytes()
	}
	// 2M location ids in one sample: ~4 MiB packed, far more parser objects.
	deep := &profile.Sample{Value: []int64{1}}
	for range 2_000_000 {
		deep.Location = append(deep.Location, loc)
	}
	labelled := &profile.Sample{Location: []*profile.Location{loc}, Value: []int64{1}, Label: map[string][]string{}}
	for i := range api.ProfileMaxSampleLabels + 1 {
		labelled.Label[strconv.Itoa(i)] = []string{"v"}
	}
	for name, sample := range map[string]*profile.Sample{"deep": deep, "labelled": labelled} {
		raw := encode(&profile.Profile{SampleType: cpu, Function: []*profile.Function{fn}, Location: []*profile.Location{loc}, Sample: []*profile.Sample{sample}})
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		err := precheckProfile(raw)
		runtime.ReadMemStats(&after)
		if err == nil {
			t.Fatalf("%s sample passed the precheck", name)
		}
		if mib := (after.TotalAlloc - before.TotalAlloc) >> 20; mib > 1 {
			t.Fatalf("refusing the %s sample allocated %d MiB", name, mib)
		}
	}
	ok := &profile.Profile{SampleType: cpu, Function: []*profile.Function{fn}, Location: []*profile.Location{loc},
		Sample: []*profile.Sample{{Location: []*profile.Location{loc, loc}, Value: []int64{1}, Label: map[string][]string{"route": {"GET /"}}}}}
	if err := precheckProfile(encode(ok)); err != nil {
		t.Fatalf("an ordinary sample was refused: %v", err)
	}
}
