// adr: 380
package focus

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func fixtureInvoice() state.Invoice {
	return state.Invoice{
		ID: "invoice-1", AccountID: "account-1", Provider: "polar", ProviderInvoiceID: "order-1", Status: "paid",
		PeriodStart: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), PeriodEnd: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 30, 2, 0, 0, 0, time.UTC),
		SubtotalCents: 1200, TaxCents: 190, TotalCents: 1190, AmountPaidCents: 1190, Currency: "eur",
		CreditsAppliedCents: 200, AmountRefundedCents: 100, AmountRefundPendingCents: 50,
	}
}

func buildFixture(t *testing.T, invoices ...state.Invoice) Dataset {
	t.Helper()
	month, err := ParseMonth("2026-09")
	if err != nil {
		t.Fatal(err)
	}
	d, err := BuildInvoiceDetail("account-1", month, fixtureInvoice().UpdatedAt, invoices)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func readRecords(t *testing.T, d Dataset) []map[string]string {
	t.Helper()
	rows, err := csv.NewReader(bytes.NewReader(d.CSV)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	result := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		m := make(map[string]string, len(row))
		for i, value := range row {
			m[rows[0][i]] = value
		}
		result = append(result, m)
	}
	return result
}

func readMetadata(t *testing.T, d Dataset) Metadata {
	t.Helper()
	var m Metadata
	if err := json.Unmarshal(d.Metadata, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestFOCUSInvoiceDetailReconciliation(t *testing.T) {
	for _, tc := range []struct{ provider, issuer string }{{"polar", "Polar"}, {"paddle", "Paddle"}, {"stripe", "Gregale"}} {
		t.Run(tc.provider, func(t *testing.T) {
			inv := fixtureInvoice()
			inv.Provider = tc.provider
			d := buildFixture(t, inv)
			rows := readRecords(t, d)
			if len(rows) != 2 || rows[0]["BilledCost"] != "10.00" || rows[1]["BilledCost"] != "1.90" || rows[0]["ChargeCategory"] != "Usage" || rows[1]["ChargeCategory"] != "Tax" {
				t.Fatalf("charges must reconcile to original 11.90 total, including discounts: %+v", rows)
			}
			for _, row := range rows {
				if row["BillingCurrency"] != "EUR" || row["InvoiceIssuerName"] != tc.issuer || row["InvoiceIssueStatus"] != "Issued" || row["ReferenceInvoiceId"] != inv.ProviderInvoiceID {
					t.Fatalf("incorrect invoice context: %+v", row)
				}
				if row["InvoiceIssueDate"] != "" || row["PaymentDueDate"] != "" || row["PaymentTerms"] != "" {
					t.Fatalf("invented provider fields: %+v", row)
				}
			}
			m := readMetadata(t, d)
			if m.Projection.Status != "partial" || !slices.Equal(m.Projection.MissingRequiredFields, []string{"PaymentTerms"}) || m.Projection.BilledCostByCurrency["EUR"] != "11.90" {
				t.Fatalf("incorrect conformance or totals: %+v", m.Projection)
			}
		})
	}
}

func TestFOCUSExactLargeAmountsAndSeparateCurrencies(t *testing.T) {
	a, b, c := fixtureInvoice(), fixtureInvoice(), fixtureInvoice()
	a.TotalCents, a.TaxCents = math.MaxInt64, 1
	b.ID, b.ProviderInvoiceID, b.TotalCents, b.TaxCents = "invoice-2", "order-2", math.MaxInt64, 0
	c.ID, c.ProviderInvoiceID, c.Currency, c.TotalCents, c.TaxCents = "invoice-3", "order-3", "usd", 1, 0
	d := buildFixture(t, c, b, a)
	rows := readRecords(t, d)
	if rows[0]["BilledCost"] != "92233720368547758.06" || rows[1]["BilledCost"] != "0.01" {
		t.Fatalf("lost integer precision: %+v", rows)
	}
	totals := readMetadata(t, d).Projection.BilledCostByCurrency
	if totals["EUR"] != "184467440737095516.14" || totals["USD"] != "0.01" {
		t.Fatalf("overflow or mixed currencies: %v", totals)
	}
	if !bytes.Equal(d.CSV, buildFixture(t, a, b, c).CSV) {
		t.Fatal("export ordering depends on store iteration")
	}
}

func TestFOCUSInvoiceStatusesAndZeroAmounts(t *testing.T) {
	for _, status := range []string{"draft", "void", "open", "paid", "uncollectible"} {
		t.Run(status, func(t *testing.T) {
			inv := fixtureInvoice()
			inv.Status, inv.TotalCents, inv.TaxCents = status, 0, 0
			d := buildFixture(t, inv)
			rows, m := readRecords(t, d), readMetadata(t, d)
			if status == "draft" || status == "void" {
				if len(rows) != 0 || m.Projection.ExcludedInvoices[status] != 1 {
					t.Fatalf("excluded invoice not accounted for: %+v", m)
				}
			} else if len(rows) != 1 || rows[0]["BilledCost"] != "0.00" || rows[0]["InvoiceIssueStatus"] != "Issued" {
				t.Fatalf("zero invoice or payment-state mapping: %+v", rows)
			}
		})
	}
}

func TestFOCUSCSVQuotingAndUTCPrecision(t *testing.T) {
	inv := fixtureInvoice()
	inv.ProviderInvoiceID = "order,\"quoted\"-é"
	zone := time.FixedZone("UTC+3", 3*60*60)
	inv.PeriodStart = inv.PeriodStart.In(zone)
	inv.CreatedAt = inv.CreatedAt.Add(123 * time.Nanosecond).In(zone)
	d := buildFixture(t, inv)
	for _, row := range readRecords(t, d) {
		if row["InvoiceId"] != inv.ProviderInvoiceID || row["BillingPeriodStart"] != "2026-09-01T00:00:00Z" || row["InvoiceDetailCreated"] != "2026-09-30T01:00:00.000000123Z" {
			t.Fatalf("quoting or date precision changed source values: %+v", row)
		}
	}
}

func TestFOCUSRejectsUnrepresentableInvoices(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*state.Invoice)
	}{
		{"foreign account", func(i *state.Invoice) { i.AccountID = "other" }},
		{"wrong month", func(i *state.Invoice) { i.PeriodEnd = i.PeriodEnd.AddDate(0, 1, 0) }},
		{"missing ID", func(i *state.Invoice) { i.ID = "" }},
		{"missing provider ID", func(i *state.Invoice) { i.ProviderInvoiceID = "" }},
		{"unknown status", func(i *state.Invoice) { i.Status = "cancelled" }},
		{"unknown provider", func(i *state.Invoice) { i.Provider = "unknown" }},
		{"bad currency", func(i *state.Invoice) { i.Currency = "wat" }},
		{"no currency", func(i *state.Invoice) { i.Currency = "XXX" }},
		{"zero decimal currency", func(i *state.Invoice) { i.Currency = "JPY" }},
		{"three decimal currency", func(i *state.Invoice) { i.Currency = "KWD" }},
		{"negative total", func(i *state.Invoice) { i.TotalCents = -1 }},
		{"negative tax", func(i *state.Invoice) { i.TaxCents = -1 }},
		{"tax exceeds total", func(i *state.Invoice) { i.TaxCents = i.TotalCents + 1 }},
		{"missing period", func(i *state.Invoice) { i.PeriodStart = time.Time{} }},
		{"reversed period", func(i *state.Invoice) { i.PeriodStart = i.PeriodEnd }},
		{"missing created date", func(i *state.Invoice) { i.CreatedAt = time.Time{} }},
		{"reversed update date", func(i *state.Invoice) { i.UpdatedAt = i.CreatedAt.Add(-time.Second) }},
		{"oversized ID", func(i *state.Invoice) { i.ID = strings.Repeat("x", api.MaxFOCUSExportFieldBytes+1) }},
		{"invalid UTF-8", func(i *state.Invoice) { i.ProviderInvoiceID = string([]byte{0xff}) }},
	}
	month, _ := ParseMonth("2026-09")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := fixtureInvoice()
			tc.mutate(&inv)
			if d, err := BuildInvoiceDetail("account-1", month, fixtureInvoice().UpdatedAt, []state.Invoice{inv}); err == nil || len(d.CSV) != 0 {
				t.Fatalf("accepted invalid data or returned partial artifact: error=%v", err)
			}
		})
	}
	inv := fixtureInvoice()
	if _, err := BuildInvoiceDetail("account-1", month, inv.UpdatedAt, []state.Invoice{inv, inv}); err == nil {
		t.Fatal("duplicate invoice accepted")
	}
}

func TestFOCUSSnapshotArchiveAndMetadata(t *testing.T) {
	d := buildFixture(t, fixtureInvoice())
	month, _ := ParseMonth("2026-09")
	body, mime, filename, err := d.Artifact(month, "zip")
	if err != nil || mime != "application/zip" || filename != "gregale-invoice-detail-2026-09.zip" {
		t.Fatalf("archive: %s %s %v", mime, filename, err)
	}
	r, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string][]byte)
	for _, file := range r.File {
		reader, openErr := file.Open()
		if openErr != nil {
			t.Fatal(openErr)
		}
		data, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read archive: %v %v", readErr, closeErr)
		}
		files[file.Name] = data
	}
	if len(files) != 2 || !bytes.Equal(files["metadata.json"], d.Metadata) || !bytes.Equal(files[csvFilename(month)], d.CSV) {
		t.Fatal("archive doesn't contain matching snapshot artifacts")
	}
	m := readMetadata(t, d)
	digest := sha256.Sum256(d.CSV)
	if m.Projection.CSVSHA256 != hex.EncodeToString(digest[:]) || m.Projection.RowCount != 2 || m.Schema[0].DatasetInstanceID != m.DatasetInstance[0].ID || m.DatasetInstance[0].Kind != "InvoiceDetail" {
		t.Fatalf("metadata doesn't bind the dataset: %+v", m)
	}
	if m.Schema[0].FocusVersion != "1.4" || m.DataGenerator.Name != "Gregale" {
		t.Fatalf("wrong metadata identity: %+v", m)
	}
	for _, format := range []string{"csv", "metadata"} {
		got, _, _, artifactErr := d.Artifact(month, format)
		want := d.CSV
		if format == "metadata" {
			want = d.Metadata
		}
		if artifactErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s artifact changed snapshot: %v", format, artifactErr)
		}
	}
}

// TestFOCUSOfficialColumnContract derives the mandatory columns and data types
// from unmodified, pinned upstream documents. It deliberately allows only the
// declared PaymentTerms null gap; this is not a certification test.
func TestFOCUSOfficialColumnContract(t *testing.T) {
	var manifest struct {
		Version string                                `json:"version"`
		Commit  string                                `json:"commit"`
		Files   []struct{ File, Path, SHA256 string } `json:"files"`
	}
	raw, err := os.ReadFile("testdata/specification/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != Version || manifest.Commit != "f1eeb30a78f7c141ef1237d589355296a2761c1c" {
		t.Fatal("unexpected official specification pin")
	}
	mandatory := make(map[string]string)
	nonnull := make(map[string]bool)
	columnID := regexp.MustCompile(`(?m)^## Column ID\s+([A-Za-z]+)`)
	for _, file := range manifest.Files {
		data, readErr := os.ReadFile(filepath.Join("testdata/specification", file.File))
		if readErr != nil {
			t.Fatal(readErr)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != file.SHA256 {
			t.Fatalf("modified upstream document %s", file.File)
		}
		id := columnID.FindSubmatch(data)
		if len(id) != 2 || !regexp.MustCompile(`Feature level\s*\|\s*Mandatory`).Match(data) {
			continue
		}
		name := string(id[1])
		dtype := regexp.MustCompile(`Data type\s*\|\s*(\w+(?:/\w+)?)`).FindSubmatch(data)
		if len(dtype) != 2 {
			t.Fatalf("missing type for %s", name)
		}
		mandatory[name] = strings.ToUpper(strings.ReplaceAll(string(dtype[1]), "/", ""))
		nonnull[name] = bytes.Contains(data, []byte(name+" MUST NOT be null"))
	}
	d := buildFixture(t, fixtureInvoice())
	m := readMetadata(t, d)
	if len(mandatory) != 18 || len(m.Schema[0].ColumnDefinition) != len(mandatory) {
		t.Fatalf("mandatory column count differs: %d vs %d", len(mandatory), len(m.Schema[0].ColumnDefinition))
	}
	for _, col := range m.Schema[0].ColumnDefinition {
		if mandatory[col.ColumnName] != col.DataType {
			t.Errorf("%s type=%s upstream=%s", col.ColumnName, col.DataType, mandatory[col.ColumnName])
		}
	}
	for _, row := range readRecords(t, d) {
		for name := range mandatory {
			if nonnull[name] && row[name] == "" && !slices.Contains(m.Projection.MissingRequiredFields, name) {
				t.Errorf("undeclared required-field gap %s", name)
			}
		}
	}
}

func TestFOCUSExportLimitsAndEmptyDataset(t *testing.T) {
	d := buildFixture(t)
	if len(readRecords(t, d)) != 0 || readMetadata(t, d).Projection.RowCount != 0 {
		t.Fatal("empty export must still provide schema and zero rows")
	}
	inv := fixtureInvoice()
	invoices := make([]state.Invoice, api.MaxFOCUSExportInvoices+1)
	for i := range invoices {
		invoices[i] = inv
		invoices[i].ID = fmt.Sprintf("invoice-%04d", i)
		invoices[i].ProviderInvoiceID = fmt.Sprintf("order-%04d", i)
	}
	d = buildFixture(t, invoices[:api.MaxFOCUSExportInvoices]...)
	if readMetadata(t, d).Projection.RowCount != 2*api.MaxFOCUSExportInvoices {
		t.Fatal("export was silently truncated")
	}
	month, _ := ParseMonth("2026-09")
	if _, err := BuildInvoiceDetail(inv.AccountID, month, inv.UpdatedAt, invoices); err == nil {
		t.Fatal("oversized export accepted")
	}
}
