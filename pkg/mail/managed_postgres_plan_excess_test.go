// adr: 20261102125208 — the plan excess email names the suspended database,
// why, and how to resume it.
package mail_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/mail"
)

func TestManagedPostgresPlanExcessBodyV1(t *testing.T) {
	for _, tc := range []struct {
		name   string
		notice mail.ManagedPostgresPlanExcessNotice
		ok     bool
		want   []string
	}{
		{"database limit", mail.ManagedPostgresPlanExcessNotice{Reason: mail.ManagedPostgresPlanExcessDatabaseLimit, DatabaseName: "events", ServiceClass: "development"},
			true, []string{"fewer databases than you have", "events, one of your newest, is suspended", "delete another database", "data, credentials and app bindings are kept"}},
		{"service class", mail.ManagedPostgresPlanExcessNotice{Reason: mail.ManagedPostgresPlanExcessServiceClass, DatabaseName: "analytics", ServiceClass: "production"},
			true, []string{"no longer includes the production class", "analytics is suspended", "gregale postgres resize"}},
		{"a name outside the database rule", mail.ManagedPostgresPlanExcessNotice{Reason: mail.ManagedPostgresPlanExcessDatabaseLimit, DatabaseName: "events\r\nBcc: x@example.test", ServiceClass: "development"}, false, nil},
		{"an unknown class", mail.ManagedPostgresPlanExcessNotice{Reason: mail.ManagedPostgresPlanExcessServiceClass, DatabaseName: "events", ServiceClass: "<b>"}, false, nil},
		{"an unknown reason", mail.ManagedPostgresPlanExcessNotice{Reason: "storage", DatabaseName: "events", ServiceClass: "development"}, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject, body, ok := mail.ManagedPostgresPlanExcessBodyV1("owner@example.test", tc.notice)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if subject != "Your database "+tc.notice.DatabaseName+" is suspended" {
				t.Fatalf("subject = %q", subject)
			}
			for _, want := range append(tc.want, "owner@example.test") {
				if !strings.Contains(body, want) {
					t.Fatalf("body lacks %q:\n%s", want, body)
				}
			}
		})
	}
}
