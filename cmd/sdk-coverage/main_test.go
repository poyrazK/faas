package main

import "testing"

func TestDevSessionAliasRetainsWireCoverage(t *testing.T) {
	spec := map[string]map[string]any{"/v1/dev/sessions/{project}": {"put": true}}
	methods := map[string]bool{"PutDevSessionsProject": true, "UpsertDevSession": true}
	if report := analyze(spec, methods); len(report.missing) != 0 || len(report.warn) != 0 {
		t.Fatalf("documented alias and wire method: %+v", report)
	}
	delete(methods, "PutDevSessionsProject")
	if report := analyze(spec, methods); len(report.missing) != 1 {
		t.Fatalf("alias hid missing wire method: %+v", report)
	}
	if report := analyze(map[string]map[string]any{}, methods); len(report.warn) != 1 {
		t.Fatalf("alias hid removed spec route: %+v", report)
	}
}
