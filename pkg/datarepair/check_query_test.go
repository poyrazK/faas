package datarepair

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRepairCheckQueryLexing(t *testing.T) {
	e, _, _ := fixture(t, 1)
	ctx := t.Context()
	if _, err := e.Pool.Exec(ctx, `CREATE TABLE check_side_effect(id int)`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, sql string
		passed    bool
	}{
		{"line comment", "SELECT true; -- done", true},
		{"empty trailing statements", "SELECT true;; /* done */ ; -- done", true},
		{"CR line comment", "SELECT true; -- done\r", true},
		{"nested comments", "SELECT true; /* a /* nested */ comment */ -- done", true},
		{"quoted literal", "SELECT '; -- literal'='; -- literal'; -- done", true},
		{"quoted identifier", `SELECT true AS "semi;""--"; -- done`, true},
		{"dollar quoted", `SELECT $$; -- ' /* */$$=$$; -- ' /* */$$; -- done`, true},
		{"tagged dollar quote", `SELECT $tag$; -- ' /* */$tag$=$tag$; -- ' /* */$tag$; /* done */`, true},
		{"Unicode dollar quote", `SELECT $é$; --$é$=$é$; --$é$; -- done`, true},
		{"escaped string", `SELECT E'it\'s; --'=E'it\'s; --'; -- done`, true},
		{"identifier dollars", `SELECT bool$tag$value FROM (SELECT true AS bool$tag$value) q; -- done`, true},
		{"text true", "SELECT 'true'::text; -- done", false},
		{"JSON true", "SELECT 'true'::jsonb; -- done", false},
		{"extra column", "SELECT true,false; -- done", false},
		{"extra row", "SELECT true FROM generate_series(1,2); -- done", false},
		{"no row", "SELECT true WHERE false; -- done", false},
		{"unclosed string", "SELECT 'unfinished; --", false},
		{"unclosed dollar quote", "SELECT $tag$unfinished; --", false},
		{"unclosed comment", "SELECT true; /* unfinished", false},
		{"multiple SELECTs", "SELECT true; SELECT true", false},
		{"statement after comment", "SELECT true; /* done */ INSERT INTO check_side_effect VALUES(1)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.passed {
				var passed bool
				if err := e.Pool.QueryRow(ctx, tc.sql).Scan(&passed); err != nil || !passed {
					t.Fatalf("positive control is not valid PostgreSQL: %v %v", passed, err)
				}
			}
			policy, err := json.Marshal(CheckPolicy{Version: 1, Checks: []Check{{Name: "Assertion", SQL: tc.sql}}})
			if err != nil {
				t.Fatal(err)
			}
			var raw []byte
			if err := e.Pool.QueryRow(ctx, `SELECT gregale_repair.evaluate_checks($1::jsonb)`, policy).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var report CheckReport
			if err := json.Unmarshal(raw, &report); err != nil || !report.Valid(tc.passed) || len(report.Results) != 1 || (report.Results[0].Status == "passed") != tc.passed {
				t.Fatalf("check result changed: %s %v", raw, err)
			}
		})
	}
	var effects int
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM check_side_effect`).Scan(&effects); err != nil || effects != 0 {
		t.Fatalf("an extra statement executed: %d %v", effects, err)
	}
}

func TestRepairChecksAndDomainsRealRehearsal(t *testing.T) {
	if os.Getenv("GREGALE_DATA_REPAIR_DOCKER_TEST") != "1" {
		t.Skip("explicit local Docker rehearsal acceptance required")
	}
	for _, mode := range []string{"legacy terminated check", "named terminated check", "unrelated domains"} {
		t.Run(mode, func(t *testing.T) {
			e, scope, source := fixture(t, 1)
			query := "SELECT count(*)=1 FROM public.products WHERE category='original'; -- restored"
			if mode == "unrelated domains" {
				if _, err := e.Pool.Exec(t.Context(), `CREATE DOMAIN positive_stock AS int NOT NULL CHECK(VALUE>0);
CREATE TABLE domain_products(id int PRIMARY KEY,category text,stock positive_stock,tags int[],document json);
SELECT gregale_repair.enroll('domain_products',ARRAY['category']);
INSERT INTO domain_products(id,category,stock,tags,document) VALUES(1,'original',10,'[0:1]={10,20}','{"a":1,"a":2}')`); err != nil {
					t.Fatal(err)
				}
				source = captureValidation(t, e, scope, "domain_products", "UPDATE domain_products SET category='bad'")
				query = "SELECT category='original' AND stock=10 AND array_dims(tags)='[0:1]' AND tags[0]=10 AND (SELECT count(*) FROM json_each(document))=2 FROM public.domain_products; /* restored */"
			}
			cfg := e.Pool.Config().ConnConfig
			dsn := cfg.ConnString()
			if at := strings.LastIndex(dsn, "/"); at >= 0 {
				dsn = dsn[:at+1] + cfg.Database + "?sslmode=disable"
			}
			runner := DockerRehearser{SourceURL: dsn, DumpBinary: os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP")}
			if mode == "named terminated check" {
				e.Checks = []Check{{Name: "Restored products", SQL: query}}
				runner.Checks = e.Checks
			} else {
				e.CheckSQL, runner.CheckSQL = query, query
			}
			e.Rehearser = runner
			p := approved(t, e, scope, source)
			if _, err := e.Apply(t.Context(), scope, p.ID, p.Digest, uuid.NewString(), "qualified-repair"); err != nil {
				t.Fatal(err)
			}
			if report, err := e.Verify(t.Context(), scope, p.ID, p.Digest); err != nil || !report.Available || report.Unchanged != 1 {
				t.Fatalf("rehearsed correction did not verify: %+v %v", report, err)
			}
		})
	}
}
