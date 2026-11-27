package dbmigration

import (
	"strings"
	"testing"
)

func TestMigrationSlotContinuityOnReconnect(t *testing.T) {
	slot := Slot{Exists: true, Plugin: "pgoutput", Database: "orders", Confirmed: "0/200", WALStatus: "reserved"}
	for _, tt := range []struct {
		name    string
		slot    Slot
		durable string
		want    bool
	}{
		{name: "saved acknowledgment", slot: slot, durable: "0/180", want: true},
		{name: "missing", slot: Slot{}, durable: "0/200"},
		{name: "invalidated", slot: Slot{Plugin: "pgoutput", Database: "orders", Confirmed: "0/200", WALStatus: "lost"}, durable: "0/200"},
		{name: "rewound", slot: Slot{Plugin: "pgoutput", Database: "orders", Confirmed: "0/1FF", WALStatus: "reserved"}, durable: "0/200"},
		{name: "rebuilt past durable apply", slot: Slot{Plugin: "pgoutput", Database: "orders", Confirmed: "0/301", WALStatus: "reserved"}, durable: "0/300"},
		{name: "apply advanced after last sample", slot: Slot{Plugin: "pgoutput", Database: "orders", Confirmed: "0/300", WALStatus: "reserved"}, durable: "0/300", want: true},
		{name: "different database", slot: Slot{Plugin: "pgoutput", Database: "other", Confirmed: "0/200", WALStatus: "reserved"}, durable: "0/200"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsContinuous(tt.slot, "orders", "0/100", "0/200", tt.durable); got != tt.want {
				t.Fatalf("continuity = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMigrationSchemaUsesOnlySupportedTypesAndIdentifiers(t *testing.T) {
	table := Table{Supported: true, Name: "orders", Key: []string{"select"}, Columns: []Column{
		{Name: "select", Type: "int8", Mod: -1, Required: true},
		{Name: "qty", Type: "numeric", Mod: (8<<16 | 2) + 4},
	}}
	schema, err := SchemaSQL([]Table{table})
	if err != nil || !strings.Contains(schema, `"select" pg_catalog."int8" NOT NULL`) ||
		!strings.Contains(schema, `"qty" pg_catalog.numeric(8,2)`) {
		t.Fatalf("schema = %q, %v", schema, err)
	}
	table.Columns[1].Type = "malicious_type"
	if _, err := SchemaSQL([]Table{table}); err == nil {
		t.Fatal("unsupported type accepted")
	}
}

func TestMigrationResourcesAreScopedToTarget(t *testing.T) {
	const op = "626a63ad-801c-493f-a952-40fd28cd9254"
	first := ResourceName(op, "a")
	if first != ResourceName(op, "a") || first == ResourceName(op, "b") || len(first) > 63 {
		t.Fatalf("unstable or unscoped resource name %q", first)
	}
}
