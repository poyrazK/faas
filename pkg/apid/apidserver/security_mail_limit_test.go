package apidserver_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Reset, magic-link and verification mail were limited per client IP only,
// so many addresses could mail-bomb one customer (bug hunt 2026-10-27 H14).
func TestSecurityMailIsCappedPerAccount(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	allow := apidserver.SecurityMailAllow(func() time.Time { return now })
	if !allow("acct", "password_reset") {
		t.Fatal("first reset mail refused")
	}
	if allow("acct", "password_reset") {
		t.Fatal("a second reset mail within the interval was sent")
	}
	if !allow("acct", "magic_link") {
		t.Fatal("a magic link right after a reset mail was refused; kinds are counted apart")
	}
	if !allow("other", "password_reset") {
		t.Fatal("another account was throttled")
	}
	sent := 1
	for range 10 {
		now = now.Add(api.SecurityMailPerAccountInterval)
		if allow("acct", "password_reset") {
			sent++
		}
	}
	if sent != api.SecurityMailPerAccountPerHour {
		t.Fatalf("reset mails in an hour = %d, want %d", sent, api.SecurityMailPerAccountPerHour)
	}
	now = now.Add(time.Hour)
	if !allow("acct", "password_reset") {
		t.Fatal("mail still refused an hour later")
	}
}
