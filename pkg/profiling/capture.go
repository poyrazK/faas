package profiling

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/google/pprof/profile"
	"github.com/onebox-faas/faas/pkg/api"
)

// HeapProfileType is the Pyroscope series for continuous heap profiles.
const HeapProfileType = "memory:inuse_space:bytes:space:bytes"

// NormalizeHeap keeps only live heap bytes. Go reports inuse_space, V8's
// sampling heap profiler "space" and the Python tracemalloc encoder
// inuse_space; all are bytes. Labels are dropped like CPU sample labels.
func NormalizeHeap(p *profile.Profile) error {
	index := -1
	for i, typ := range p.SampleType {
		if (typ.Type == "inuse_space" || typ.Type == "space") && typ.Unit == "bytes" {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("profile contains no live heap bytes")
	}
	for _, sample := range p.Sample {
		if sample.Value[index] < 0 {
			return fmt.Errorf("negative heap sample")
		}
		sample.Value = []int64{sample.Value[index]}
		sample.Label, sample.NumLabel, sample.NumUnit = nil, nil, nil
	}
	p.SampleType = []*profile.ValueType{{Type: "inuse_space", Unit: "bytes"}}
	p.DefaultSampleType = "inuse_space"
	p.PeriodType = &profile.ValueType{Type: "space", Unit: "bytes"}
	p.Comments = nil
	return nil
}

// NormalizeKind normalizes a parsed profile for kind and resets the period
// so profiles from different runtimes merge.
func NormalizeKind(p *profile.Profile, kind string) error {
	var err error
	switch kind {
	case api.ProfileKindCPU:
		err = NormalizeCPU(p)
	case api.ProfileKindHeap:
		err = NormalizeHeap(p)
	default:
		err = fmt.Errorf("unknown profile kind %q", kind)
	}
	if err == nil {
		p.Period = 1
	}
	return err
}

// MergeCapture parses, normalizes and merges one kind's raw captures.
// Unparseable or mismatched profiles are skipped and counted; nil means no
// usable profile of that kind.
func MergeCapture(raws [][]byte, kind string) (*profile.Profile, int, error) {
	var parsed []*profile.Profile
	skipped := 0
	for _, raw := range raws {
		p, err := Parse(raw)
		if err == nil {
			err = NormalizeKind(p, kind)
		}
		if err != nil {
			skipped++
			continue
		}
		parsed = append(parsed, p)
	}
	if len(parsed) == 0 {
		return nil, skipped, nil
	}
	merged, err := profile.Merge(parsed)
	if err != nil {
		return nil, skipped, fmt.Errorf("merge %s profiles: %w", kind, err)
	}
	return merged, skipped, nil
}

// EncodeCapture returns the gzip pprof encoding of a merged capture.
func EncodeCapture(p *profile.Profile) ([]byte, error) {
	var out bytes.Buffer
	if err := p.Write(&out); err != nil {
		return nil, fmt.Errorf("encode profile: %w", err)
	}
	return out.Bytes(), nil
}

// CaptureView builds the function table and call-path tree of a merged,
// normalized capture. Bounds match the continuous profile view.
func CaptureView(p *profile.Profile, kind string) (api.ProfileCaptureView, error) {
	out := api.ProfileCaptureView{Kind: kind, Unit: "nanoseconds", Functions: []api.ProfileCaptureFunction{},
		Flamegraph: &api.ProfileCaptureStack{Name: "all", Children: []*api.ProfileCaptureStack{}}, Empty: true}
	if kind == api.ProfileKindHeap {
		out.Unit = "bytes"
	}
	if p == nil {
		return out, nil
	}
	functions := map[string]*api.ProfileCaptureFunction{}
	children := map[*api.ProfileCaptureStack]map[string]*api.ProfileCaptureStack{}
	nodes, symbolBytes := 0, 0
	for _, s := range p.Sample {
		value := s.Value[0]
		if value == 0 {
			continue
		}
		frames := sampleFrames(s)
		if len(frames) == 0 {
			frames = []frame{{name: "[unknown]"}}
		}
		out.Total += value
		seen := map[string]bool{}
		for i, f := range frames {
			key := frameKey(f)
			fn := functions[key]
			if fn == nil {
				symbolBytes += len(f.name) + len(f.file)
				if symbolBytes > api.ProfileMaxViewSymbolBytes {
					return out, fmt.Errorf("profile source symbols exceed view bounds")
				}
				fn = &api.ProfileCaptureFunction{Name: f.name, File: f.file, Line: f.line}
				functions[key] = fn
			}
			if !seen[key] {
				fn.Total += value
				seen[key] = true
			}
			if i == 0 {
				fn.Self += value
			}
		}
		node := out.Flamegraph
		node.Value += value
		for i := len(frames) - 1; i >= 0; i-- {
			if children[node] == nil {
				children[node] = map[string]*api.ProfileCaptureStack{}
			}
			key := frameKey(frames[i])
			child := children[node][key]
			if child == nil {
				nodes++
				symbolBytes += len(frames[i].name) + len(frames[i].file)
				if nodes > api.ProfileMaxViewNodes || symbolBytes > api.ProfileMaxViewSymbolBytes {
					return out, fmt.Errorf("profile has too many call paths to display")
				}
				child = &api.ProfileCaptureStack{Name: frames[i].name, File: frames[i].file, Line: frames[i].line, Children: []*api.ProfileCaptureStack{}}
				children[node][key] = child
				node.Children = append(node.Children, child)
			}
			child.Value += value
			node = child
		}
	}
	for _, f := range functions {
		out.Functions = append(out.Functions, *f)
	}
	sort.Slice(out.Functions, func(i, j int) bool {
		a, b := out.Functions[i], out.Functions[j]
		if a.Self != b.Self {
			return a.Self > b.Self
		}
		return frameKey(frame{a.Name, a.File, a.Line}) < frameKey(frame{b.Name, b.File, b.Line})
	})
	for parent := range children {
		sort.Slice(parent.Children, func(i, j int) bool { return parent.Children[i].Name < parent.Children[j].Name })
	}
	out.Empty = out.Total == 0
	return out, nil
}
