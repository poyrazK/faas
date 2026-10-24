package profiling

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func diffStack(name, file string, line, value int64, children ...*api.ProfileCaptureStack) *api.ProfileCaptureStack {
	if children == nil {
		children = []*api.ProfileCaptureStack{}
	}
	return &api.ProfileCaptureStack{Name: name, File: file, Line: line, Value: value, Children: children}
}

func heapView(capture api.ProfileCapture, root *api.ProfileCaptureStack, functions ...api.ProfileCaptureFunction) api.ProfileCaptureView {
	return api.ProfileCaptureView{Capture: capture, Kind: api.ProfileKindHeap, Unit: "bytes", Total: root.Value, Flamegraph: root, Functions: functions, Empty: root.Value == 0}
}

func findDelta(node *api.ProfileCaptureStackDelta, path ...string) *api.ProfileCaptureStackDelta {
	for _, name := range path {
		var next *api.ProfileCaptureStackDelta
		for _, child := range node.Children {
			if child.Name == name {
				next = child
			}
		}
		if next == nil {
			return nil
		}
		node = next
	}
	return node
}

func TestDiffCaptureViews(t *testing.T) {
	t0 := time.Unix(1000, 0)
	baseCapture := api.ProfileCapture{ID: "a", DurationSeconds: 10, DeploymentID: "d1", CreatedAt: t0}
	candCapture := api.ProfileCapture{ID: "b", DurationSeconds: 10, DeploymentID: "d1", CreatedAt: t0.Add(time.Minute)}
	base := heapView(baseCapture,
		diffStack("all", "", 0, 100, diffStack("main", "app.js", 1, 100, diffStack("grow", "app.js", 10, 100))),
		api.ProfileCaptureFunction{Name: "grow", File: "app.js", Line: 10, Self: 100})
	// grow moved to line 12 in the candidate; it must still pair with the
	// baseline frame, while the new leak path is candidate-only.
	cand := heapView(candCapture,
		diffStack("all", "", 0, 350, diffStack("main", "app.js", 1, 350,
			diffStack("grow", "app.js", 12, 300), diffStack("leak", "cache.js", 4, 50))),
		api.ProfileCaptureFunction{Name: "grow", File: "app.js", Line: 12, Self: 300},
		api.ProfileCaptureFunction{Name: "leak", File: "cache.js", Line: 4, Self: 50})

	out, err := DiffCaptureViews(base, cand)
	if err != nil {
		t.Fatal(err)
	}
	if out.BaselineTotal != 100 || out.CandidateTotal != 350 || out.Delta != 250 || out.Unit != "bytes" {
		t.Fatalf("totals = %+v", out)
	}
	for _, tc := range []struct {
		path                       []string
		baseline, candidate, delta int64
		line                       int64
	}{
		{[]string{"main"}, 100, 350, 250, 1},
		{[]string{"main", "grow"}, 100, 300, 200, 12},
		{[]string{"main", "leak"}, 0, 50, 50, 4},
	} {
		node := findDelta(out.Flamegraph, tc.path...)
		if node == nil || node.Baseline != tc.baseline || node.Candidate != tc.candidate || node.Delta != tc.delta || node.Line != tc.line {
			t.Fatalf("%v = %+v", tc.path, node)
		}
	}
	if len(out.Functions) != 2 || out.Functions[0].Name != "grow" || out.Functions[0].Delta != 200 || out.Functions[1].Name != "leak" {
		t.Fatalf("functions = %+v", out.Functions)
	}
	if len(out.Warnings) != 0 {
		t.Fatalf("warnings = %v", out.Warnings)
	}
}

func TestDiffCaptureViewsWarnings(t *testing.T) {
	t0 := time.Unix(1000, 0)
	root := func(v int64) *api.ProfileCaptureStack { return diffStack("all", "", 0, v) }
	for _, tc := range []struct {
		name       string
		base, cand api.ProfileCapture
		baseValue  int64
		want       []string
	}{
		{"durations", api.ProfileCapture{DurationSeconds: 10, CreatedAt: t0}, api.ProfileCapture{DurationSeconds: 30, CreatedAt: t0}, 1, []string{"10 s and 30 s"}},
		{"deployments", api.ProfileCapture{DurationSeconds: 10, DeploymentID: "d1", CreatedAt: t0}, api.ProfileCapture{DurationSeconds: 10, DeploymentID: "d2", CreatedAt: t0}, 1, []string{"different deployments"}},
		{"order", api.ProfileCapture{DurationSeconds: 10, CreatedAt: t0.Add(time.Hour)}, api.ProfileCapture{DurationSeconds: 10, CreatedAt: t0}, 1, []string{"baseline is newer"}},
		{"partial", api.ProfileCapture{DurationSeconds: 10, CreatedAt: t0}, api.ProfileCapture{DurationSeconds: 10, CreatedAt: t0, Partial: true}, 1, []string{"candidate ended early"}},
		{"empty", api.ProfileCapture{DurationSeconds: 10, CreatedAt: t0}, api.ProfileCapture{DurationSeconds: 10, CreatedAt: t0}, 0, []string{"no samples"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := DiffCaptureViews(heapView(tc.base, root(tc.baseValue)), heapView(tc.cand, root(5)))
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(out.Warnings, "\n")
			for _, want := range tc.want {
				if !strings.Contains(joined, want) {
					t.Fatalf("warnings %q lack %q", joined, want)
				}
			}
		})
	}
}

func TestDiffCaptureViewsRejectsMixedKinds(t *testing.T) {
	base := heapView(api.ProfileCapture{}, diffStack("all", "", 0, 1))
	cand := base
	cand.Kind = api.ProfileKindCPU
	if _, err := DiffCaptureViews(base, cand); err == nil {
		t.Fatal("mixed kinds compared")
	}
}

func TestDiffCaptureViewsBounds(t *testing.T) {
	wide := diffStack("all", "", 0, 0)
	for i := 0; i <= api.ProfileCaptureDiffMaxNodes; i++ {
		wide.Children = append(wide.Children, diffStack("f"+strconv.Itoa(i), "", 0, 1))
	}
	if _, err := DiffCaptureViews(heapView(api.ProfileCapture{}, wide), heapView(api.ProfileCapture{}, diffStack("all", "", 0, 1))); err == nil {
		t.Fatal("unbounded comparison accepted")
	}
}
