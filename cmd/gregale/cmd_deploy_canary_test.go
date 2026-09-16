// cmd/gregale/cmd_deploy_canary_test.go — pins the CLI's
// canary-preset / canary-stages parsing contract (SAFE-RELEASES
// production-leveling Stream F). Lives alongside cmd_deploy_canary.go
// so the parser is unit-testable without spinning up the flag
// machinery.

package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api/canary"
)

// TestParseCanaryStages_HappyPath pins the basic comma + @ shape.
func TestParseCanaryStages_HappyPath(t *testing.T) {
	got, err := parseCanaryStages("1@30s,10@2m,100@0s")
	if err != nil {
		t.Fatalf("parseCanaryStages: %v", err)
	}
	want := []canary.CustomStage{
		{Percent: 1, Duration: "30s"},
		{Percent: 10, Duration: "2m"},
		{Percent: 100, Duration: "0s"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestParseCanaryStages_WhitespaceTolerance — "1@30s, 10@2m"
// parses identically to the tight form.
func TestParseCanaryStages_WhitespaceTolerance(t *testing.T) {
	got, err := parseCanaryStages("1@30s, 10@2m , 100@0s")
	if err != nil {
		t.Fatalf("parseCanaryStages: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[1].Percent != 10 || got[1].Duration != "2m" {
		t.Errorf("got[1] = %+v, want {10, 2m}", got[1])
	}
}

// TestParseCanaryStages_EmptyOK — empty input returns empty
// slice, nil error. Caller (buildCanarySpec) is responsible for
// rejecting empty stages when preset="custom".
func TestParseCanaryStages_EmptyOK(t *testing.T) {
	got, err := parseCanaryStages("")
	if err != nil {
		t.Fatalf("parseCanaryStages: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

// TestParseCanaryStages_BadShapes — every error path is a
// table-driven sub-case so a refactor that loosens validation is
// caught at unit-test time.
func TestParseCanaryStages_BadShapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // substring expected in the error message
	}{
		{"missing_at", "1_30s", "missing '@'"},
		{"missing_percent", "@30s", "missing percent"},
		{"missing_duration", "1@", "missing duration"},
		{"percent_non_int", "abc@30s", "percent"},
		{"percent_too_high", "150@30s", "out of [0,100]"},
		{"percent_negative", "-1@30s", "out of [0,100]"},
		{"empty_stage", "1@30s,,100@0s", "stage 1: empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseCanaryStages(c.in)
			if err == nil {
				t.Fatalf("parseCanaryStages(%q) accepted; want error", c.in)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want substring %q", err.Error(), c.want)
			}
		})
	}
}

// TestBuildCanarySpec_Empty — empty preset returns nil so the
// server applies the fast-default zero-value (no canary).
func TestBuildCanarySpec_Empty(t *testing.T) {
	got, err := buildCanarySpec("", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("buildCanarySpec(\"\", \"\") = %+v, want nil", got)
	}
}

// TestBuildCanarySpec_CatalogPreset — non-custom preset names
// just carry the name; Stages stays nil so the catalog lookup on
// the server side resolves the ladder.
func TestBuildCanarySpec_CatalogPreset(t *testing.T) {
	got, err := buildCanarySpec("balanced", "")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("got nil; want *CanaryPresetSpec")
	}
	if got.Preset != "balanced" {
		t.Errorf("Preset = %q, want balanced", got.Preset)
	}
	if len(got.Stages) != 0 {
		t.Errorf("Stages = %+v, want empty (catalog resolves)", got.Stages)
	}
}

// TestBuildCanarySpec_CustomHappyPath — preset=custom + a valid
// stages string → a fully populated spec ready for the wire.
func TestBuildCanarySpec_CustomHappyPath(t *testing.T) {
	got, err := buildCanarySpec("custom", "1@30s,10@2m,100@0s")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("got nil; want *CanaryPresetSpec")
	}
	if got.Preset != "custom" {
		t.Errorf("Preset = %q, want custom", got.Preset)
	}
	if len(got.Stages) != 3 {
		t.Fatalf("Stages len = %d, want 3", len(got.Stages))
	}
	if got.Stages[0].Percent != 1 || got.Stages[0].Duration != "30s" {
		t.Errorf("Stages[0] = %+v, want {1, 30s}", got.Stages[0])
	}
}

// adr: 122
func TestBuildCanarySpecRejectsEveryInvalidLadderClass(t *testing.T) {
	cases := []struct {
		name, preset, stages string
	}{
		{"unknown preset", "mystery", ""},
		{"stages without preset", "", "100@0s"},
		{"stages on catalog preset", "balanced", "100@0s"},
		{"custom without stages", "custom", ""},
		{"invalid duration", "custom", "1@never,100@0s"},
		{"non increasing", "custom", "10@30s,5@30s,100@0s"},
		{"invalid terminal", "custom", "1@30s,50@0s"},
		{"nonzero terminal duration", "custom", "1@30s,100@30s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildCanarySpec(tc.preset, tc.stages); err == nil {
				t.Fatalf("buildCanarySpec(%q, %q) accepted", tc.preset, tc.stages)
			}
		})
	}
}
