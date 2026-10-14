// adr: 1027
package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

func TestImportConnStringCarriesNoSecretOrEndpoint(t *testing.T) {
	src := dbguest.ImportSource{Database: "gregale", User: "gregale_mig_ab", Password: "secret-value", TargetDatabase: "gregale", Owner: "gregale_schema_owner"}
	got := importConnString(src)
	want := "host=/run/gregale-import port=5432 dbname=gregale user=gregale_mig_ab sslmode=disable connect_timeout=30 application_name=gregale_import"
	if got != want || strings.Contains(got, "secret") {
		t.Fatalf("conn string %q", got)
	}
	dump := strings.Join(importDumpArgs(17, got), " ")
	for _, flag := range []string{"/usr/lib/postgresql/17/bin/pg_dump", "--format=custom", "--no-owner", "--no-privileges", "--no-subscriptions", "--no-publications"} {
		if !strings.Contains(dump, flag) {
			t.Errorf("pg_dump lacks %s: %s", flag, dump)
		}
	}
	restore := strings.Join(importRestoreArgs(17, "gregale", "gregale_schema_owner"), " ")
	for _, flag := range []string{"--single-transaction", "--exit-on-error", "--role=gregale_schema_owner", "--use-list=", "--dbname=gregale"} {
		if !strings.Contains(restore, flag) {
			t.Errorf("pg_restore lacks %s: %s", flag, restore)
		}
	}
}

func TestFilterImportTOC(t *testing.T) {
	list := `;
; Archive created at 2026-10-14 12:00:00 UTC
;
; Selected TOC Entries:
;
2; 3079 16385 EXTENSION - pgcrypto
3401; 0 0 COMMENT - EXTENSION pgcrypto
1; 3079 13576 EXTENSION - plpgsql
3402; 0 0 COMMENT - EXTENSION plpgsql
4; 3079 16400 EXTENSION - neon
3403; 0 0 COMMENT - EXTENSION neon 
218; 1259 16410 TABLE public accounts gregale_schema_1
3398; 0 16410 TABLE DATA public accounts gregale_schema_1
`
	got := filterImportTOC(list, []string{"neon"})
	for _, gone := range []string{"plpgsql", "EXTENSION - neon", "EXTENSION neon"} {
		if strings.Contains(got, gone) {
			t.Errorf("filtered list keeps %q:\n%s", gone, got)
		}
	}
	for _, kept := range []string{"EXTENSION - pgcrypto", "COMMENT - EXTENSION pgcrypto", "TABLE public accounts", "TABLE DATA public accounts", "; Selected TOC Entries:"} {
		if !strings.Contains(got, kept) {
			t.Errorf("filtered list lost %q:\n%s", kept, got)
		}
	}
}

func TestMissingExtensions(t *testing.T) {
	got := missingExtensions("neon,pg_stat_statements,pgcrypto,vector", "citext,pgcrypto,uuid-ossp", []string{"neon"})
	if !slices.Equal(got, []string{"pg_stat_statements", "vector"}) {
		t.Fatalf("missing = %v", got)
	}
	if got := missingExtensions("", "pgcrypto", nil); len(got) != 0 {
		t.Fatalf("no source extensions, missing %v", got)
	}
}
