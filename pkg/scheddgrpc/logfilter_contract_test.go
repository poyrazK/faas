package scheddgrpc

import (
	"os"
	"strings"
	"testing"
)

// TestLogFilter_StructuredLevelIsASeverityFloor pins the documented
// GET /v1/apps/{slug}/logs `level` contract (#6667): structured info, warn
// and error events pass every floor at or below their own severity. The
// published OpenAPI once called this an exact match while the API (and the
// CLI and customer docs) used a threshold.
func TestLogFilter_StructuredLevelIsASeverityFloor(t *testing.T) {
	events := []string{"info", "warn", "error"}
	want := map[string][]string{
		"info":  {"info", "warn", "error"},
		"warn":  {"warn", "error"},
		"error": {"error"},
	}
	for floor, passing := range want {
		f, err := ParseLogFilter(floor, "")
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, level := range events {
			if f.MatchLineWithLevel(`{"level":"`+level+`","message":"gregale-qa"}`, level) {
				got = append(got, level)
			}
		}
		if strings.Join(got, ",") != strings.Join(passing, ",") {
			t.Errorf("level=%s passed %v, want %v", floor, got, passing)
		}
	}
}

// TestLogsLevelSpecDescribesTheFloor keeps the canonical spec in step with
// the behaviour pinned above.
func TestLogsLevelSpecDescribesTheFloor(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	start := strings.Index(spec, "  /v1/apps/{slug}/logs:")
	if start < 0 {
		t.Fatal("logs path missing from api/openapi.yaml")
	}
	section := spec[start:]
	level := strings.Index(section, "- name: level")
	if level < 0 {
		t.Fatal("level parameter missing from the logs operation")
	}
	desc := section[level:min(len(section), level+900)]
	if strings.Contains(desc, "Exact match") || !strings.Contains(desc, "this level or higher") {
		t.Fatalf("logs level description must describe a severity floor:\n%s", desc)
	}
}
