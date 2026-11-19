package reposcan

import (
	"strings"
	"testing"
)

const startupServiceCompose = "services:\n  orders:\n    image: nginx:1.27\n    ports: [8080]\n    healthcheck:\n      test: [\"CMD\", \"curl\", \"-fsS\", \"http://localhost:8080/actuator/health\"]\n      start_period: %s\n"

func startupWarnings(t *testing.T, startPeriod string, files ...string) []string {
	t.Helper()
	result, err := scanCompose(t, strings.Replace(startupServiceCompose, "%s", startPeriod, 1), files...)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, w := range result.Warnings {
		if strings.Contains(w, "start_period") {
			out = append(out, w)
		}
	}
	return out
}

// GREG-01, then retail-store A5: without gregale.yaml the healthcheck timing
// raises the service's own startup deadline, and the plan says to what.
func TestComposeStartPeriodRaisesStartupDeadline(t *testing.T) {
	got := startupWarnings(t, "60s")
	if len(got) != 1 {
		t.Fatalf("start_period notes = %q, want one", got)
	}
	for _, want := range []string{
		"orders: startup deadline raised to 120 s from the healthcheck (start_period 1m0s + 3 retries × 30s)",
		"scale 120 s",
		"x-gregale-startup-deadline",
	} {
		if !strings.Contains(got[0], want) {
			t.Errorf("note %q lacks %q", got[0], want)
		}
	}
}

func TestComposeStartPeriodWithinConfiguredDeadlineIsQuiet(t *testing.T) {
	if got := startupWarnings(t, "60s", "gregale.yaml", "lifecycle:\n  startup_deadline_s: 120\n"); len(got) != 0 {
		t.Fatalf("warnings = %q, want none once gregale.yaml raises the deadline", got)
	}
}

func TestComposeStartPeriodBeyondConfiguredDeadlineWarns(t *testing.T) {
	got := startupWarnings(t, "90s", "gregale.yaml", "lifecycle:\n  startup_deadline_s: 45\n")
	if len(got) != 1 || !strings.Contains(got[0], "longer than the 45 s lifecycle.startup_deadline_s") {
		t.Fatalf("warnings = %q", got)
	}
}
