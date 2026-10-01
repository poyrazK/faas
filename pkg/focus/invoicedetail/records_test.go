package invoicedetail

import (
	"testing"
	"time"
)

// adr:382
func TestFOCUSRecordFingerprintNormalization(t *testing.T) {
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	s := Snapshot{ID: "invoice", AccountID: "account", Provider: "polar", ProviderInvoiceID: "order", Currency: "eur",
		PeriodStart: now.AddDate(0, 0, -29), PeriodEnd: now, CreatedAt: now, UpdatedAt: now,
		IssuedAt: "2026-09-30T00:00:00Z", DueAt: "2026-10-30T00:00:00Z", PaymentTerms: "Net 30",
		Detailed: true, Lines: []Line{{ID: "plan", Description: "Plan", ChargeCategory: "Purchase", NetCents: 1000, TaxCents: 190}}}
	before := Records(s)
	s.Currency, s.IssuedAt, s.DueAt = "EUR", "2026-09-30T03:00:00+03:00", "2026-10-29T20:00:00-04:00"
	s.CreatedAt, s.UpdatedAt = now.Add(time.Hour), now.Add(2*time.Hour)
	s.Lines[0].CreatedAt, s.Lines[0].UpdatedAt = "2026-09-30T01:00:00Z", "2026-09-30T02:00:00Z"
	after := Records(s)
	for n := range before {
		if before[n].ID() != after[n].ID() || before[n].Fingerprint() != after[n].Fingerprint() {
			t.Fatal("equivalent canonical values or lifecycle timestamps changed fingerprint")
		}
	}
	// Fields which become exported columns must change both record hashes.
	for _, tc := range []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{"account", func(s *Snapshot) { s.AccountID = "different-account" }},
		{"document", func(s *Snapshot) { s.ProviderInvoiceID = "different-document" }},
		{"currency", func(s *Snapshot) { s.Currency = "usd" }},
		{"merchant fallback", func(s *Snapshot) { s.Provider = "paddle" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := s
			tc.mutate(&changed)
			rows := Records(changed)
			for n := range after {
				if rows[n].ID() != after[n].ID() || rows[n].Fingerprint() == after[n].Fingerprint() {
					t.Fatal("changed common exported column was not detected")
				}
			}
		})
	}
	// Fingerprinting must not mutate the rendered date columns or leak slices.
	values := after[0].Values()
	values[6] = "mutated"
	_ = after[0].Fingerprint()
	if after[0].CreatedAt() != "2026-09-30T01:00:00Z" {
		t.Fatal("record values alias or fingerprint mutates them")
	}
}
