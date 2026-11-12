package datarepair

import (
	"net/url"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestRepairStatementConstraintSwaps(t *testing.T) {
	for _, tc := range []struct{ name, setup, update, check string }{
		{"unique", `CREATE TABLE swap_products(id int PRIMARY KEY,value int UNIQUE DEFERRABLE INITIALLY IMMEDIATE,untouched numeric);
INSERT INTO swap_products VALUES(1,10,1.00),(2,20,1.00)`, `UPDATE swap_products SET value=30-value`,
			`SELECT bool_and(value=id*10 AND untouched::text='1.00') FROM swap_products`},
		{"exclusion", `CREATE TABLE swap_products(id int PRIMARY KEY,value int4range,untouched numeric,EXCLUDE USING gist(value WITH &&) DEFERRABLE INITIALLY IMMEDIATE);
INSERT INTO swap_products VALUES(1,'[1,3)',1.00),(2,'[4,6)',1.00)`, `UPDATE swap_products SET value=CASE id WHEN 1 THEN '[4,6)'::int4range ELSE '[1,3)'::int4range END`,
			`SELECT bool_and(value=CASE id WHEN 1 THEN '[1,3)'::int4range ELSE '[4,6)'::int4range END AND untouched::text='1.00') FROM swap_products`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, scope, _ := fixture(t, 1)
			e.CheckSQL = tc.check
			if _, err := e.Pool.Exec(t.Context(), tc.setup+`; SELECT gregale_repair.enroll('swap_products',ARRAY['value','untouched'])`); err != nil {
				t.Fatal(err)
			}
			source := captureValidation(t, e, scope, "swap_products", tc.update)
			if os.Getenv("GREGALE_DATA_REPAIR_DOCKER_TEST") == "1" {
				u, err := url.Parse(e.Pool.Config().ConnConfig.ConnString())
				if err != nil {
					t.Fatal(err)
				}
				u.Path, u.RawPath = "/"+e.Pool.Config().ConnConfig.Database, ""
				e.Rehearser = DockerRehearser{SourceURL: u.String(), DumpBinary: os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP"), CheckSQL: e.CheckSQL}
			}
			plan := approved(t, e, scope, source)
			if plan.Candidates != 2 {
				t.Fatal("swap lost candidates", plan.Candidates)
			}
			if _, err := e.Apply(t.Context(), scope, plan.ID, plan.Digest, uuid.NewString(), "repair-swaps"); err != nil {
				t.Fatal("valid swap could not be restored", err)
			}
			verification, err := e.Verify(t.Context(), scope, plan.ID, plan.Digest)
			if err != nil || !verification.Available || verification.Unchanged != 2 {
				t.Fatalf("swap restoration not verified: %+v %v", verification, err)
			}
		})
	}
}
