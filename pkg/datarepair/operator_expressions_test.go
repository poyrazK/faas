package datarepair

import (
	"strings"
	"testing"
)

// ADR-20261103205518: operator dependencies are part of expression qualification.
func TestRepairOperatorExpressionEnrollment(t *testing.T) {
	for _, tc := range []struct {
		name, setup string
		allowed     bool
	}{
		{"builtin", `CREATE TABLE operator_products(id int PRIMARY KEY,value int CHECK(value>0))`, true},
		{"constraint", `CREATE TABLE operator_products(id int PRIMARY KEY,value int CHECK(value OPERATOR(public.##) 0))`, false},
		{"domain", `CREATE DOMAIN operator_value AS int CHECK(VALUE OPERATOR(public.##) 0); CREATE TABLE operator_products(id int PRIMARY KEY,value operator_value)`, false},
		{"index", `CREATE TABLE operator_products(id int PRIMARY KEY,value int); CREATE INDEX ON operator_products((value OPERATOR(public.##) 0))`, false},
		{"predicate", `CREATE TABLE operator_products(id int PRIMARY KEY,value int); CREATE INDEX ON operator_products(id) WHERE value OPERATOR(public.##) 0`, false},
		{"operator over builtin function", `DROP OPERATOR public.##(int,int); CREATE OPERATOR public.## (LEFTARG=int,RIGHTARG=int,FUNCTION=pg_catalog.int4gt); CREATE TABLE operator_products(id int PRIMARY KEY,value int CHECK(value OPERATOR(public.##) 0))`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := fixture(t, 1)
			if _, err := e.Pool.Exec(t.Context(), `CREATE FUNCTION public.operator_allowed(a int,b int) RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$BEGIN RAISE EXCEPTION 'application expression executed'; END$$;
CREATE OPERATOR public.## (LEFTARG=int,RIGHTARG=int,FUNCTION=public.operator_allowed);`+tc.setup); err != nil {
				t.Fatal(err)
			}
			_, err := e.Pool.Exec(t.Context(), `SELECT gregale_repair.enroll('operator_products',ARRAY['value'])`)
			if tc.allowed && err != nil || !tc.allowed && (err == nil || !strings.Contains(err.Error(), "repair_custom_expressions_unsupported")) {
				t.Fatalf("operator qualification allowed=%v err=%v", tc.allowed, err)
			}
		})
	}
}
