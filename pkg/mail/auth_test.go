package mail

import (
	"strings"
	"testing"
	"time"
)

func TestEmailVerificationBody(t *testing.T) {
	expires := time.Date(2026, 9, 6, 12, 30, 0, 0, time.UTC)
	subject, body := EmailVerificationBody("alice@example.com", "https://gregale.dev/v1/auth/verify-email?token=abc", expires)
	if subject != "Verify your Gregale email" {
		t.Fatalf("subject = %q", subject)
	}
	for _, want := range []string{"alice@example.com", "token=abc", "2026-09-06 12:30 UTC", "used once"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}
