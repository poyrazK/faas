package profiling

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"runtime"
	"testing"

	"github.com/google/pprof/profile"
	"github.com/onebox-faas/faas/pkg/api"
)

func prescanCPUProfile(samples, depth int) *profile.Profile {
	p := &profile.Profile{SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}}, PeriodType: &profile.ValueType{Type: "cpu", Unit: "nanoseconds"}, Period: 1, TimeNanos: 1, DurationNanos: 1}
	for i := 0; i < depth; i++ {
		fn := &profile.Function{ID: uint64(i + 1), Name: fmt.Sprintf("f%d", i)}
		p.Function = append(p.Function, fn)
		p.Location = append(p.Location, &profile.Location{ID: uint64(i + 1), Line: []profile.Line{{Function: fn}}})
	}
	for i := 0; i < samples; i++ {
		p.Sample = append(p.Sample, &profile.Sample{Location: p.Location, Value: []int64{1}})
	}
	return p
}

func gzipProfile(t *testing.T, p *profile.Profile) []byte {
	t.Helper()
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

func parseAllocMiB(data []byte) (uint64, error) {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := Parse(data)
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) >> 20, err
}

// A small, highly compressible upload must not cost the parser hundreds of
// MiB before it is refused: 1.4M one-frame samples took 268 MiB unscanned.
func TestParseRefusesElementBombsBeforeParsing(t *testing.T) {
	bomb := gzipProfile(t, prescanCPUProfile(1_300_000, 1))
	if len(bomb) > api.ProfileMaxCompressedBytes {
		t.Fatalf("bomb is %d bytes, over the upload bound", len(bomb))
	}
	mib, err := parseAllocMiB(bomb)
	if err == nil {
		t.Fatal("element bomb parsed")
	}
	if mib > 32 {
		t.Fatalf("refusing the bomb allocated %d MiB", mib)
	}
	cases := map[string]*profile.Profile{
		"deep sample": prescanCPUProfile(1, api.ProfileMaxStackDepth+1),
	}
	labelled := prescanCPUProfile(1, 1)
	labelled.Sample[0].Label = map[string][]string{}
	for i := 0; i <= api.ProfileMaxSampleLabels; i++ {
		labelled.Sample[0].Label[fmt.Sprintf("k%d", i)] = []string{"v"}
	}
	cases["labelled sample"] = labelled
	for name, p := range cases {
		if _, err := Parse(gzipProfile(t, p)); err == nil {
			t.Errorf("%s parsed", name)
		}
	}
}

// The largest profile the bounds admit still parses, at a bounded cost.
func TestParseAcceptsProfilesAtTheElementBounds(t *testing.T) {
	depth := api.ProfileMaxTotalFrames / api.ProfileMaxNodes
	data := gzipProfile(t, prescanCPUProfile(api.ProfileMaxNodes, depth))
	mib, err := parseAllocMiB(data)
	if err != nil {
		t.Fatalf("profile at the bounds refused: %v", err)
	}
	t.Logf("profile at the bounds: %d MiB", mib)
	if mib > 64 {
		t.Fatalf("profile at the bounds allocated %d MiB", mib)
	}
}
