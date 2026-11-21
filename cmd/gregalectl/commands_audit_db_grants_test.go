package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func dbGrantKnown(tables ...string) map[string]bool {
	out := map[string]bool{}
	for _, t := range tables {
		out[t] = true
	}
	return out
}

func flattenAccess(access map[string]map[string]bool) []string {
	var out []string
	for table, privs := range access {
		for p := range privs {
			out = append(out, table+":"+p)
		}
	}
	sort.Strings(out)
	return out
}

func TestStatementTableAccess(t *testing.T) {
	known := dbGrantKnown("apps", "deployments", "instances", "audit_log", "app_secrets")
	cases := []struct {
		name, query string
		want        []string
	}{
		{"select with join", `SELECT a.id FROM apps a JOIN deployments d ON d.app_id = a.id WHERE a.slug = $1`,
			[]string{"apps:SELECT", "deployments:SELECT"}},
		{"insert returning", `INSERT INTO audit_log (actor, kind) VALUES ($1, $2) RETURNING id`,
			[]string{"audit_log:INSERT", "audit_log:SELECT"}},
		{"quoted and schema-qualified", `UPDATE "public"."instances" SET state = $1 WHERE id = $2`,
			[]string{"instances:SELECT", "instances:UPDATE"}},
		{"delete only", `DELETE FROM ONLY app_secrets WHERE app_id = $1`,
			[]string{"app_secrets:DELETE", "app_secrets:SELECT"}},
		{"upsert needs update", `INSERT INTO instances (id) VALUES ($1) ON CONFLICT (id) DO UPDATE SET state = excluded.state`,
			[]string{"instances:INSERT", "instances:SELECT", "instances:UPDATE"}},
		{"row lock needs update on what it reads", `SELECT * FROM deployments d JOIN apps a ON a.id = d.app_id WHERE d.id = $1 FOR UPDATE OF d SKIP LOCKED`,
			[]string{"apps:SELECT", "apps:UPDATE", "deployments:SELECT", "deployments:UPDATE"}},
		{"writable cte", `WITH gone AS (DELETE FROM instances WHERE app_id = $1 RETURNING id) INSERT INTO audit_log (kind) SELECT 'x' FROM gone`,
			[]string{"audit_log:INSERT", "audit_log:SELECT", "instances:DELETE", "instances:SELECT"}},
		{"merge", `MERGE INTO apps a USING deployments d ON a.id = d.app_id WHEN MATCHED THEN UPDATE SET slug = d.slug`,
			[]string{"apps:DELETE", "apps:INSERT", "apps:SELECT", "apps:UPDATE", "deployments:SELECT"}},
		{"literals and comments ignored", `SELECT 'apps' AS label /* instances */ -- deployments
FROM audit_log`, []string{"audit_log:SELECT"}},
		{"unknown names ignored", `SELECT now(), pg_advisory_xact_lock($1)`, nil},
		{"update set is not a table", `UPDATE apps SET deployments = $1`, []string{"apps:SELECT", "apps:UPDATE", "deployments:SELECT"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := flattenAccess(statementTableAccess(tc.query, known))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildDBGrantReport(t *testing.T) {
	tables := []string{"apps", "instances", "audit_log", "goose_db_version"}
	granted := map[string]map[string]map[string]bool{
		"faas_schedd": {
			"apps":      {privSelect: true, privInsert: true, privUpdate: true, privDelete: true},
			"instances": {privSelect: true, privInsert: true, privUpdate: true, privDelete: true},
			"audit_log": {privSelect: true, privInsert: true},
		},
	}
	statements := []dbGrantStatement{
		{Role: "faas_schedd", Query: `SELECT id FROM apps WHERE slug = $1`, Calls: 10},
		{Role: "faas_schedd", Query: `UPDATE instances SET state = $1 WHERE id = $2`, Calls: 5},
		{Role: "faas_schedd", Query: `DELETE FROM goose_db_version`, Calls: 1},
		{Role: "faas_apid", Query: `SELECT 1 FROM apps`, Calls: 3},
	}
	report := buildDBGrantReport("faas", tables, []string{"faas_schedd"}, granted, statements)
	if len(report.Roles) != 1 {
		t.Fatalf("roles: %+v", report.Roles)
	}
	r := report.Roles[0]
	if r.Statements != 3 || r.Calls != 16 {
		t.Fatalf("totals: %+v", r)
	}
	byTable := map[string]dbGrantTable{}
	for _, row := range r.Tables {
		byTable[row.Table] = row
	}
	if got := byTable["apps"]; !reflect.DeepEqual(got.Unused, []string{privInsert, privUpdate, privDelete}) || !reflect.DeepEqual(got.Observed, []string{privSelect}) {
		t.Fatalf("apps: %+v", got)
	}
	if got := byTable["instances"]; !reflect.DeepEqual(got.Observed, []string{privSelect, privUpdate}) || !reflect.DeepEqual(got.Unused, []string{privInsert, privDelete}) {
		t.Fatalf("instances: %+v", got)
	}
	if got := byTable["audit_log"]; got.Observed != nil || !reflect.DeepEqual(got.Unused, []string{privSelect, privInsert}) {
		t.Fatalf("audit_log: %+v", got)
	}
	if got := byTable["goose_db_version"]; !reflect.DeepEqual(got.Ungranted, []string{privSelect, privDelete}) {
		t.Fatalf("goose_db_version: %+v", got)
	}
	if r.GrantedTables != 3 || r.ObservedTables != 3 || r.UnusedGrants != 7 || r.UngrantedUses != 2 {
		t.Fatalf("counts: %+v", r)
	}
}

func TestEmitDBGrantReport(t *testing.T) {
	report := buildDBGrantReport("faas", []string{"apps"}, []string{"faas_apid"},
		map[string]map[string]map[string]bool{"faas_apid": {"apps": {privSelect: true, privDelete: true}}},
		[]dbGrantStatement{{Role: "faas_apid", Query: `SELECT 1 FROM apps`, Calls: 2}})
	var text bytes.Buffer
	if err := emitDBGrantReport(&text, report, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"faas_apid: 1 statements, 2 calls; 1 tables observed of 1 granted; 1 unused grants", "apps", "Caveats:"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("want %q in:\n%s", want, text.String())
		}
	}
	var out bytes.Buffer
	if err := emitDBGrantReport(&out, report, true); err != nil {
		t.Fatal(err)
	}
	var decoded dbGrantReport
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil || decoded.Roles[0].UnusedGrants != 1 {
		t.Fatalf("json: %v %+v", err, decoded)
	}
}
