package state

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/focus/invoicedetail"
)

// InvoiceLifecycle is local export-record history, separate from provider facts.
// LegacyHistory means this invoice predates tracking; unknown creation history
// is declared rather than reconstructed from current provider data.
type InvoiceLifecycle struct {
	StartedAt     string                            `json:"started_at"`
	LegacyHistory bool                              `json:"legacy_history,omitempty"`
	Records       map[string]InvoiceRecordLifecycle `json:"records"`
}

type InvoiceRecordLifecycle struct {
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	Fingerprint   string `json:"fingerprint"`
	LegacyCreated bool   `json:"legacy_created,omitempty"`
}

func cloneInvoiceLifecycle(l *InvoiceLifecycle) *InvoiceLifecycle {
	if l == nil {
		return nil
	}
	out := *l
	out.Records = make(map[string]InvoiceRecordLifecycle, len(l.Records))
	for id, record := range l.Records {
		out.Records[id] = record
	}
	return &out
}

func ValidateInvoiceLifecycle(l *InvoiceLifecycle) error {
	if l == nil {
		return nil
	}
	if _, err := parseLifecycleDate(l.StartedAt); err != nil {
		return fmt.Errorf("state: invalid invoice lifecycle start: %w", err)
	}
	if len(l.Records) > api.MaxInvoiceLifecycleRecords {
		return fmt.Errorf("state: too many invoice lifecycle records")
	}
	for id, record := range l.Records {
		if id == "" || !validInvoiceText(id, api.MaxFOCUSExportFieldBytes+len(":charges")) || strings.ContainsAny(id, "\r\n") {
			return fmt.Errorf("state: invalid invoice lifecycle ID")
		}
		created, err := parseLifecycleDate(record.CreatedAt)
		if err != nil {
			return fmt.Errorf("state: invalid invoice record creation: %w", err)
		}
		updated, err := parseLifecycleDate(record.UpdatedAt)
		if err != nil || updated.Before(created) {
			return fmt.Errorf("state: invalid invoice record update")
		}
		digest, err := hex.DecodeString(record.Fingerprint)
		if err != nil || len(digest) != 32 {
			return fmt.Errorf("state: invalid invoice record fingerprint")
		}
	}
	return nil
}

func parseLifecycleDate(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	if t.IsZero() || t.UTC().Year() < 1 || t.UTC().Year() > 9999 {
		return time.Time{}, fmt.Errorf("unrepresentable lifecycle date")
	}
	return t.UTC(), nil
}

// InvoiceExportRecords is the single adapter to the shared row renderer.
// Source-gap decisions and stable IDs match the exported dataset exactly.
func InvoiceExportRecords(inv Invoice) []invoicedetail.Record {
	if inv.Status != "open" && inv.Status != "paid" && inv.Status != "uncollectible" {
		return nil
	}
	s := invoicedetail.Snapshot{ID: inv.ID, AccountID: inv.AccountID, Provider: inv.Provider,
		ProviderInvoiceID: inv.ProviderInvoiceID, Currency: inv.Currency,
		PeriodStart: inv.PeriodStart, PeriodEnd: inv.PeriodEnd, CreatedAt: inv.CreatedAt, UpdatedAt: inv.UpdatedAt,
		TotalCents: inv.TotalCents, TaxCents: inv.TaxCents, Detailed: InvoiceLineGap(inv) == ""}
	if d := inv.Details; d != nil {
		s.IssuerName, s.PaymentTerms, s.IssuedAt, s.DueAt = d.IssuerName, d.PaymentTerms, d.IssuedAt, d.DueAt
		if s.Detailed {
			for _, line := range d.Lines.Items {
				s.Lines = append(s.Lines, invoicedetail.Line{ID: line.ID, Description: line.Description,
					ChargeCategory: line.ChargeCategory, NetCents: line.NetCents, TaxCents: line.TaxCents,
					CreatedAt: line.CreatedAt, UpdatedAt: line.UpdatedAt})
			}
		}
	}
	return invoicedetail.Records(s)
}

func newInvoiceLifecycle(now time.Time) *InvoiceLifecycle {
	return &InvoiceLifecycle{StartedAt: now.UTC().Format(time.RFC3339Nano), Records: make(map[string]InvoiceRecordLifecycle)}
}

// advanceInvoiceLifecycle retains removed IDs and compares all exported values.
// Call only while holding the invoice's store lock; caller history is ignored.
func advanceInvoiceLifecycle(inv Invoice, now time.Time) (*InvoiceLifecycle, error) {
	if err := ValidateInvoiceLifecycle(inv.Lifecycle); err != nil {
		return nil, err
	}
	old := inv.Lifecycle
	l := cloneInvoiceLifecycle(old)
	if l == nil {
		l = newInvoiceLifecycle(now)
		l.LegacyHistory = true
	}
	started, _ := parseLifecycleDate(l.StartedAt)
	for _, record := range InvoiceExportRecords(inv) {
		previous, exists := l.Records[record.ID()]
		fingerprint := record.Fingerprint()
		if exists && previous.Fingerprint == fingerprint {
			continue
		}
		stamp := now.UTC()
		if exists {
			updated, _ := parseLifecycleDate(previous.UpdatedAt)
			if stamp.Before(updated) {
				stamp = updated // preserve monotonicity if the local clock moves back
			}
		} else {
			previous.CreatedAt = stamp.Format(time.RFC3339Nano)
			if observed, err := parseLifecycleDate(record.CreatedAt()); l.LegacyHistory && err == nil && observed.Before(started) {
				previous.LegacyCreated = true
				if old == nil {
					previous.CreatedAt = observed.Format(time.RFC3339Nano)
				}
			}
		}
		previous.UpdatedAt, previous.Fingerprint = stamp.Format(time.RFC3339Nano), fingerprint
		l.Records[record.ID()] = previous
	}
	return l, ValidateInvoiceLifecycle(l)
}
