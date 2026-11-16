package gateway

import (
	"strings"
	"testing"
)

// TestMissingBearerDetailNamesConsumerKeys pins that a consumer key presented
// to a platform bearer gate is told why it fails and what to change.
func TestMissingBearerDetailNamesConsumerKeys(t *testing.T) {
	if got := missingBearerDetail("ck_0123456789abcdef"); !strings.Contains(got, "consumer keys") || !strings.Contains(got, "--consumer-auth-mode required") {
		t.Fatalf("ck_ detail = %q", got)
	}
	for _, tok := range []string{"", "not-a-key"} {
		if got := missingBearerDetail(tok); !strings.Contains(got, "Authorization: Bearer <token>") {
			t.Fatalf("detail(%q) = %q", tok, got)
		}
	}
}
