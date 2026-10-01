package deploydiff

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestComputeRegionPreviewIdentity(t *testing.T) {
	ptr := func(s string) *string { return &s }
	for _, tc := range []struct {
		name     string
		baseline *api.AppResponse
		request  *string
		region   string
		code     string
		added    bool
	}{
		{name: "legacy creation"},
		{name: "regional creation", request: ptr("eu-fsn"), region: "eu-fsn", added: true},
		{name: "preserve omission", baseline: &api.AppResponse{ComputeRegion: "eu-fsn"}, region: "eu-fsn"},
		{name: "same choice", baseline: &api.AppResponse{ComputeRegion: "eu-fsn"}, request: ptr("eu-fsn"), region: "eu-fsn"},
		{name: "repin refused", baseline: &api.AppResponse{ComputeRegion: "eu-fsn"}, request: ptr("us-east"), region: "eu-fsn", code: api.CodeConflict},
		{name: "clear refused", baseline: &api.AppResponse{ComputeRegion: "eu-fsn"}, request: ptr(""), region: "eu-fsn", code: api.CodeConflict},
		{name: "legacy repin refused", baseline: &api.AppResponse{}, request: ptr("eu-fsn"), code: api.CodeConflict},
		{name: "noncanonical refused", request: ptr("EU-fsn"), code: api.CodeValidation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diff := Compute("regional", api.PlanPro, Baseline{App: tc.baseline}, Pending{ComputeRegion: tc.request})
			if diff.ComputeRegion != tc.region || diff.HasBlockingBreaks() != (tc.code != "") {
				t.Fatalf("preview=%+v", diff)
			}
			if tc.code != "" && (len(diff.Breaks) != 1 || diff.Breaks[0].Code != tc.code || diff.Breaks[0].Field != "compute_region") {
				t.Fatalf("breaks=%+v", diff.Breaks)
			}
			if tc.added && (len(diff.Changes) != 1 || diff.Changes[0].Field != "compute_region" || diff.Changes[0].Kind != ChangeAdd) {
				t.Fatalf("changes=%+v", diff.Changes)
			}
			var out bytes.Buffer
			if err := RenderJSON(&out, diff); err != nil {
				t.Fatal(err)
			}
			var rendered api.DiffResponse
			if err := json.Unmarshal(out.Bytes(), &rendered); err != nil {
				t.Fatal(err)
			}
			wire := diff.ToWire()
			if rendered.Diff.ComputeRegion != wire.Diff.ComputeRegion || rendered.Blocking != wire.Blocking {
				t.Fatalf("local/server wire differs: %+v %+v", rendered, wire)
			}
			out.Reset()
			RenderText(&out, diff)
			if tc.region != "" && !strings.Contains(out.String(), "Compute region: "+tc.region+" (immutable)") {
				t.Fatalf("missing effective region: %s", out.String())
			}
		})
	}
}
