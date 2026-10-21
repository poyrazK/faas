package checkpoint

import (
	"strings"
	"testing"
)

func TestWriterCatalogFingerprintPreservesIdentityAndSettings(t *testing.T) {
	base := []CatalogDatabase{{OID: 1, OwnerOID: 10, Name: "one", ConnectionLimit: -1}, {OID: 2, OwnerOID: 10, Name: "two", AllowConnections: true}}
	original, err := CatalogFingerprint(base)
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := CatalogFingerprint([]CatalogDatabase{base[1], base[0]})
	if err != nil || reordered != original {
		t.Fatal("catalog order changed identity", err)
	}
	for _, change := range []func(*CatalogDatabase){
		func(d *CatalogDatabase) { d.OID = 3 }, func(d *CatalogDatabase) { d.OwnerOID++ },
		func(d *CatalogDatabase) { d.Name = "renamed" }, func(d *CatalogDatabase) { d.AllowConnections = true },
		func(d *CatalogDatabase) { d.IsTemplate = true }, func(d *CatalogDatabase) { d.ConnectionLimit = 0 },
	} {
		rows := []CatalogDatabase{base[0], base[1]}
		change(&rows[0])
		changed, err := CatalogFingerprint(rows)
		if err != nil || changed == original {
			t.Fatal("catalog change reused earlier evidence", err)
		}
	}
	if _, err := CatalogFingerprint([]CatalogDatabase{base[0], base[0]}); err == nil {
		t.Fatal("duplicate catalog identity accepted")
	}
}

func TestWriterInventoryRejectsPartialOrMalformedPositiveEvidence(t *testing.T) {
	i := WriterInventory{CatalogComplete: true, CatalogFingerprint: strings.Repeat("a", 64), DatabaseCount: 3, UnknownBackends: 1}
	if err := i.Validate(); err != nil {
		t.Fatal("unknown activity must remain inspectable", err)
	}
	for _, change := range []func(*WriterInventory){
		func(i *WriterInventory) { i.CatalogFingerprint = "" },
		func(i *WriterInventory) { i.CatalogComplete = false },
		func(i *WriterInventory) { i.ClientSessions = -1 },
		func(i *WriterInventory) { i.UnknownBackends = -1 },
	} {
		copyInventory := i
		change(&copyInventory)
		if copyInventory.Validate() == nil {
			t.Fatal("malformed inventory accepted")
		}
	}
}
