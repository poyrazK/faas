package api

import (
	"testing"
	"time"
)

// adr: 592
func TestObjectWriteProtectionValidation(t *testing.T) {
	until := time.Date(2027, 1, 2, 3, 4, 5, 123456789, time.UTC)
	for _, tc := range []struct {
		name  string
		p     ObjectWriteProtection
		valid bool
	}{
		{"default", ObjectWriteProtection{}, true},
		{"fixed", ObjectWriteProtection{Retention: &ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &until}}, true},
		{"hold", ObjectWriteProtection{LegalHold: &ObjectVersionLegalHold{Status: "ON"}}, true},
		{"clear", ObjectWriteProtection{Retention: &ObjectVersionRetention{}}, false},
		{"event", ObjectWriteProtection{Retention: &ObjectVersionRetention{EventHold: "ON"}}, false},
		{"missing mode", ObjectWriteProtection{Retention: &ObjectVersionRetention{RetainUntilDate: &until}}, false},
		{"invalid hold", ObjectWriteProtection{LegalHold: &ObjectVersionLegalHold{Status: "on"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.p.Valid() != tc.valid {
				t.Fatal(tc.p)
			}
		})
	}
	p := ObjectWriteProtection{Retention: &ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &until}, LegalHold: &ObjectVersionLegalHold{Status: "ON"}}
	write := p.ForWrite()
	if write.Retention.RetainUntilDate.Before(until) || write.Retention.RetainUntilDate.Nanosecond()%int(time.Millisecond) != 0 {
		t.Fatal("retention rounded down", write)
	}
	write.LegalHold.Status = "OFF"
	if p.LegalHold.Status != "ON" {
		t.Fatal("write selection aliases caller")
	}
}
