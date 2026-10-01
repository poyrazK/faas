package focus

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr:382
func TestFOCUSInvoiceLifecycleCoverageAndValidation(t *testing.T) {
	inv := detailedFixture()
	coverage := readMetadata(t, buildFixture(t, inv)).Projection.SourceCoverage
	if coverage.UntrackedLifecycleRecords != 3 || coverage.LegacyLifecycleRecords != 0 {
		t.Fatal("missing history not declared")
	}
	inv.Lifecycle = &state.InvoiceLifecycle{StartedAt: date(inv.UpdatedAt), Records: make(map[string]state.InvoiceRecordLifecycle)}
	for _, record := range state.InvoiceExportRecords(inv) {
		inv.Lifecycle.Records[record.ID()] = state.InvoiceRecordLifecycle{CreatedAt: date(inv.CreatedAt), UpdatedAt: date(inv.UpdatedAt), Fingerprint: record.Fingerprint()}
	}
	m := readMetadata(t, buildFixture(t, inv))
	if m.Projection.SourceCoverage.UntrackedLifecycleRecords != 0 || m.Projection.SourceCoverage.LegacyLifecycleRecords != 0 {
		t.Fatal("tracked rows show a history gap")
	}
	for _, limitation := range m.Projection.Limitations {
		if strings.Contains(limitation, "Historical record creation") {
			t.Fatal("fully tracked rows claim unknown history")
		}
	}
	var id string
	for id = range inv.Lifecycle.Records {
		break
	}
	entry := inv.Lifecycle.Records[id]
	entry.LegacyCreated = true
	inv.Lifecycle.Records[id] = entry
	coverage = readMetadata(t, buildFixture(t, inv)).Projection.SourceCoverage
	if coverage.LegacyLifecycleRecords != 1 {
		t.Fatal("legacy row uncertainty not counted")
	}
	month, _ := ParseMonth("2026-09")
	for _, tc := range []struct {
		name   string
		mutate func()
	}{
		{"stale common facts", func() { inv.Details.PaymentTerms = "Net 60" }},
		{"malformed digest", func() { entry.Fingerprint = "invalid"; inv.Lifecycle.Records[id] = entry }},
		{"update before creation", func() { entry.UpdatedAt = date(inv.CreatedAt.Add(-time.Hour)); inv.Lifecycle.Records[id] = entry }},
		{"malformed start", func() { inv.Lifecycle.StartedAt = "invalid" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.mutate()
			if _, err := BuildInvoiceDetail(inv.AccountID, month, inv.UpdatedAt, []state.Invoice{inv}); err == nil {
				t.Fatal("invalid lifecycle exported")
			}
			inv.Details.PaymentTerms = "Net 30"
			entry.Fingerprint, entry.UpdatedAt = state.InvoiceExportRecords(inv)[0].Fingerprint(), date(inv.UpdatedAt)
			// Restore the selected row's digest rather than relying on map order.
			for _, record := range state.InvoiceExportRecords(inv) {
				if record.ID() == id {
					entry.Fingerprint = record.Fingerprint()
				}
			}
			inv.Lifecycle.Records[id] = entry
			inv.Lifecycle.StartedAt = date(inv.UpdatedAt)
		})
	}
}
