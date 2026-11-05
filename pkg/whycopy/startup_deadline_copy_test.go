package whycopy

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// H10-86: the startup-timeout explanation told customers to raise
// startup_timeout_s, the internal column; they set the deadline as
// startup_deadline_s in gregale.yaml or --startup-deadline-s on deploy.
func TestStartupTimeoutCopyNamesCustomerSetting(t *testing.T) {
	entry, ok := Lookup(api.CodeAppStartupTimeout)
	if !ok {
		t.Fatal("no copy for app_startup_timeout")
	}
	text := entry.Why + "\n" + entry.Fix
	if strings.Contains(text, "startup_timeout_s") {
		t.Fatalf("copy names the internal column: %q", text)
	}
	for _, want := range []string{"startup_deadline_s", "--startup-deadline-s"} {
		if !strings.Contains(text, want) {
			t.Fatalf("copy does not name %s: %q", want, text)
		}
	}
}

// Hunt 14 H14-4: H12-10 capped startup deadlines at the 30 s cold-boot window,
// but every startup-timeout explanation still told the customer to raise the
// deadline, which the platform refuses above 30 s.
func TestStartupGuidanceStatesTheColdBootCap(t *testing.T) {
	entry, _ := Lookup(api.CodeAppStartupTimeout)
	fixes := []string{entry.Fix}
	for _, phase := range []string{"startup_phase=guest_startup", "startup_phase=handler_healthcheck", "startup_phase=image_healthcheck"} {
		_, fix := startupFailureGuidance(phase)
		fixes = append(fixes, fix)
	}
	for i, fix := range fixes {
		if fix == "" {
			continue
		}
		if strings.Contains(fix, "raise the startup deadline") || !strings.Contains(fix, "30 s") {
			t.Errorf("guidance %d does not state the 30 s cap: %q", i, fix)
		}
	}
}
