package dbmigration

import (
	"strings"
	"testing"
)

func TestVerificationMarkerUsesExistingPublication(t *testing.T) {
	m := VerificationMarker("gregale_m_fixture", "test-operation")
	sql := m.CreateSQL("gregale_m_fixture")
	if !strings.Contains(sql, `ALTER PUBLICATION "gregale_m_fixture" ADD TABLE ONLY public."gregale_m_fixture_barrier"`) {
		t.Fatalf("source marker is outside the existing publication: %s", sql)
	}
	if strings.Contains(m.CreateSQL(""), "ALTER PUBLICATION") {
		t.Fatal("target marker modifies a publication")
	}
	if !strings.Contains(InventoryWithMarkerSQL(m), Literal(m.Tag)) {
		t.Fatal("inventory exclusion needs a scoped ownership tag")
	}
	if !strings.Contains(m.InsertSQL("token"), "ON CONFLICT DO NOTHING") {
		t.Fatal("retry rewrites the marker")
	}
}

func TestVerificationSequencePreservesCallState(t *testing.T) {
	seq := Sequence{Schema: "public", Name: "orders_seq", Type: "bigint", Start: 40, Increment: -2,
		Minimum: 1, Maximum: 100, Cache: 3, Cycle: true, Standalone: true}
	create, err := seq.CreateSQL()
	if err != nil || !strings.Contains(create, "INCREMENT BY -2 MINVALUE 1 MAXVALUE 100 START WITH 40 CACHE 3 CYCLE") {
		t.Fatalf("sequence DDL = %q, %v", create, err)
	}
	val, err := seq.SetValueSQL(SequenceValue{Name: seq.Name, Last: 40})
	if err != nil || !strings.Contains(val, ",40,false)") {
		t.Fatalf("unused sequence = %q, %v", val, err)
	}
	if _, err := seq.SetValueSQL(SequenceValue{Name: seq.Name, Last: 101}); err == nil {
		t.Fatal("value beyond sequence bounds accepted")
	}
	seq.Standalone = false
	if _, err := SequenceValuesSQL([]Sequence{seq}); err == nil {
		t.Fatal("owned sequence accepted")
	}
	seq.Standalone, seq.Name = true, `s"; SELECT 1; --`
	if _, err := seq.CreateSQL(); err == nil {
		t.Fatal("invalid sequence identifier accepted")
	}
}
