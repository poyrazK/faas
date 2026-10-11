package profiling

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func differentialFixture(seconds time.Duration, children ...*api.ProfileStack) api.ProfileResponse {
	now := time.Now()
	root := &api.ProfileStack{Name: "all", Children: children}
	for _, child := range children {
		root.CPUSeconds += child.CPUSeconds
	}
	return api.ProfileResponse{Query: api.ProfileQuery{Runtime: "node24", Start: now.Add(-seconds), End: now}, CPUSeconds: root.CPUSeconds, Flamegraph: root}
}

func differentialLeaf(name, file string, line int64, cpu float64) *api.ProfileStack {
	return &api.ProfileStack{Name: name, File: file, Line: line, CPUSeconds: cpu}
}

func closeRate(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-12 {
		t.Fatalf("rate = %v, want %v", got, want)
	}
}

func TestDifferentialNormalizesWindowsAndKeepsMovedSourceRevisions(t *testing.T) {
	a := differentialFixture(10*time.Second, differentialLeaf("hot", "app.js", 10, 1), differentialLeaf("hot", "app.js", 11, 2))
	b := differentialFixture(20*time.Second, differentialLeaf("hot", "app.js", 35, 8))
	old, next := sourceOrigin(), sourceOrigin()
	next.CommitSHA = strings.Repeat("b", 40)
	next.SourceURL = "github://acme/service@" + next.CommitSHA
	LinkSources(&a, old)
	LinkSources(&b, next)
	result := Compare(a, b)
	if result.Flamegraph == nil || len(result.Flamegraph.Children) != 1 || result.FlamegraphReason != "" {
		t.Fatal("moved lines split a named call path", result)
	}
	f := result.Flamegraph.Children[0]
	closeRate(t, f.BaselineCPUPerSecond, .3)
	closeRate(t, f.CandidateCPUPerSecond, .4)
	closeRate(t, f.DeltaCPUPerSecond, .1)
	if math.Abs(f.WidthCPUPerSecond-.7) > 1e-12 || f.BaselineLine != 11 || f.CandidateLine != 35 || f.BaselineSource == nil || f.CandidateSource == nil || !strings.Contains(f.BaselineSource.URL, old.CommitSHA) || !strings.Contains(f.CandidateSource.URL, next.CommitSHA) {
		t.Fatal("source revision or additive width lost", f)
	}
}

func TestDifferentialRetainsCompleteCallerPathFileAndRecursion(t *testing.T) {
	caller := func(name string, cpu float64, children ...*api.ProfileStack) *api.ProfileStack {
		return &api.ProfileStack{Name: name, File: "app.js", CPUSeconds: cpu, Children: children}
	}
	a := differentialFixture(10*time.Second,
		caller("one", 1, differentialLeaf("hot", "first.js", 5, 1)),
		caller("two", 1, differentialLeaf("hot", "first.js", 5, 1)),
		caller("recurse", 1, caller("recurse", 1)),
	)
	b := differentialFixture(10*time.Second,
		caller("one", 2, differentialLeaf("hot", "first.js", 50, 2)),
		caller("two", 1, differentialLeaf("hot", "second.js", 5, 1)),
		caller("recurse", 1, caller("recurse", 1)),
	)
	tree, err := differentialFlamegraph(a, b)
	if err != nil {
		t.Fatal(err)
	}
	closeRate(t, tree.Children[0].Children[0].DeltaCPUPerSecond, .1)
	if tree.Children[1].Children[0].Name != "recurse" || tree.Children[1].Children[0].DeltaCPUPerSecond == nil {
		t.Fatal("recursive paths collapsed")
	}
	if len(tree.Children[2].Children) != 2 || tree.Children[2].Children[0].DeltaCPUPerSecond != nil || tree.Children[2].Children[1].DeltaCPUPerSecond != nil {
		t.Fatal("different files under same caller were merged")
	}
}

func TestDifferentialUnobservedPathsHaveNoFabricatedZeroOrDelta(t *testing.T) {
	a := differentialFixture(10*time.Second, differentialLeaf("old", "app.js", 10, 1), differentialLeaf("same", "app.js", 10, 1))
	b := differentialFixture(10*time.Second, differentialLeaf("new", "app.js", 20, 1), differentialLeaf("same", "app.js", 20, 0))
	tree, err := differentialFlamegraph(a, b)
	if err != nil {
		t.Fatal(err)
	}
	newFrame, oldFrame, sameFrame := tree.Children[0], tree.Children[1], tree.Children[2]
	if newFrame.BaselineCPUPerSecond != nil || newFrame.CandidateCPUPerSecond == nil || newFrame.DeltaCPUPerSecond != nil || oldFrame.BaselineCPUPerSecond == nil || oldFrame.CandidateCPUPerSecond != nil || oldFrame.DeltaCPUPerSecond != nil {
		t.Fatal("unobserved path shown as measured zero")
	}
	closeRate(t, sameFrame.CandidateCPUPerSecond, 0)
	closeRate(t, sameFrame.DeltaCPUPerSecond, -.1)
	encoded, err := json.Marshal(newFrame)
	if err != nil || strings.Contains(string(encoded), "baseline_cpu_per_second") || strings.Contains(string(encoded), "delta_cpu_per_second") {
		t.Fatal("wire encoded unknown rates as zero", string(encoded), err)
	}
	a.Functions = []api.ProfileFunction{{Name: "old", SelfCPUSeconds: 1}}
	b.Functions = []api.ProfileFunction{{Name: "new", SelfCPUSeconds: 1}}
	for _, row := range Compare(a, b).Functions {
		if row.DeltaKnown || row.BaselineObserved == row.CandidateObserved {
			t.Fatal("function table fabricated a zero comparison", row)
		}
	}
	a.Empty = true
	if result := Compare(a, b); result.Comparable || result.Flamegraph != nil || result.Reason == "" {
		t.Fatal("absent profile still produced a differential")
	}
}

func TestDifferentialAnonymousLineIdentityAndDeterministicOrder(t *testing.T) {
	a := differentialFixture(10*time.Second, differentialLeaf("(anonymous)", "app.js", 10, 1), differentialLeaf("known", "app.js", 20, 1))
	b := differentialFixture(10*time.Second, differentialLeaf("known", "app.js", 30, 1), differentialLeaf("(anonymous)", "app.js", 11, 1))
	first, err := differentialFlamegraph(a, b)
	if err != nil || len(first.Children) != 3 || first.Children[0].DeltaCPUPerSecond != nil || first.Children[1].DeltaCPUPerSecond != nil {
		t.Fatal("anonymous frames matched across line move", first, err)
	}
	b.Flamegraph.Children[0], b.Flamegraph.Children[1] = b.Flamegraph.Children[1], b.Flamegraph.Children[0]
	second, err := differentialFlamegraph(a, b)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("input order changed differential output", err)
	}
}

func TestDifferentialLayoutAdditiveWhenHotChildrenSwitch(t *testing.T) {
	a := differentialFixture(10*time.Second, differentialLeaf("one", "app.js", 1, 9), differentialLeaf("two", "app.js", 2, 1))
	b := differentialFixture(10*time.Second, differentialLeaf("one", "app.js", 1, 1), differentialLeaf("two", "app.js", 2, 9))
	tree, err := differentialFlamegraph(a, b)
	if err != nil {
		t.Fatal(err)
	}
	closeRate(t, tree.Children[0].DeltaCPUPerSecond, -.8)
	closeRate(t, tree.Children[1].DeltaCPUPerSecond, .8)
	if tree.Children[0].WidthCPUPerSecond+tree.Children[1].WidthCPUPerSecond != tree.WidthCPUPerSecond {
		t.Fatal("union child widths overflowed parent")
	}
}

func TestDifferentialBoundsPreserveFunctionComparison(t *testing.T) {
	a := differentialFixture(time.Second)
	b := differentialFixture(time.Second)
	for i := range api.ProfileMaxViewNodes/2 + 1 {
		a.Flamegraph.Children = append(a.Flamegraph.Children, differentialLeaf("baseline-"+strconv.Itoa(i), "app.js", 1, 1))
		b.Flamegraph.Children = append(b.Flamegraph.Children, differentialLeaf("candidate-"+strconv.Itoa(i), "app.js", 1, 1))
		a.Flamegraph.CPUSeconds++
		b.Flamegraph.CPUSeconds++
	}
	result := Compare(a, b)
	if !result.Comparable || result.Flamegraph != nil || !strings.Contains(result.FlamegraphReason, "bounds") {
		t.Fatal("union bounds truncated the graph or discarded the function comparison")
	}
	for _, invalid := range []float64{math.NaN(), math.Inf(1), -1} {
		a = differentialFixture(time.Second, differentialLeaf("bad", "app.js", 1, invalid))
		if result := Compare(a, b); result.Flamegraph != nil || result.FlamegraphReason == "" {
			t.Fatal("invalid CPU entered the differential tree")
		}
	}
}

func TestDifferentialDepthSymbolAndLayoutValidation(t *testing.T) {
	valid := differentialFixture(time.Second, differentialLeaf("valid", "app.js", 1, 1))
	for _, makeTree := range []func() api.ProfileResponse{
		func() api.ProfileResponse {
			root := differentialLeaf("root", "app.js", 1, 1)
			parent := root
			for range api.ProfileMaxStackDepth + 1 {
				child := differentialLeaf("recursive", "app.js", 1, 1)
				parent.Children = []*api.ProfileStack{child}
				parent = child
			}
			out := differentialFixture(time.Second)
			out.Flamegraph = root
			return out
		},
		func() api.ProfileResponse {
			out := differentialFixture(time.Second)
			for i := range api.ProfileMaxViewSymbolBytes/api.ProfileMaxSymbolBytes + 1 {
				frame := differentialLeaf("large-"+strconv.Itoa(i), strings.Repeat("x", api.ProfileMaxSymbolBytes), 1, 1)
				out.Flamegraph.Children = append(out.Flamegraph.Children, frame)
				out.Flamegraph.CPUSeconds++
			}
			return out
		},
		func() api.ProfileResponse {
			out := differentialFixture(time.Second, differentialLeaf("invalid-child", "app.js", 1, 2))
			out.Flamegraph.CPUSeconds = 1
			return out
		},
	} {
		if result := Compare(makeTree(), valid); result.Flamegraph != nil || result.FlamegraphReason == "" || !result.Comparable {
			t.Fatal("invalid differential was drawn or discarded the function comparison")
		}
	}
}
