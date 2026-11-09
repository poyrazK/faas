package profiling

import (
	"bytes"
	"compress/gzip"
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
