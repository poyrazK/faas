package agentd

// adr: 20261124075005 §2 — a desktop session's start and ready commands are
// agentd's constants at the session's resolution, and only an image session
// with a well-formed resolution gets them.

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

func TestDesktopCommands(t *testing.T) {
	for _, tc := range []struct {
		name  string
		claim agentstore.SandboxClaim
		start string
		ok    bool
	}{
		{"desktop", agentstore.SandboxClaim{ImageKey: "images/desktop", DesktopResolution: "1280x800"},
			"gregale-desktop start --resolution 1280x800", true},
		{"not a desktop", agentstore.SandboxClaim{ImageKey: "images/desktop"}, "", false},
		{"no image", agentstore.SandboxClaim{DesktopResolution: "1280x800"}, "", false},
		{"shell text", agentstore.SandboxClaim{ImageKey: "images/desktop", DesktopResolution: "1280x800; reboot"}, "", false},
	} {
		start, ready, timeout, ok := desktopCommands(tc.claim)
		if ok != tc.ok || start != tc.start {
			t.Errorf("%s: start %q ok %v, want %q %v", tc.name, start, ok, tc.start, tc.ok)
			continue
		}
		if ok && (ready != "gregale-desktop wait" || timeout != time.Duration(api.SandboxDesktopReadyTimeoutSec)*time.Second) {
			t.Errorf("%s: ready %q within %s", tc.name, ready, timeout)
		}
	}
}
