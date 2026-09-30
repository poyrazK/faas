package focus

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// ColumnDefinition describes the exact delivered CSV schema in FOCUS metadata.
type ColumnDefinition struct {
	ColumnName       string `json:"ColumnName"`
	DataType         string `json:"DataType"`
	StringMaxLength  int    `json:"StringMaxLength,omitempty"`
	StringEncoding   string `json:"StringEncoding,omitempty"`
	NumericPrecision int    `json:"NumericPrecision,omitempty"`
	NumberScale      int    `json:"NumberScale,omitempty"`
}

// Metadata links a dataset instance to its schema and declares the limitations
// of Gregale's projection. x_GregaleProjection is a custom metadata section.
type Metadata struct {
	DataGenerator   DataGenerator     `json:"DataGenerator"`
	DatasetInstance []DatasetInstance `json:"DatasetInstance"`
	Schema          []Schema          `json:"Schema"`
	Projection      Projection        `json:"x_GregaleProjection"`
}

type DataGenerator struct {
	Name string `json:"DataGenerator"`
}

type DatasetInstance struct {
	ID   string `json:"DatasetInstanceId"`
	Name string `json:"DatasetInstanceName"`
	Kind string `json:"FocusDatasetId"`
}

type Schema struct {
	ID                string             `json:"SchemaId"`
	FocusVersion      string             `json:"FocusVersion"`
	CreationDate      string             `json:"CreationDate"`
	DatasetInstanceID string             `json:"DatasetInstanceId"`
	ColumnDefinition  []ColumnDefinition `json:"ColumnDefinition"`
}

type Projection struct {
	Status                string            `json:"ConformanceStatus"`
	MissingRequiredFields []string          `json:"MissingRequiredFields"`
	Limitations           []string          `json:"Limitations"`
	BillingAccountID      string            `json:"BillingAccountId"`
	Month                 string            `json:"Month"`
	MonthFilter           string            `json:"MonthFilter"`
	GeneratedAt           string            `json:"GeneratedAt"`
	CSVFilename           string            `json:"CSVFilename"`
	CSVSHA256             string            `json:"CSVSHA256"`
	RowCount              int               `json:"RowCount"`
	BilledCostByCurrency  map[string]string `json:"BilledCostByCurrency"`
	ExcludedInvoices      map[string]int    `json:"ExcludedInvoices"`
}

func buildMetadata(accountID string, month, generatedAt time.Time, csv []byte, count int, totals map[string]string, excluded map[string]int) ([]byte, error) {
	instanceID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale:focus:invoice-detail:"+accountID+":"+month.Format("2006-01"))).String()
	definitions, err := json.Marshal(columns())
	if err != nil {
		return nil, fmt.Errorf("encode FOCUS schema identity: %w", err)
	}
	// This schema object is created with the export. Include its creation
	// instant in the identity so repeated exports never change CreationDate
	// on an existing SchemaId. Definitions participate in the identity too.
	schemaID := uuid.NewSHA1(uuid.NameSpaceURL, append([]byte(instanceID+":"+Version+":"+date(generatedAt)+":"), definitions...)).String()
	digest := sha256.Sum256(csv)
	m := Metadata{
		DataGenerator:   DataGenerator{Name: "Gregale"},
		DatasetInstance: []DatasetInstance{{ID: instanceID, Name: "Gregale invoice detail " + month.Format("2006-01"), Kind: "InvoiceDetail"}},
		Schema:          []Schema{{ID: schemaID, FocusVersion: Version, CreationDate: date(generatedAt), DatasetInstanceID: instanceID, ColumnDefinition: columns()}},
		Projection: Projection{
			Status: "partial", MissingRequiredFields: []string{"PaymentTerms"},
			Limitations: []string{
				"Payment terms are not persisted; PaymentTerms is empty and full FOCUS conformance is not claimed.",
				"Invoice-level non-tax and tax aggregates, not provider line-item or per-resource cost allocation.",
				"Issue and due dates are not persisted; InvoiceIssueDate and PaymentDueDate are empty.",
				"Conditional payment-currency and purchase-order data are unavailable; those columns are omitted.",
				"Refunds and credit notes are not separate invoice documents in this projection; amounts remain original invoice totals.",
				"Only locally persisted invoices are included; this is not a provider reconciliation or completeness guarantee.",
				"Issuer names use normalized merchant brands; exact invoice legal issuer identities are not persisted.",
			},
			BillingAccountID: accountID, Month: month.Format("2006-01"), MonthFilter: "PeriodEnd in UTC month (half-open)",
			GeneratedAt: date(generatedAt), CSVFilename: csvFilename(month), CSVSHA256: hex.EncodeToString(digest[:]),
			RowCount: count, BilledCostByCurrency: totals, ExcludedInvoices: excluded,
		},
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode FOCUS metadata: %w", err)
	}
	return append(data, '\n'), nil
}

func csvFilename(month time.Time) string {
	return "gregale-invoice-detail-" + month.Format("2006-01") + ".csv"
}

// Artifact delivers the CSV and metadata together by default. The SHA-256 in
// metadata binds it to the CSV; separate downloads can observe newer webhooks.
func (d Dataset) Artifact(month time.Time, format string) (body []byte, contentType, filename string, err error) {
	base := strings.TrimSuffix(csvFilename(month), ".csv")
	switch format {
	case "csv":
		return d.CSV, "text/csv; charset=utf-8", base + ".csv", nil
	case "metadata":
		return d.Metadata, "application/json", base + ".metadata.json", nil
	case "zip":
		body, err = d.archive(month)
		return body, "application/zip", base + ".zip", err
	default:
		return nil, "", "", fmt.Errorf("expected format zip, csv, or metadata")
	}
}

func (d Dataset) archive(month time.Time) ([]byte, error) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, file := range []struct {
		name string
		data []byte
	}{{csvFilename(month), d.CSV}, {"metadata.json", d.Metadata}} {
		w, err := z.Create(file.name)
		if err != nil {
			return nil, fmt.Errorf("create FOCUS ZIP entry: %w", err)
		}
		if _, err := w.Write(file.data); err != nil {
			return nil, fmt.Errorf("write FOCUS ZIP entry: %w", err)
		}
	}
	if err := z.Close(); err != nil {
		return nil, fmt.Errorf("finish FOCUS ZIP: %w", err)
	}
	if buf.Len() > api.MaxFOCUSExportBytes {
		return nil, fmt.Errorf("FOCUS ZIP exceeds %d bytes", api.MaxFOCUSExportBytes)
	}
	return buf.Bytes(), nil
}
