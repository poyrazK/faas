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
