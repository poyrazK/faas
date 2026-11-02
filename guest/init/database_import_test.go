// adr: 1027
package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

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
	script := strings.Join(importScriptArgs(17, "0123abcd"), " ")
	for _, flag := range []string{"/usr/lib/postgresql/17/bin/pg_restore", "--single-transaction", "--exit-on-error", "--no-owner", "--no-privileges", "--restrict-key=0123abcd", "--use-list=", "--file=-"} {
		if !strings.Contains(script, flag) {
			t.Errorf("pg_restore lacks %s: %s", flag, script)
		}
	}
	if strings.Contains(script, "--dbname") || strings.Contains(script, "--role") {
		t.Errorf("pg_restore must write a script, not connect: %s", script)
	}
	apply := strings.Join(importApplyArgs(17, "gregale"), " ")
	for _, flag := range []string{"/usr/lib/postgresql/17/bin/psql -X", "ON_ERROR_STOP=1", "-U gregale_import", "-d gregale", "-f -", "-w"} {
		if !strings.Contains(apply, flag) {
			t.Errorf("psql lacks %s: %s", flag, apply)
		}
	}
	// The script's own transaction is the restore's: psql must not wrap
	// it, or the script's COMMIT would end psql's transaction early.
	if strings.Contains(apply, "--single-transaction") || strings.Contains(apply, "-U postgres") {
		t.Errorf("psql must run the script unwrapped and as the import role: %s", apply)
	}
}

// The restore session is never the superuser, and its owner may not hold
// any role attribute.
func TestImportRoleStatement(t *testing.T) {
	got := importRoleStatement("gregale_schema_owner")
	for _, want := range []string{
		"CREATE ROLE gregale_import WITH LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS",
		"rolname = 'gregale_schema_owner'", "NOT rolsuper", "NOT rolcreaterole", "NOT rolreplication", "NOT rolbypassrls",
		"RAISE EXCEPTION", "GRANT gregale_schema_owner TO gregale_import",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("import role statement lacks %q:\n%s", want, got)
		}
	}
}

// The lineage opens the restore's transaction, so it commits with the copy
// The prologue opens the transaction as the owner; the epilogue carries the
// source's PUBLIC privileges and the lineage, just before the COMMIT.
func TestImportScriptPrologueAndEpilogue(t *testing.T) {
	const id = "0b0b8c1e-5f0a-4d36-9a7e-1c2d3e4f5a6b"
	if got := importScriptPrologue("gregale_schema_owner"); got != "BEGIN;\nSET ROLE gregale_schema_owner;\n" {
		t.Fatalf("prologue = %q", got)
	}
	got := importScriptEpilogue("gregale", id, []string{"REVOKE ALL ON ROUTINE public.f() FROM PUBLIC;"})
	want := "REVOKE ALL ON ROUTINE public.f() FROM PUBLIC;\nCOMMENT ON DATABASE gregale IS 'gregale:import:v1:" + id + "';\n"
	if got != want {
		t.Fatalf("epilogue = %q, want %q", got, want)
	}
	if lineage, ok := importLineage("gregale:import:v1:" + id); !ok || lineage != id {
		t.Fatalf("lineage of the recorded comment = %q, %v", lineage, ok)
	}
}

// The epilogue lands before the script's last COMMIT, and only once the
// end marker proves the script complete; COPY rows that read "COMMIT;"
// pass through, and a script cut short never gets its COMMIT.
func TestGateCommit(t *testing.T) {
	const key = "0123abcd"
	long := strings.Repeat("y", 200<<10)
	complete := "\\restrict " + key + "\nBEGIN;\nCOPY public.t (a) FROM stdin;\nCOMMIT;\n" + long + "\nCOMMIT;\n-- a row\n\\.\n\nCOMMIT;\n\n--\n-- PostgreSQL database dump complete\n--\n\n\\unrestrict " + key + "\n\n"
	var out strings.Builder
	if err := gateCommit(&out, strings.NewReader(complete), key, "EPILOGUE;\n"); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(complete, "\\.\n\nCOMMIT;\n", "\\.\n\nEPILOGUE;\nCOMMIT;\n", 1)
	if out.String() != want {
		t.Fatalf("gated script differs (%d vs %d bytes)", out.Len(), len(want))
	}
	if strings.Count(out.String(), "EPILOGUE;") != 1 {
		t.Fatal("epilogue written more than once")
	}

	for name, script := range map[string]string{
		"cut before the end":        "BEGIN;\nCOPY public.t (a) FROM stdin;\nCOMMIT;\n",
		"cut before the marker":     "BEGIN;\nCREATE TABLE t ();\nCOMMIT;\n\n--\n",
		"another key":               "BEGIN;\nCOMMIT;\n\\unrestrict ffff\n",
		"marker without its commit": "BEGIN;\nCREATE TABLE t ();\n\\unrestrict " + key + "\n",
	} {
		var cut strings.Builder
		if err := gateCommit(&cut, strings.NewReader(script), key, "EPILOGUE;\n"); !errors.Is(err, errScriptUnfinished) {
			t.Errorf("%s: err = %v, want errScriptUnfinished", name, err)
		}
		if strings.HasSuffix(strings.TrimRight(cut.String(), "\n-"), "COMMIT;") || strings.Contains(cut.String(), "EPILOGUE") {
			t.Errorf("%s: the commit or epilogue got through: %q", name, cut.String())
		}
	}
}

// Only the query's own statement shapes are accepted from the source.
func TestPublicACLStatements(t *testing.T) {
	out := []byte("REVOKE ALL ON ROUTINE public.add_one(integer) FROM PUBLIC;\x00GRANT EXECUTE ON ROUTINE public.add_one(integer) TO PUBLIC;\x00" +
		"GRANT SELECT, UPDATE ON TABLE billing.\"in\nvoices\" TO PUBLIC;\x00GRANT USAGE ON SCHEMA billing TO PUBLIC;\x00\n")
	got, err := publicACLStatements(out)
	if err != nil || len(got) != 4 {
		t.Fatalf("statements = %q, %v", got, err)
	}
	for _, bad := range []string{
		"GRANT CREATE ON SCHEMA public TO PUBLIC;",
		"GRANT TRIGGER ON TABLE public.t TO PUBLIC;",
		"GRANT SELECT ON TABLE public.t TO gregale_rt_abc;",
		"ALTER ROLE gregale_import SUPERUSER;",
		"GRANT ALL ON TABLE public.t TO PUBLIC;",
	} {
		if _, err := publicACLStatements([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if got, err := publicACLStatements(nil); err != nil || len(got) != 0 {
		t.Fatalf("no privileges = %q, %v", got, err)
	}
}

func TestImportLineage(t *testing.T) {
	const id = "0b0b8c1e-5f0a-4d36-9a7e-1c2d3e4f5a6b"
	for comment, want := range map[string]string{
		"gregale:import:v1:" + id:                  id,
		" gregale:import:v1:" + id + "\n":          id,
		"gregale:import:v1:":                       "",
		"gregale:import:v1:not-a-uuid":             "",
		"gregale:import:v1:" + strings.ToUpper(id): "",
		"gregale:import:v2:" + id:                  "",
		"a customer's own comment":                 "",
	} {
		got, ok := importLineage(comment)
		if got != want || ok != (want != "") {
			t.Errorf("importLineage(%q) = %q, %v; want %q", comment, got, ok, want)
		}
	}
	out := "gregale|gregale:import:v1:" + id + "\nother|gregale:import:v1:bogus\nbroken line\n"
	if got := recordedImports(out); len(got) != 1 || got["gregale"] != id {
		t.Fatalf("recordedImports = %v", got)
	}
	if got := recordedImports(""); len(got) != 0 {
		t.Fatalf("recordedImports of nothing = %v", got)
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

// The import leaves the floor free on any volume and needs room for the
// copy and its dump above it.
func TestImportSpace(t *testing.T) {
	const mib, gib = 1 << 20, 1 << 30
	for total, want := range map[uint64]uint64{1 * gib: 128 * mib, 2 * gib: 128 * mib, 10 * gib: 512 * mib, 100 * gib: 5 * gib} {
		if got := importSpaceFloor(total); got != want {
			t.Errorf("floor of %d = %d, want %d", total, got, want)
		}
	}
	if got := importSpaceNeeded(2*gib, 128*mib); got != 4*gib+128*mib {
		t.Fatalf("needed for 2 GiB = %d", got)
	}
}

// The watchdog stops the copy at the floor with its own cause, retries a
// failed probe, and stays quiet while space remains.
func TestWatchImportSpaceStopsAtTheFloor(t *testing.T) {
	readings := make(chan uint64, 8)
	for _, free := range []uint64{900, 0, 500, 100} {
		readings <- free
	}
	probes := 0
	ctx, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchImportSpace(ctx, stop, 200, time.Millisecond, func() (uint64, error) {
			probes++
			free := <-readings
			if free == 0 {
				return 0, errors.New("statfs failed")
			}
			return free, nil
		})
	}()
	<-done
	if !errors.Is(context.Cause(ctx), errImportVolumeFull) || probes != 4 {
		t.Fatalf("cause %v after %d probes; want errImportVolumeFull after 4", context.Cause(ctx), probes)
	}

	quiet, cancel := context.WithCancelCause(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel(nil) }()
	watchImportSpace(quiet, cancel, 200, time.Millisecond, func() (uint64, error) { return 900, nil })
	if errors.Is(context.Cause(quiet), errImportVolumeFull) {
		t.Fatal("stopped a copy with space left")
	}
}
