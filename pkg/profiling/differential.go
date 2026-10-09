package profiling

import (
	"errors"
	"math"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

type differentialNode struct {
	view           api.ProfileStackDelta
	children       map[differentialFrameKey]*differentialNode
	baselineFrame  *api.ProfileStack
	candidateFrame *api.ProfileStack
}

type differentialFrameKey struct {
	name, file string
	line       int64
}

func differentialKey(frame *api.ProfileStack) differentialFrameKey {
	key := differentialFrameKey{name: frame.Name, file: frame.File}
	if frame.Name == "" || frame.Name == "[unknown]" || frame.Name == "(anonymous)" {
		key.line = frame.Line
	}
	return key
}

type differentialBuilder struct {
	nodes, visits, symbolBytes int
}

func differentialFlamegraph(a, b api.ProfileResponse) (*api.ProfileStackDelta, error) {
	if a.Flamegraph == nil || b.Flamegraph == nil {
		return nil, errors.New("both profiles need call-path data for a differential flamegraph")
	}
	root := &differentialNode{view: api.ProfileStackDelta{Name: "all"}}
	builder := differentialBuilder{nodes: 1, symbolBytes: len(root.view.Name)}
	if err := builder.add(root, a.Flamegraph, a.Query.End.Sub(a.Query.Start).Seconds(), true, 0); err != nil {
		return nil, err
	}
	if err := builder.add(root, b.Flamegraph, b.Query.End.Sub(b.Query.Start).Seconds(), false, 0); err != nil {
		return nil, err
	}
	return finishDifferential(root), nil
}

func (b *differentialBuilder) add(target *differentialNode, frame *api.ProfileStack, seconds float64, baseline bool, depth int) error {
	b.visits++
	if depth > api.ProfileMaxStackDepth || b.visits > 2*(api.ProfileMaxViewNodes+1) {
		return differentialBoundsError()
	}
	if frame == nil || seconds <= 0 || !finiteCPU(frame.CPUSeconds) {
		return errors.New("call-path data is unavailable for this comparison")
	}
	rate := frame.CPUSeconds / seconds
	observed, representative, line, source := &target.view.CandidateCPUPerSecond, &target.candidateFrame, &target.view.CandidateLine, &target.view.CandidateSource
	if baseline {
		observed, representative, line, source = &target.view.BaselineCPUPerSecond, &target.baselineFrame, &target.view.BaselineLine, &target.view.BaselineSource
	}
	if *observed == nil {
		*observed = new(float64)
	}
	**observed += rate
	if !finiteCPU(**observed) || !finiteCPU(differentialWidth(&target.view)) {
		return errors.New("call-path CPU rates exceed supported bounds")
	}
	if *representative == nil || preferredFrameSource(frame, *representative) {
		b.symbolBytes += sourceLocationBytes(frame.Source) - sourceLocationBytes(*source)
		*representative, *line, *source = frame, frame.Line, frame.Source
	}
	if b.symbolBytes > api.ProfileMaxViewSymbolBytes {
		return differentialBoundsError()
	}
	childrenCPU := 0.0
	for _, child := range frame.Children {
		if child == nil {
			return errors.New("call-path data is unavailable for this comparison")
		}
		childrenCPU += child.CPUSeconds
		key := differentialKey(child)
		if target.children == nil {
			target.children = make(map[differentialFrameKey]*differentialNode)
		}
		next := target.children[key]
		if next == nil {
			b.nodes++
			b.symbolBytes += len(child.Name) + len(child.File)
			if b.nodes > api.ProfileMaxViewNodes || b.symbolBytes > api.ProfileMaxViewSymbolBytes {
				return differentialBoundsError()
			}
			next = &differentialNode{view: api.ProfileStackDelta{Name: child.Name, File: child.File}}
			target.children[key] = next
		}
		if err := b.add(next, child, seconds, baseline, depth+1); err != nil {
			return err
		}
	}
	if !finiteCPU(childrenCPU) || childrenCPU > frame.CPUSeconds+math.Max(1e-12, frame.CPUSeconds*1e-9) {
		return errors.New("call-path totals are inconsistent for this comparison")
	}
	return nil
}

func finishDifferential(node *differentialNode) *api.ProfileStackDelta {
	out := &node.view
	out.WidthCPUPerSecond = differentialWidth(out)
	if out.BaselineCPUPerSecond != nil && out.CandidateCPUPerSecond != nil {
		delta := *out.CandidateCPUPerSecond - *out.BaselineCPUPerSecond
		out.DeltaCPUPerSecond = &delta
	}
	keys := make([]differentialFrameKey, 0, len(node.children))
	for key := range node.children {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].name != keys[j].name {
			return keys[i].name < keys[j].name
		}
		if keys[i].file != keys[j].file {
			return keys[i].file < keys[j].file
		}
		return keys[i].line < keys[j].line
	})
	out.Children = make([]*api.ProfileStackDelta, 0, len(keys))
	for _, key := range keys {
		out.Children = append(out.Children, finishDifferential(node.children[key]))
	}
	return out
}

func differentialWidth(node *api.ProfileStackDelta) float64 {
	width := 0.0
	if node.BaselineCPUPerSecond != nil {
		width += *node.BaselineCPUPerSecond
	}
	if node.CandidateCPUPerSecond != nil {
		width += *node.CandidateCPUPerSecond
	}
	return width
}

func preferredFrameSource(next, previous *api.ProfileStack) bool {
	return preferredSource(api.ProfileFunction{Line: next.Line, Source: next.Source, SelfCPUSeconds: next.CPUSeconds},
		api.ProfileFunction{Line: previous.Line, Source: previous.Source, SelfCPUSeconds: previous.CPUSeconds})
}

func sourceLocationBytes(location *api.ProfileSourceLocation) int {
	if location == nil {
		return 0
	}
	return len(location.URL) + len(location.Path)
}

func finiteCPU(value float64) bool { return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0) }

func differentialBoundsError() error {
	return errors.New("differential flamegraph exceeds supported bounds; select shorter capture windows")
}
