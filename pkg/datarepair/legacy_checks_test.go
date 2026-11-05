package datarepair

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type legacyCheckCase struct {
	name, sql string
	passed    bool
}

func legacyCheckCases() []legacyCheckCase {
	return []legacyCheckCase{
		{"boolean", "SELECT true", true},
		{"business query", "SELECT count(*)=1 FROM public.products", true},
		{"trailing comment", "SELECT true -- a reviewed assertion", true},
		{"terminal semicolon", "SELECT true;", true},
		{"terminated line comment", "SELECT true; -- a reviewed assertion", true},
		{"terminated block comment", "SELECT true; /* outer /* nested */ comment */", true},
		{"quoted semicolon", "SELECT '; -- literal'='; -- literal'; -- assertion", true},
		{"extra column", "SELECT true, false", false},
		{"duplicate names", "SELECT true AS value,true AS value", false},
		{"text true", "SELECT 'true'::text", false},
		{"integer", "SELECT 1", false},
		{"null", "SELECT NULL::boolean", false},
		{"false", "SELECT false", false},
		{"empty result", "SELECT true WHERE false", false},
		{"multiple rows", "SELECT true FROM generate_series(1,2)", false},
	}
}

func TestSelectiveRepairLegacyCheckShapes(t *testing.T) {
	for _, tc := range legacyCheckCases() {
		t.Run(tc.name, func(t *testing.T) {
			e, scope, source := fixture(t, 1)
			e.CheckSQL = tc.sql
			p := approved(t, e, scope, source)
			id := uuid.NewString()
			_, err := e.Apply(t.Context(), scope, p.ID, p.Digest, id, "check-shape")
			if tc.passed && err != nil || !tc.passed && !errors.Is(err, ErrConflict) {
				t.Fatalf("scalar-boolean contract: passed=%v err=%v", tc.passed, err)
			}
			var price, receipts int
			wantCategory, wantReceipts := "bad", 0
			if tc.passed {
				wantCategory, wantReceipts = "original", 1
			}
			if err := e.Pool.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE category=$1),(SELECT count(*) FROM gregale_repair.operations WHERE id=$2) FROM products`, wantCategory, id).Scan(&price, &receipts); err != nil || price != 1 || receipts != wantReceipts {
				t.Fatalf("failed checks left changes or a receipt: rows=%d receipts=%d err=%v", price, receipts, err)
			}
		})
	}
}

func TestSelectiveRepairLegacyCheckShapesRealRehearsal(t *testing.T) {
	if os.Getenv("GREGALE_DATA_REPAIR_DOCKER_TEST") != "1" {
		t.Skip("explicit local Docker rehearsal acceptance required")
	}
	e, scope, source := fixture(t, 1)
	cfg := e.Pool.Config().ConnConfig
	dsn := cfg.ConnString()
	if at := strings.LastIndex(dsn, "/"); at >= 0 {
		dsn = dsn[:at+1] + cfg.Database + "?sslmode=disable"
	}
	for _, tc := range legacyCheckCases() {
		t.Run(tc.name, func(t *testing.T) {
			e.CheckSQL = tc.sql
			e.Rehearser = DockerRehearser{SourceURL: dsn, DumpBinary: os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP"), CheckSQL: tc.sql}
			p, err := e.Plan(t.Context(), scope, source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Rehearse(t.Context(), scope, p.ID, p.Digest)
			if tc.passed && err != nil || !tc.passed && err == nil {
				t.Fatalf("rehearsal disagrees with live scalar-boolean contract: passed=%v err=%v", tc.passed, err)
			}
			var untouched int
			if err := e.Pool.QueryRow(t.Context(), `SELECT count(*) FROM products WHERE category='bad' AND gregale_row_version=2`).Scan(&untouched); err != nil || untouched != 1 {
				t.Fatal("rehearsal changed source rows", err)
			}
		})
	}
}
