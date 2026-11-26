// adr: 20261125231222 — the weekly awake email names the database, how long it
// was awake, the cause that started it most and what to do about it.
package mail_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/mail"
)

func TestManagedPostgresAwakeBodyV1(t *testing.T) {
	base := mail.ManagedPostgresAwakeNotice{DatabaseName: "orders", AwakeHoursPerDay: 9, Starts: 672, CauseStarts: 670}
	with := func(f func(*mail.ManagedPostgresAwakeNotice)) mail.ManagedPostgresAwakeNotice {
		n := base
		f(&n)
		return n
	}
	for _, tc := range []struct {
		name   string
		notice mail.ManagedPostgresAwakeNotice
		ok     bool
		want   []string
	}{
		{"cron", with(func(n *mail.ManagedPostgresAwakeNotice) {
			n.Cause, n.AppSlug, n.Detail = "cron", "api", "/cron/cleanup"
		}),
			true, []string{"awake 9 hours a day", "started 672 times", "The cron /cron/cleanup of api started it 670", "suspend-timeout set orders 1m", "gregale postgres activity orders --since 7d"}},
		{"wake with app", with(func(n *mail.ManagedPostgresAwakeNotice) { n.Cause = "app_wake" }),
			true, []string{"Wake-with-app started it", "app-wake set orders off"}},
		{"running app", with(func(n *mail.ManagedPostgresAwakeNotice) { n.Cause, n.AppSlug = "running_app", "worker" }),
			true, []string{"worker connected to it", "close idle pool connections"}},
		{"external", with(func(n *mail.ManagedPostgresAwakeNotice) { n.Cause = "external" }),
			true, []string{"A client outside Gregale", "rotate the database's credentials"}},
		{"requests are not noticed", with(func(n *mail.ManagedPostgresAwakeNotice) { n.Cause, n.AppSlug = "request", "api" }), false, nil},
		{"a cron path with markup", with(func(n *mail.ManagedPostgresAwakeNotice) {
			n.Cause, n.AppSlug, n.Detail = "cron", "api", "/x\r\nBcc: a@example.test"
		}), false, nil},
		{"a slug outside the app rule", with(func(n *mail.ManagedPostgresAwakeNotice) { n.Cause, n.AppSlug = "running_app", "<b>" }), false, nil},
		{"a name outside the database rule", with(func(n *mail.ManagedPostgresAwakeNotice) { n.Cause, n.DatabaseName = "external", "Orders DB" }), false, nil},
		{"more cause starts than starts", with(func(n *mail.ManagedPostgresAwakeNotice) { n.Cause, n.CauseStarts = "external", 700 }), false, nil},
		{"under an hour a day", with(func(n *mail.ManagedPostgresAwakeNotice) { n.Cause, n.AwakeHoursPerDay = "external", 0 }), false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject, body, ok := mail.ManagedPostgresAwakeBodyV1("owner@example.test", tc.notice)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if subject != "Your database orders was awake 9 hours a day" {
				t.Fatalf("subject = %q", subject)
			}
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("body lacks %q:\n%s", want, body)
				}
			}
		})
	}
}
