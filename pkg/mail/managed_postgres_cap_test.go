// adr: 20261025094915 — the compute cap emails say what was used, what
// happens next and when the databases resume, in compute-unit hours.
package mail_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/mail"
)

func TestManagedPostgresCapBodyV1(t *testing.T) {
	reached := time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC)
	base := mail.ManagedPostgresCapNotice{UsedSeconds: 72000, Allowance: 90000, Ceiling: 90000, ReachedAt: reached}
	for _, tc := range []struct {
		name    string
		notice  func(mail.ManagedPostgresCapNotice) mail.ManagedPostgresCapNotice
		subject string
		want    []string
		absent  []string
	}{
		{"warning without overage", func(n mail.ManagedPostgresCapNotice) mail.ManagedPostgresCapNotice {
			n.Threshold = mail.ManagedPostgresCapWarning
			return n
		}, "80% of this month's compute", []string{"20.0 compute-unit hours of this month's 25.0", "suspended until 2026-11-01", "turn on compute overage"}, nil},
		{"warning with overage", func(n mail.ManagedPostgresCapNotice) mail.ManagedPostgresCapNotice {
			n.Threshold, n.Overage, n.Ceiling = mail.ManagedPostgresCapWarning, true, 180000
			return n
		}, "80% of this month's compute", []string{"keep running until 50.0 compute-unit hours"}, nil},
		{"past the allowance with overage", func(n mail.ManagedPostgresCapNotice) mail.ManagedPostgresCapNotice {
			n.Threshold, n.Overage, n.Ceiling, n.UsedSeconds = mail.ManagedPostgresCapAllowance, true, 180000, 90000
			return n
		}, "past this month's compute allowance", []string{"not billed yet", "until 50.0 compute-unit hours"}, nil},
		{"suspended", func(n mail.ManagedPostgresCapNotice) mail.ManagedPostgresCapNotice {
			n.Threshold, n.UsedSeconds = mail.ManagedPostgresCapSuspended, 90000
			return n
		}, "suspended until next month", []string{"2026-10-20 09:30 UTC", "data, backups and connection settings are kept", "resume on 2026-11-01"}, []string{"deleted"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject, body, ok := mail.ManagedPostgresCapBodyV1("owner@example.com", tc.notice(base))
			if !ok || !strings.Contains(subject, tc.subject) {
				t.Fatalf("subject %q ok %v; want %q", subject, ok, tc.subject)
			}
			for _, want := range append(tc.want, "owner@example.com", "gregale postgres usage") {
				if !strings.Contains(body, want) {
					t.Errorf("body lacks %q:\n%s", want, body)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(body, absent) {
					t.Errorf("body has %q:\n%s", absent, body)
				}
			}
		})
	}
	for _, skipped := range []mail.ManagedPostgresCapNotice{
		{Threshold: mail.ManagedPostgresCapAllowance, Allowance: 1, Ceiling: 1, ReachedAt: reached}, // the suspension email covers it
		{Threshold: "nearly", Allowance: 1, Ceiling: 1, ReachedAt: reached},
	} {
		if _, _, ok := mail.ManagedPostgresCapBodyV1("owner@example.com", skipped); ok {
			t.Errorf("%+v rendered; want no email", skipped)
		}
	}
}

// ADR-20261110160000: the egress hold email names the shared allowance, what
// apps and databases sent, and how to resume sooner.
func TestManagedPostgresCapBodyV1EgressHeld(t *testing.T) {
	reached := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	n := mail.ManagedPostgresCapNotice{Threshold: mail.ManagedPostgresCapEgressHeld, UsedSeconds: 500 << 30, Allowance: 500 << 30, Ceiling: 500 << 30, ReachedAt: reached}
	subject, body, ok := mail.ManagedPostgresCapBodyV1("one@example.net", n)
	if !ok || !strings.Contains(subject, "paused until next month") {
		t.Fatalf("subject %q, ok %v", subject, ok)
	}
	for _, want := range []string{"500.0 GiB monthly egress allowance", "sent 500.0 GiB by 2026-11-20 08:00 UTC", "resume on 2026-12-01", "gregale billing egress-overage on"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	n.Overage, n.Ceiling, n.UsedSeconds = true, 1000<<30, 1000<<30
	_, body, ok = mail.ManagedPostgresCapBodyV1("one@example.net", n)
	if !ok || !strings.Contains(body, "1000.0 GiB, twice the 500.0 GiB monthly egress allowance") || strings.Contains(body, "egress-overage on") {
		t.Fatalf("overage body:\n%s", body)
	}
}
