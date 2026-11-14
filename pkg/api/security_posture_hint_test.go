package api

import (
	"strings"
	"testing"
)

// Hunt 32 H32-82: the security-enforce refusal pointed CLI users at
// GET /v1/apps/{slug}/security instead of the command that shows it.
func TestSecurityPostureBlockedHintNamesTheCommand(t *testing.T) {
	p := ErrSecurityPostureBlocked("shop", "public_debug_endpoint")
	if !strings.Contains(p.Hint, "gregale app shop security") {
		t.Fatalf("hint = %q", p.Hint)
	}
}
