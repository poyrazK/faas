package preflight

import (
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
)

// A source tree that already satisfies the container contract — a detected
// framework, a real start command, and a port — must come back green with
// nothing for the operator to fix.
func TestEvaluate_CleanExpressProfileIsGreen(t *testing.T) {
	profile := frameworkprofile.Profile{
		Version:      frameworkprofile.Version,
		Framework:    "express",
		StartCommand: "npm run start",
		Port:         3000,
		HealthPath:   "/healthz",
		Inferred:     true,
	}

	verdict := Evaluate(profile)

	if verdict.Level != LevelGreen {
		t.Fatalf("Level = %q, want %q (findings: %+v)", verdict.Level, LevelGreen, verdict.Findings)
	}
	if len(verdict.Findings) != 0 {
		t.Fatalf("Findings = %+v, want none", verdict.Findings)
	}
}

// frameworkprofile reports an un-inferrable entrypoint as a warning. Preflight
// must surface that as amber with a remedy, never as green — the deploy would
// fail and the point of the check is to say so first.
func TestEvaluate_MissingStartCommandIsAmber(t *testing.T) {
	profile := frameworkprofile.Profile{
		Version:   frameworkprofile.Version,
		Framework: "node",
		Port:      3000,
		Warnings: []frameworkprofile.Warning{{
			Code:    "missing_start_command",
			Message: "No npm start script or conventional Node entrypoint was found; add scripts.start or configure an explicit command.",
			Sources: []string{"package.json"},
		}},
	}

	verdict := Evaluate(profile)

	if verdict.Level != LevelAmber {
		t.Fatalf("Level = %q, want %q", verdict.Level, LevelAmber)
	}
	var found *Finding
	for i := range verdict.Findings {
		if verdict.Findings[i].Code == "missing_start_command" {
			found = &verdict.Findings[i]
		}
	}
	if found == nil {
		t.Fatalf("no missing_start_command finding, got %+v", verdict.Findings)
	}
	if found.Remedy == "" {
		t.Error("finding has no remedy; a preflight finding must say what to do")
	}
}

// Inference emits one warning per file, so a real repository produces the same
// code a dozen times over. A report a stranger reads must state each problem
// once, with the files gathered under it.
func TestEvaluate_CollapsesRepeatedWarningsIntoOneFinding(t *testing.T) {
	profile := frameworkprofile.Profile{
		Version:      frameworkprofile.Version,
		Framework:    "express",
		StartCommand: "node index.js",
		Port:         3000,
	}
	for _, path := range []string{"a.js", "b.js", "c.js"} {
		profile.Warnings = append(profile.Warnings, frameworkprofile.Warning{
			Code:    "loopback_bind_possible",
			Message: "The source references localhost or 127.0.0.1; bind to 0.0.0.0 for public API traffic.",
			Sources: []string{path},
		})
	}

	verdict := Evaluate(profile)

	if len(verdict.Findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(verdict.Findings), verdict.Findings)
	}
	if got := verdict.Findings[0].Sources; len(got) != 3 {
		t.Errorf("Sources = %v, want all three files gathered under one finding", got)
	}
}

// A repository that references localhost everywhere must not turn the report
// into a wall of paths.
func TestEvaluate_CapsSourcesOnACollapsedFinding(t *testing.T) {
	profile := frameworkprofile.Profile{Version: frameworkprofile.Version, Framework: "express"}
	for i := 0; i < 200; i++ {
		profile.Warnings = append(profile.Warnings, frameworkprofile.Warning{
			Code:    "loopback_bind_possible",
			Message: "localhost",
			Sources: []string{fmt.Sprintf("file%03d.js", i)},
		})
	}

	verdict := Evaluate(profile)

	if len(verdict.Findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(verdict.Findings))
	}
	if got := len(verdict.Findings[0].Sources); got > maxFindingSources {
		t.Errorf("Sources length = %d, want at most %d", got, maxFindingSources)
	}
}
