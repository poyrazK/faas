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

// GREG-01: a Compose start_period longer than the startup deadline is a
// grace period the service never gets; the plan says so and names the fix.
func TestComposeStartPeriodBeyondStartupDeadlineWarns(t *testing.T) {
	got := startupWarnings(t, "60s")
	if len(got) != 1 {
		t.Fatalf("start_period warnings = %q, want one", got)
	}
	for _, want := range []string{
		"orders: healthcheck start_period 1m0s is longer than the 30 s default startup deadline",
		"cold_boot_timeout",
		"lifecycle.startup_deadline_s: 60",
		"scale 120 s",
	} {
		if !strings.Contains(got[0], want) {
			t.Errorf("warning %q lacks %q", got[0], want)
		}
	}
}

func TestComposeStartPeriodWithinStartupDeadlineIsQuiet(t *testing.T) {
	if got := startupWarnings(t, "20s"); len(got) != 0 {
		t.Fatalf("warnings = %q, want none for a start_period inside the default deadline", got)
	}
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
