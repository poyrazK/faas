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
