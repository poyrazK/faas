package mail_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/mail"
)

// The new account security notices name the account, mask the other
// address and say how to stop or undo the change (ADR 20261110100000).
func TestAccountSecurityNotices(t *testing.T) {
	at := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	cases := []struct {
		name       string
		body       func() (string, string)
		want, deny []string
	}{
		{"change requested", func() (string, string) {
			return mail.EmailChangeRequestedBody("owner@example.com", "taker@example.net", at, at.Add(time.Hour))
		}, []string{"owner@example.com", "t***@example.net", "Sign out", "Forgot password", "2026-11-10 10:30 UTC"}, []string{"taker@example.net"}},
		{"provider removed", func() (string, string) {
			return mail.SignInMethodRemovedBody("owner@example.com", "github", at)
		}, []string{"GitHub", "owner@example.com", "Forgot password"}, nil},
		{"operator revert", func() (string, string) {
			return mail.OperatorEmailRevertedBody("owner@example.com", "taker@example.net", at)
		}, []string{"owner@example.com", "t***@example.net", "Forgot password"}, []string{"taker@example.net"}},
		{"email changed", func() (string, string) {
			return mail.EmailChangedNoticeBody("owner@example.com", "taker@example.net", at)
		}, []string{"within 30 days", "move the account back"}, nil},
	}
	for _, tc := range cases {
		subject, body := tc.body()
		if subject == "" {
			t.Errorf("%s: empty subject", tc.name)
		}
		for _, w := range tc.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: body lacks %q:\n%s", tc.name, w, body)
			}
		}
		for _, d := range tc.deny {
			if strings.Contains(body, d) {
				t.Errorf("%s: body shows %q unmasked", tc.name, d)
			}
		}
	}
}
