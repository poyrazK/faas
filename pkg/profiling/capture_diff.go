package profiling

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// DiffCaptureViews compares one kind of two capture views (ADR-1155): the
// call-path tree and function table of each are merged, and every node
// carries both values and their difference. Named frames are keyed by name
// and file so moved lines between deployments do not split a call path;
// unknown and anonymous frames keep their line as identity.
func DiffCaptureViews(base, cand api.ProfileCaptureView) (api.ProfileCaptureDiff, error) {
	out := api.ProfileCaptureDiff{Baseline: base.Capture, Candidate: cand.Capture, Kind: cand.Kind, Unit: cand.Unit,
		BaselineTotal: base.Total, CandidateTotal: cand.Total, Delta: cand.Total - base.Total,
		Functions: []api.ProfileCaptureFunctionDelta{}, Warnings: []string{}}
	if base.Kind != cand.Kind {
		return out, errors.New("captures must be compared on the same profile kind")
	}
	root := &captureDiffNode{view: api.ProfileCaptureStackDelta{Name: "all"}}
	b := captureDiffBuilder{nodes: 1}
	if err := b.add(root, base.Flamegraph, true, 0); err != nil {
		return out, err
	}
	if err := b.add(root, cand.Flamegraph, false, 0); err != nil {
		return out, err
	}
	out.Flamegraph = root.finish()
	out.Functions = diffCaptureFunctions(base.Functions, cand.Functions)
	out.Warnings = captureDiffWarnings(base, cand)
	return out, nil
}

type captureDiffNode struct {
	view     api.ProfileCaptureStackDelta
	children map[differentialFrameKey]*captureDiffNode
}

type captureDiffBuilder struct {
	nodes, symbolBytes int
}

func captureDiffKey(name, file string, line int64) differentialFrameKey {
	key := differentialFrameKey{name: name, file: file}
	if name == "" || name == "[unknown]" || name == "(anonymous)" {
		key.line = line
	}
	return key
}

func (b *captureDiffBuilder) add(target *captureDiffNode, frame *api.ProfileCaptureStack, baseline bool, depth int) error {
	if frame == nil {
		return nil
	}
	if depth > api.ProfileMaxStackDepth {
		return captureDiffBoundsError()
	}
	if baseline {
		target.view.Baseline += frame.Value
	} else {
		target.view.Candidate += frame.Value
		// The candidate's location wins: links then point at its commit.
		target.view.Line, target.view.Source = frame.Line, frame.Source
	}
	if target.view.Line == 0 {
		target.view.Line = frame.Line
	}
	if target.view.Source == nil {
		target.view.Source = frame.Source
	}
	for _, child := range frame.Children {
		if child == nil {
			continue
		}
		key := captureDiffKey(child.Name, child.File, child.Line)
		next := target.children[key]
		if next == nil {
			b.nodes++
			b.symbolBytes += len(child.Name) + len(child.File)
			if b.nodes > api.ProfileCaptureDiffMaxNodes || b.symbolBytes > 2*api.ProfileMaxViewSymbolBytes {
				return captureDiffBoundsError()
			}
			if target.children == nil {
				target.children = map[differentialFrameKey]*captureDiffNode{}
			}
			next = &captureDiffNode{view: api.ProfileCaptureStackDelta{Name: child.Name, File: child.File}}
			target.children[key] = next
		}
		if err := b.add(next, child, baseline, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (n *captureDiffNode) finish() *api.ProfileCaptureStackDelta {
	out := &n.view
	out.Delta = out.Candidate - out.Baseline
	keys := make([]differentialFrameKey, 0, len(n.children))
	for key := range n.children {
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
	out.Children = make([]*api.ProfileCaptureStackDelta, 0, len(keys))
	for _, key := range keys {
		out.Children = append(out.Children, n.children[key].finish())
	}
	return out
}

// diffCaptureFunctions pairs self costs by function, largest growth first.
func diffCaptureFunctions(base, cand []api.ProfileCaptureFunction) []api.ProfileCaptureFunctionDelta {
	rows := map[differentialFrameKey]*api.ProfileCaptureFunctionDelta{}
	row := func(f api.ProfileCaptureFunction) *api.ProfileCaptureFunctionDelta {
		key := captureDiffKey(f.Name, f.File, f.Line)
		if rows[key] == nil {
			rows[key] = &api.ProfileCaptureFunctionDelta{Name: f.Name, File: f.File, Line: f.Line, Source: f.Source}
		}
		return rows[key]
	}
	for _, f := range base {
		row(f).Baseline += f.Self
	}
	for _, f := range cand {
		r := row(f)
		r.Candidate += f.Self
		if f.Source != nil {
			r.Line, r.Source = f.Line, f.Source
		}
	}
	out := make([]api.ProfileCaptureFunctionDelta, 0, len(rows))
	for _, r := range rows {
		r.Delta = r.Candidate - r.Baseline
		if r.Baseline == 0 && r.Candidate == 0 {
			continue
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Delta != out[j].Delta {
			return out[i].Delta > out[j].Delta
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].File < out[j].File
	})
	return out
}

func captureDiffWarnings(base, cand api.ProfileCaptureView) []string {
	warnings := []string{}
	b, c := base.Capture, cand.Capture
	if b.DurationSeconds != c.DurationSeconds {
		what := "CPU time"
		if cand.Kind == api.ProfileKindHeap {
			what = "Node and Python heap values"
		}
		warnings = append(warnings, fmt.Sprintf("The captures ran for %d s and %d s; %s grow with the window, so compare captures of equal duration.", b.DurationSeconds, c.DurationSeconds, what))
	}
	if b.DeploymentID != "" && c.DeploymentID != "" && b.DeploymentID != c.DeploymentID {
		warnings = append(warnings, "The captures come from different deployments; code changes can move cost between functions.")
	}
	if b.CreatedAt.After(c.CreatedAt) {
		warnings = append(warnings, "The baseline is newer than the candidate; positive values show what was larger in the older capture.")
	}
	var partial []string
	for _, side := range []struct {
		name string
		c    api.ProfileCapture
	}{{"baseline", b}, {"candidate", c}} {
		if side.c.Partial {
			partial = append(partial, side.name)
		}
	}
	if len(partial) > 0 {
		warnings = append(warnings, fmt.Sprintf("The %s ended early; its profiles are the last checkpoints before the instance stopped.", strings.Join(partial, " and ")))
	}
	if base.Empty || cand.Empty {
		warnings = append(warnings, "One capture has no samples of this kind; every value is one-sided.")
	}
	return warnings
}

func captureDiffBoundsError() error {
	return errors.New("the comparison has too many call paths to display; download both pprof files and compare them with go tool pprof -diff_base")
}
