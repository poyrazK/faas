package issues

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func sourceMapFixture() api.PublishIssueSourceMapsRequest {
	return api.PublishIssueSourceMapsRequest{Directory: "/app/dist", Manifest: api.IssueSourceMapManifest{Version: 1, Artifacts: []api.IssueSourceMapArtifact{{GeneratedFile: "bundle.js", SHA256: strings.Repeat("a", 64), Payload: `{"version":3,"sourceRoot":"../","sources":["src/checkout.ts"],"names":["charge","refund"],"mappings":"AASAA,K,KAACC","sourcesContent":["PRIVATE_SOURCE"],"privateExtension":"PRIVATE_EXTENSION"}`}}}}
}

func TestSymbolicationDoesNotRejectRedactionExpansion(t *testing.T) {
	set, err := CompileSourceMaps(sourceMapFixture())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	in := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: strings.Repeat("a@b.co ", 30), GroupingStrategy: FailureSiteStrategy, SymbolicationStrategy: DeploymentSourceMapStrategy, Frames: []api.IssueFrame{{File: "/app/dist/bundle.js", Function: "a", Line: 1, Column: 1, InApp: true}}}
	original, _, _, err := Normalize(in, now, api.PlanPro.IssueLimits())
	if err != nil || len(original.ExceptionType) <= api.IssueMaxTypeBytes {
		t.Fatalf("redaction fixture: len=%d err=%v", len(original.ExceptionType), err)
	}
	mapped, _ := set.Symbolicate(original)
	got, fp, _, err := NormalizeSymbolicated(original, mapped)
	if err != nil || fp == "" || got.ExceptionType != original.ExceptionType || got.Frames[0].Function != "charge" {
		t.Fatalf("symbolicated event: %+v %v", got, err)
	}
	mapped.Frames[0].Function = "alice@example.com"
	got, _, _, err = NormalizeSymbolicated(original, mapped)
	if err != nil || strings.Contains(got.Frames[0].Function, "alice@example.com") {
		t.Fatalf("mapped frame was not sanitized: %+v %v", got.Frames[0], err)
	}
}

func TestSourceMapCacheReusesAndEvicts(t *testing.T) {
	cache := NewSourceMapCache(1)
	raw, err := json.Marshal(sourceMapFixture())
	if err != nil {
		t.Fatal(err)
	}
	first, err := cache.Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := cache.Compile(raw)
	if err != nil || again != first {
		t.Fatal("cache did not reuse compiled map")
	}
	cache.Remove(raw)
	after, err := cache.Compile(raw)
	if err != nil || after == first {
		t.Fatal("retired map remained cached")
	}
	other := sourceMapFixture()
	other.Directory = "/app/other"
	otherRaw, _ := json.Marshal(other)
	if _, err := cache.Compile(otherRaw); err != nil {
		t.Fatal(err)
	}
	afterEviction, err := cache.Compile(raw)
	if err != nil || afterEviction == after {
		t.Fatal("cache did not evict oldest map")
	}
}
func TestDeploymentSourceMapsCanonicalizationAndCoordinates(t *testing.T) {
	set, err := CompileSourceMaps(sourceMapFixture())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(set.Request)
	if strings.Contains(string(raw), "PRIVATE_") {
		t.Fatal("source contents or extensions persisted")
	}
	again, err := CompileSourceMaps(set.Request)
	if err != nil || again.Digest != set.Digest {
		t.Fatalf("canonical round trip: %v", err)
	}
	frames := []api.IssueFrame{}
	for _, column := range []int{1, 6, 10, 11, 0} {
		frames = append(frames, api.IssueFrame{File: "file:///app/dist/bundle.js", Function: "a", Line: 1, Column: column, InApp: true})
	}
	event, result := set.Symbolicate(api.IssueEvent{Frames: frames, Causes: []api.IssueCause{{Frames: []api.IssueFrame{{File: "/app/dist/bundle.js", Line: 2, Column: 1, InApp: true}}}}})
	if event.Frames[0].File != "/app/src/checkout.ts" || event.Frames[0].Function != "charge" || event.Frames[0].Line != 10 || event.Frames[0].Column != 1 {
		t.Fatalf("mapped frame: %+v", event.Frames[0])
	}
	if event.Frames[3].Function != "refund" {
		t.Fatal("wrong nearest mapped segment")
	}
	for _, i := range []int{1, 2, 4} {
		if event.Frames[i] != frames[i] {
			t.Fatal("unmapped region changed")
		}
	}
	if result.MappedFrames != 2 || result.UnmappedFrames != 4 || frames[0].Function != "a" {
		t.Fatalf("counts or input mutation: %+v", result)
	}
}
func TestDeploymentSourceMapsRejectMalformedAndAmplifyingInputs(t *testing.T) {
	for _, mapping := range []string{"!", "g", "ggggggggA", "AA", "AAAA,", ",AAAA", "AAAA,,AAAA", "ACAAA", "AAAAE", "ADAA", strings.Repeat(";", api.IssueSourceMapEntriesMax)} {
		in := sourceMapFixture()
		in.Manifest.Artifacts[0].Payload = `{"version":3,"sources":["src.ts"],"names":["fn"],"mappings":"` + mapping + `"}`
		if _, err := CompileSourceMaps(in); err == nil {
			t.Fatalf("accepted invalid mapping %.20q", mapping)
		}
	}
	for _, payload := range []string{`null`, `{"version":3,"sections":[]}`, `{"version":3,"sources":[],"names":[],"mappings":"AAAA"}`, strings.Repeat("x", api.IssueSourceMapMaxBytes+1)} {
		in := sourceMapFixture()
		in.Manifest.Artifacts[0].Payload = payload
		if _, err := CompileSourceMaps(in); err == nil {
			t.Fatal("accepted malformed map")
		}
	}
	for _, file := range []string{"../outside.js", "/app/a.js", "dist/../a.js", "https://example.com/a.js", "a\\b.js"} {
		in := sourceMapFixture()
		in.Manifest.Artifacts[0].GeneratedFile = file
		if _, err := CompileSourceMaps(in); err == nil {
			t.Fatal("accepted unsafe manifest path")
		}
	}
	in := sourceMapFixture()
	in.Manifest.Artifacts = append(in.Manifest.Artifacts, in.Manifest.Artifacts[0])
	if _, err := CompileSourceMaps(in); err == nil {
		t.Fatal("accepted ambiguous bundle names")
	}
}
func TestDeploymentSourceMapsPreserveGroupingAndRawStack(t *testing.T) {
	set, err := CompileSourceMaps(sourceMapFixture())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	in := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", Message: "payment failed", GroupingStrategy: FailureSiteStrategy, SymbolicationStrategy: DeploymentSourceMapStrategy, StackTrace: "Error: payment failed\n    at a (/app/dist/bundle.js:1:1)"}
	event, _, _, err := Normalize(in, now, api.PlanPro.IssueLimits())
	if err != nil {
		t.Fatal(err)
	}
	if event.Frames[0].Column != 1 {
		t.Fatal("Node column not extracted")
	}
	mapped, _ := set.Symbolicate(event)
	mapped, fp, _, err := Normalize(mapped, now, api.PlanPro.IssueLimits())
	if err != nil {
		t.Fatal(err)
	}
	if mapped.StackTrace != in.StackTrace {
		t.Fatal("raw evidence changed")
	}
	mapped.Frames[0].Line++
	mapped.Frames[0].Column++
	_, same, _, err := Normalize(mapped, now, api.PlanPro.IssueLimits())
	if err != nil || same != fp {
		t.Fatal("line/column changed group identity")
	}
	in.SymbolicationStrategy = ""
	legacy, _, _, err := Normalize(in, now, api.PlanPro.IssueLimits())
	if err != nil || legacy.Frames[0].Column != 0 {
		t.Fatal("existing capture envelope changed")
	}
	in.SymbolicationStrategy = DeploymentSourceMapStrategy
	in.GroupingStrategy = "legacy"
	if _, _, _, err = Normalize(in, now, api.PlanPro.IssueLimits()); err == nil {
		t.Fatal("legacy grouping silently symbolicated")
	}
	in.GroupingStrategy = FailureSiteStrategy
	in.Symbolication = &api.IssueSymbolicationResult{}
	if _, _, _, err = Normalize(in, now, api.PlanPro.IssueLimits()); err == nil {
		t.Fatal("accepted forged symbolication evidence")
	}
}

func TestSymbolicationBoundsRedactedMappedFrames(t *testing.T) {
	now := time.Now().UTC()
	frame := api.IssueFrame{File: "/app/dist/bundle.js", Function: "x", Line: 1, Column: 1, InApp: true}
	frames := make([]api.IssueFrame, 32)
	for i := range frames {
		frames[i] = frame
	}
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", GroupingStrategy: CauseStrategy, SymbolicationStrategy: DeploymentSourceMapStrategy, Frames: frames}
	for i := 0; i < 8; i++ {
		event.Causes = append(event.Causes, api.IssueCause{ExceptionType: "Error", Frames: frames})
	}
	original, _, _, err := Normalize(event, now, api.PlanPro.IssueLimits())
	if err != nil {
		t.Fatal(err)
	}
	mapped := original
	mapped.Frames = append([]api.IssueFrame(nil), original.Frames...)
	mapped.Causes = append([]api.IssueCause(nil), original.Causes...)
	for i := range mapped.Causes {
		mapped.Causes[i].Frames = append([]api.IssueFrame(nil), original.Causes[i].Frames...)
	}
	rewrite := func(frames []api.IssueFrame) {
		for i := range frames {
			frames[i].File = strings.Repeat("a@b.co ", 60)
			frames[i].Function = strings.Repeat("a@b.co ", 70)
		}
	}
	rewrite(mapped.Frames)
	for i := range mapped.Causes {
		rewrite(mapped.Causes[i].Frames)
	}
	out, fp, _, err := NormalizeSymbolicated(original, mapped)
	if err != nil || fp == "" {
		t.Fatalf("symbolication: %v", err)
	}
	payload, err := json.Marshal(out)
	if err != nil || len(payload) > 512<<10 {
		t.Fatalf("symbolicated payload size=%d error=%v", len(payload), err)
	}
	for _, f := range out.Frames {
		if len(f.File) > api.IssueMaxFrameBytes || len(f.Function) > api.IssueMaxFrameBytes {
			t.Fatalf("mapped frame exceeded bound: %+v", f)
		}
	}
}

func TestMappedFrameJSONLimit(t *testing.T) {
	for _, value := range []string{strings.Repeat("\x01", 512), strings.Repeat("<&\u2028", 170), strings.Repeat("a@b.co ", 70)} {
		out := boundedMappedFrame(value)
		raw, err := json.Marshal(out)
		if err != nil || len(raw) > api.IssueMaxFrameBytes {
			t.Fatalf("encoded mapped frame size %d: %v", len(raw), err)
		}
	}
}

func TestSourceMapDeploymentCacheIsBoundedAndScoped(t *testing.T) {
	set, err := CompileSourceMaps(sourceMapFixture())
	if err != nil {
		t.Fatal(err)
	}
	cache := NewSourceMapCache(1)
	cache.PutDeployment("app-a:dep-a:"+set.Digest, set)
	if cache.GetDeployment("app-a:dep-a:"+set.Digest) != set || cache.GetDeployment("app-b:dep-a:"+set.Digest) != nil {
		t.Fatal("deployment cache key is not scoped")
	}
	cache.PutDeployment("app-b:dep-b:"+set.Digest, set)
	if cache.GetDeployment("app-a:dep-a:"+set.Digest) != nil {
		t.Fatal("deployment cache did not evict")
	}
	cache.RemoveDeployment("app-b:dep-b:" + set.Digest)
	if cache.GetDeployment("app-b:dep-b:"+set.Digest) != nil {
		t.Fatal("retired deployment remains cached")
	}
}

func TestSourceMapDeploymentCacheHandlesWorkingSetAndByteLimit(t *testing.T) {
	set, err := CompileSourceMaps(sourceMapFixture())
	if err != nil {
		t.Fatal(err)
	}
	weight := set.retainedBytes()
	cache := NewSourceMapCacheWithBudget(32, weight*5)
	for i := 0; i < 5; i++ {
		cache.PutDeployment(string(rune('a'+i)), set)
	}
	for i := 0; i < 25; i++ {
		if cache.GetDeployment(string(rune('a'+i%5))) != set {
			t.Fatalf("working set missed at access %d", i)
		}
	}
	cache.PutDeployment("sixth", set)
	if cache.bytes > weight*5 || cache.GetDeployment("a") != nil || cache.GetDeployment("sixth") != set {
		t.Fatalf("byte budget not enforced: retained=%d budget=%d", cache.bytes, weight*5)
	}
	tooSmall := NewSourceMapCacheWithBudget(32, weight-1)
	tooSmall.PutDeployment("large", set)
	if tooSmall.GetDeployment("large") != nil || tooSmall.bytes != 0 {
		t.Fatal("oversized set was retained")
	}
}
