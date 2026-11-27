package routeresources

import "testing"

func heavyReport() Report {
	return Report{
		MemPeakP50MB: 180,
		Routes: []RouteReport{
			{Route: "POST /v1/exports/{id}", Method: "POST", HighMemoryMinutes: 6, HighMemoryShare: 1, PresenceShare: 0.1,
				MemPeakP95WithMB: 1500, MemRiseP95WithoutMB: 20},
			{Route: "GET /v1/items", Method: "GET", HighMemoryMinutes: 6, HighMemoryShare: 1, PresenceShare: 1,
				MemPeakP95WithMB: 1500, MemRiseP95WithoutMB: 0},
		},
	}
}

func TestSuggestSplitPicksTheRareHeavyRoute(t *testing.T) {
	s := SuggestSplit(heavyReport(), 2048, 2048, SplitOptions{})
	if s == nil {
		t.Fatal("no split suggested")
	}
	// 1500 × 1.25 = 1875 → 1920; (180 + 20) × 1.25 = 250 → 256.
	if s.Route != "POST /v1/exports/{id}" || s.PathGlob != "/v1/exports/*" || s.RouteRAMMB != 1920 || s.MainRAMMB != 256 || s.RouteRAMOverPlan {
		t.Fatalf("split = %+v", *s)
	}
	if s.Reason == "" || s.CurrentRAMMB != 2048 {
		t.Fatalf("split = %+v", *s)
	}
}

func TestSuggestSplitDeclines(t *testing.T) {
	cases := map[string]func(*Report) (current, planMax int){
		"too few peaks":    func(r *Report) (int, int) { r.Routes[0].HighMemoryMinutes = 2; return 2048, 2048 },
		"route runs often": func(r *Report) (int, int) { r.Routes[0].PresenceShare = 0.5; return 2048, 2048 },
		"share too low":    func(r *Report) (int, int) { r.Routes[0].HighMemoryShare = 0.6; return 2048, 2048 },
		"no saving":        func(r *Report) (int, int) { return 256, 2048 },
		"folded route": func(r *Report) (int, int) {
			r.Routes[0].Route = "__route_other__"
			return 2048, 2048
		},
	}
	for name, mutate := range cases {
		r := heavyReport()
		current, planMax := mutate(&r)
		if s := SuggestSplit(r, current, planMax, SplitOptions{}); s != nil {
			t.Errorf("%s: suggested %+v", name, *s)
		}
	}
}

func cpuReport() Report {
	return Report{
		MemPeakP50MB:   180,
		HighCPUMinutes: 5,
		Routes: []RouteReport{
			{Route: "POST /v1/render", Method: "POST", HighCPUMinutes: 5, HighCPUShare: 1, PresenceShare: 0.2},
			{Route: "GET /v1/items", Method: "GET", HighCPUMinutes: 5, HighCPUShare: 1, PresenceShare: 1},
		},
	}
}

func TestSuggestSplitCPU(t *testing.T) {
	s := SuggestSplit(cpuReport(), 512, 2048, SplitOptions{})
	if s == nil {
		t.Fatal("no split suggested")
	}
	if s.Kind != SplitCPU || s.Route != "POST /v1/render" || s.PathGlob != "/v1/render" ||
		s.RouteRAMMB != 512 || s.MainRAMMB != 512 || s.HighCPUMinutes != 5 || s.Reason == "" {
		t.Fatalf("split = %+v", *s)
	}
}

func TestSuggestSplitPrefersMemory(t *testing.T) {
	r := heavyReport()
	r.HighCPUMinutes = 5
	r.Routes = append(r.Routes, RouteReport{Route: "POST /v1/render", HighCPUMinutes: 5, HighCPUShare: 1, PresenceShare: 0.2})
	s := SuggestSplit(r, 2048, 2048, SplitOptions{})
	if s == nil || s.Kind != SplitMemory || s.Route != "POST /v1/exports/{id}" {
		t.Fatalf("split = %+v", s)
	}
}

func TestSuggestSplitCPUDeclines(t *testing.T) {
	cases := map[string]func(*Report){
		"too few peaks":    func(r *Report) { r.Routes[0].HighCPUMinutes = 2 },
		"route runs often": func(r *Report) { r.Routes[0].PresenceShare = 0.5 },
		"share too low":    func(r *Report) { r.Routes[0].HighCPUShare = 0.6 },
		"folded route":     func(r *Report) { r.Routes[0].Route = "__route_other__" },
	}
	for name, mutate := range cases {
		r := cpuReport()
		mutate(&r)
		if s := SuggestSplit(r, 512, 2048, SplitOptions{}); s != nil {
			t.Errorf("%s: suggested %+v", name, *s)
		}
	}
}

func TestSuggestSplitFlagsOverPlan(t *testing.T) {
	s := SuggestSplit(heavyReport(), 1024, 1024, SplitOptions{})
	if s == nil || !s.RouteRAMOverPlan || s.MainRAMMB != 256 {
		t.Fatalf("split = %+v", s)
	}
}

func TestPathGlob(t *testing.T) {
	for in, want := range map[string]string{
		"GET /v1/exports/{id}":       "/v1/exports/*",
		"/pdf/:doc/render":           "/pdf/*/render",
		"GET /grow":                  "/grow",
		"POST /files/{bucket}/{key}": "/files/*/*",
		"__route_other__":            "__route_other__",
	} {
		if got := PathGlob(in); got != want {
			t.Errorf("PathGlob(%q) = %q, want %q", in, got, want)
		}
	}
}
