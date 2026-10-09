package profiling

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/onebox-faas/faas/pkg/api"
)

func sourceOrigin() SourceProvenance {
	sha := strings.Repeat("a", 40)
	return SourceProvenance{SourceURL: "github://acme/service@" + sha, CommitSHA: sha, SourceRoot: "apps/api", ManagedPaths: true}
}

func TestProfileSourceMapsOnlySafeRecordedPaths(t *testing.T) {
	for _, tc := range []struct {
		file, want string
	}{
		{"/app/src/server.js", "apps/api/src/server.js"},
		{"file:///app/src/server.js", "apps/api/src/server.js"},
		{"./server.py", "apps/api/server.py"},
		{"src/a b#c?.go", "apps/api/src/a b#c?.go"},
		{"/app/../secret.js", ""}, {"../../secret.js", ""}, {"/etc/passwd", ""},
		{"/app/node_modules/lib/index.js", ""}, {".venv/lib/main.py", ""},
		{"venv/lib/main.py", ""}, {".git/config", ""}, {"<eval>", ""},
		{"https://evil.test/x", ""}, {"file://evil.test/app/server.js", ""},
		{"file:///app/server.js?token=secret", ""}, {"file:///app/%2e%2e/secret.js", ""},
		{"src\\server.js", ""}, {"src/%2e%2e/secret.js", ""}, {"server.js\n", ""},
		{"/app//server.js", ""}, {"src/./server.js", ""}, {"", ""},
	} {
		t.Run(tc.file, func(t *testing.T) {
			out := api.ProfileResponse{Functions: []api.ProfileFunction{{File: tc.file, Line: 42}}}
			LinkSources(&out, sourceOrigin())
			location := out.Functions[0].Source
			if tc.want == "" {
				if location != nil {
					t.Fatalf("unmapped path became a link: %+v", location)
				}
				return
			}
			if location == nil || location.Path != tc.want || location.Line != 42 {
				t.Fatalf("incorrect mapping: %+v", location)
			}
			u, err := url.Parse(location.URL)
			if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.Fragment != "L42" || u.RawQuery != "" || u.Path != "/acme/service/blob/"+sourceOrigin().CommitSHA+"/"+tc.want {
				t.Fatalf("unsafe or incorrectly escaped link: %s", location.URL)
			}
		})
	}
}

func TestProfileSourceUnavailableProvenanceAndSymbols(t *testing.T) {
	for _, mutate := range []func(*SourceProvenance){
		func(p *SourceProvenance) { p.SourceURL = "https://github.com/acme/service" },
		func(p *SourceProvenance) { p.SourceURL = "github://evil.test@github.com/acme/service@" + p.CommitSHA },
		func(p *SourceProvenance) { p.SourceURL = "github://acme/../service@" + p.CommitSHA },
		func(p *SourceProvenance) { p.CommitSHA = "main" },
		func(p *SourceProvenance) { p.CommitSHA = strings.Repeat("b", 40) },
		func(p *SourceProvenance) { p.SourceRoot = "../other" },
	} {
		origin := sourceOrigin()
		mutate(&origin)
		out := api.ProfileResponse{Functions: []api.ProfileFunction{{File: "server.js", Line: 7}}}
		LinkSources(&out, origin)
		if out.Source.Available || out.Source.Reason == "" || out.Functions[0].Source != nil {
			t.Fatalf("invalid provenance linked: %+v, %+v", origin, out)
		}
	}
	origin := sourceOrigin()
	origin.CommitSHA = "" // legacy rows retain the immutable @suffix
	out := api.ProfileResponse{Functions: []api.ProfileFunction{{File: "server.js", Line: 7}, {File: "server.js", Line: 0}}}
	LinkSources(&out, origin)
	if !out.Source.Available || out.Functions[0].Source == nil || out.Functions[1].Source != nil {
		t.Fatal("legacy revision or missing line handling failed")
	}
	origin.ManagedPaths = false
	origin.Function = true
	out.Functions = []api.ProfileFunction{{File: "/app/server.js", Line: 7}, {File: "node24.js", Line: 7}, {File: "handler.py", Line: 7}}
	LinkSources(&out, origin)
	for _, f := range out.Functions {
		if f.Source != nil {
			t.Fatal("unknown image layout or generated adapter linked", f)
		}
	}
}

func TestProfileSourceFrameIdentityAndLinkBudget(t *testing.T) {
	first := &api.ProfileStack{Name: "hot", File: "/app/one.js", Line: 7}
	second := &api.ProfileStack{Name: "hot", File: "/app/two.js", Line: 9}
	out := api.ProfileResponse{Flamegraph: &api.ProfileStack{Name: "all", Children: []*api.ProfileStack{first, second}}}
	LinkSources(&out, sourceOrigin())
	if first.Source == nil || second.Source == nil || first.Source.URL == second.Source.URL || out.Flamegraph.Source != nil {
		t.Fatal("same-name frames lost their source identity")
	}
	for range api.ProfileMaxViewNodes {
		out.Functions = append(out.Functions, api.ProfileFunction{File: strings.Repeat("a", api.ProfileMaxSymbolBytes-3) + ".js", Line: 7})
	}
	LinkSources(&out, sourceOrigin())
	linked, bytes := 0, 0
	for _, f := range out.Functions {
		if f.Source != nil {
			linked++
			bytes += len(f.Source.URL) + len(f.Source.Path)
		}
	}
	if bytes > api.ProfileMaxViewSymbolBytes || linked == 0 || linked == len(out.Functions) {
		t.Fatal("source links bypassed output bounds", bytes, linked)
	}
}

func TestProfileViewKeepsDistinctSameNameSourceFrames(t *testing.T) {
	p := cpuFixture(time.Now(), 1e8)
	other := &profile.Function{ID: 3, Name: p.Function[0].Name, Filename: "other.go"}
	location := &profile.Location{ID: 3, Line: []profile.Line{{Function: other, Line: 42}}}
	p.Function = append(p.Function, other)
	p.Location = append(p.Location, location)
	p.Sample = append(p.Sample, &profile.Sample{Location: []*profile.Location{location, p.Location[1]}, Value: []int64{2e8}})
	out, err := View(p, api.ProfileQuery{Runtime: "node24"})
	if err != nil {
		t.Fatal(err)
	}
	LinkSources(&out, sourceOrigin())
	children := out.Flamegraph.Children[0].Children
	if len(children) != 2 || children[0].Name != children[1].Name || children[0].Source == nil || children[1].Source == nil || children[0].Source.URL == children[1].Source.URL {
		t.Fatal("merged same-name frames shared a source link", children)
	}
	p.Function[0].Filename = strings.Repeat("x", api.ProfileMaxSymbolBytes)
	p.Sample[0].Location = make([]*profile.Location, 100)
	for i := range p.Sample[0].Location {
		p.Sample[0].Location[i] = p.Location[0]
	}
	if _, err := View(p, api.ProfileQuery{}); err == nil {
		t.Fatal("repeated filenames bypassed the view's symbol budget")
	}
}

func TestProfileSourceGoModulesAndPythonImplementation(t *testing.T) {
	origin := sourceOrigin()
	out := api.ProfileResponse{Query: api.ProfileQuery{Runtime: "go124"}, Functions: []api.ProfileFunction{
		{File: "github.com/acme/service/apps/api/server.go", Line: 9},
		{File: "github.com/another/dependency/lib.go", Line: 9},
		{File: "runtime/proc.go", Line: 9},
		{File: "/app/server.go", Line: 9},
	}}
	LinkSources(&out, origin)
	if out.Functions[0].Source == nil || out.Functions[0].Source.Path != "apps/api/server.go" || out.Functions[1].Source != nil || out.Functions[2].Source != nil || out.Functions[3].Source == nil {
		t.Fatal("trimmed Go modules mapped incorrectly", out.Functions)
	}
	origin.Function = true
	out.Query.Runtime = "python313"
	out.Functions = []api.ProfileFunction{{File: "/app/.faas-handler.py", Line: 18}, {File: "/app/handler.py", Line: 100}}
	LinkSources(&out, origin)
	if out.Functions[0].Source == nil || out.Functions[0].Source.Path != "apps/api/handler.py" || out.Functions[0].Source.Line != 18 || out.Functions[1].Source != nil {
		t.Fatal("preserved Python implementation confused with generated adapter", out.Functions)
	}
}

func TestProfileComparisonSourceUsesEachRevisionAndHottestLine(t *testing.T) {
	now := time.Now()
	query := api.ProfileQuery{Runtime: "node24", Start: now.Add(-time.Minute), End: now}
	baseline := api.ProfileResponse{Query: query, Functions: []api.ProfileFunction{
		{Name: "hot", File: "/app/server.js", Line: 10, SelfCPUSeconds: 2},
		{Name: "hot", File: "/app/server.js", Line: 11, SelfCPUSeconds: 1},
		{Name: "removed", File: "/app/server.js", Line: 3, SelfCPUSeconds: 1},
	}}
	candidate := api.ProfileResponse{Query: query, Functions: []api.ProfileFunction{{Name: "hot", File: "/app/server.js", Line: 35, SelfCPUSeconds: 4}, {Name: "new", File: "/app/server.js", Line: 40, SelfCPUSeconds: 1}}}
	a, b := sourceOrigin(), sourceOrigin()
	b.CommitSHA = strings.Repeat("b", 40)
	b.SourceURL = "github://acme/service@" + b.CommitSHA
	LinkSources(&baseline, a)
	LinkSources(&candidate, b)
	result := Compare(baseline, candidate)
	for _, f := range result.Functions {
		switch f.Name {
		case "hot":
			if f.BaselineSource == nil || f.CandidateSource == nil || f.BaselineSource.Line != 10 || f.CandidateSource.Line != 35 || !strings.Contains(f.BaselineSource.URL, a.CommitSHA) || !strings.Contains(f.CandidateSource.URL, b.CommitSHA) {
				t.Fatal("moved function lost revision-specific sources", f)
			}
		case "removed":
			if f.BaselineSource == nil || f.CandidateSource != nil {
				t.Fatal("removed function linked to candidate", f)
			}
		case "new":
			if f.BaselineSource != nil || f.CandidateSource == nil {
				t.Fatal("new function linked to baseline", f)
			}
		}
	}
}
