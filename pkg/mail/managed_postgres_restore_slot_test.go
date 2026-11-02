// adr: 20261031123259 — the restore slot emails name the copy, when its slot
// ends and how to keep or recover it.
package mail_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/mail"
)

func TestManagedPostgresRestoreSlotBodyV1(t *testing.T) {
	until := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		notice  mail.ManagedPostgresRestoreSlotNotice
		ok      bool
		subject string
		want    []string
	}{
		{"ending", mail.ManagedPostgresRestoreSlotNotice{Kind: mail.ManagedPostgresRestoreSlotEnding, DatabaseName: "orders-copy", SlotUntil: until},
			true, "orders-copy will be deleted on 2026-11-07", []string{"lasts until 2026-11-07 12:00 UTC", "delete another database or upgrade your plan", "recover it for 7 days"}},
		{"expired", mail.ManagedPostgresRestoreSlotNotice{Kind: mail.ManagedPostgresRestoreSlotExpired, DatabaseName: "orders-copy", SlotUntil: until, RestorableUntil: until.Add(7 * 24 * time.Hour)},
			true, "orders-copy is scheduled for deletion", []string{"ended at 2026-11-07 12:00 UTC", "bindings were removed", "before 2026-11-14 12:00 UTC", "gregale postgres recover orders-copy"}},
		{"expired without a recovery deadline", mail.ManagedPostgresRestoreSlotNotice{Kind: mail.ManagedPostgresRestoreSlotExpired, DatabaseName: "orders-copy", SlotUntil: until}, false, "", nil},
		{"ending with a recovery deadline", mail.ManagedPostgresRestoreSlotNotice{Kind: mail.ManagedPostgresRestoreSlotEnding, DatabaseName: "orders-copy", SlotUntil: until, RestorableUntil: until}, false, "", nil},
		{"a name outside the database rule", mail.ManagedPostgresRestoreSlotNotice{Kind: mail.ManagedPostgresRestoreSlotEnding, DatabaseName: "orders\r\nBcc: x@example.test", SlotUntil: until}, false, "", nil},
		{"an unknown kind", mail.ManagedPostgresRestoreSlotNotice{Kind: "released", DatabaseName: "orders-copy", SlotUntil: until}, false, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject, body, ok := mail.ManagedPostgresRestoreSlotBodyV1("owner@example.test", tc.notice)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if !strings.Contains(subject, tc.subject) || strings.ContainsAny(subject, "\r\n") {
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
