package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/pgextension"
)

func TestImportManagedVectorWithPostgreSQL(t *testing.T) {
	bin, socket := os.Getenv("FAAS_TEST_NATIVE_VECTOR_BIN"), os.Getenv("FAAS_TEST_NATIVE_VECTOR_SOCKET")
	if bin == "" || !filepath.IsAbs(socket) {
		t.Skip("set FAAS_TEST_NATIVE_VECTOR_BIN and FAAS_TEST_NATIVE_VECTOR_SOCKET for a disposable PostgreSQL 16–18 cluster with pgvector")
	}
	const (
		source = "gregale_vector_source"
		target = "gregale_vector_target"
		owner  = "gregale_schema_owner"
		app    = "gregale_vector_test_app"
		key    = "0123abcd"
	)
	ctx := t.Context()
	run := func(program, stdin string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, filepath.Join(bin, program), args...)
		cmd.Env = append(os.Environ(), "PGHOST="+socket, "PGUSER=postgres")
		cmd.Stdin = strings.NewReader(stdin)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s %v: %v: %s", program, args, err, stderr.String())
		}
		return string(out)
	}
	psql := func(user, db, sql string, flags ...string) string {
		t.Helper()
		return run("psql", sql, append([]string{"-XqtwA", "-v", "ON_ERROR_STOP=1", "-U", user, "-d", db, "-f", "-"}, flags...)...)
	}
	psql("postgres", "postgres", `CREATE ROLE gregale_schema_owner NOLOGIN; CREATE ROLE gregale_vector_test_app LOGIN;`)
	for _, db := range []string{source, target} {
		run("createdb", "", "--owner", owner, db)
	}
	var catalog pgextension.Catalog
	wantAvailable := pgextension.Available{Name: pgextension.VectorName, Version: pgextension.VectorVersion, Default: true}
	if err := json.Unmarshal([]byte(psql("postgres", target, pgextension.CatalogSQL)), &catalog); err != nil || !slices.Contains(catalog.Available, wantAvailable) {
		t.Fatalf("managed vector availability = %+v, %v", catalog, err)
	}
	psql("postgres", "postgres", importRoleStatement(owner))
	enable, _ := pgextension.EnableSQL(pgextension.VectorName)
	psql("postgres", source, enable)
	psql("postgres", source, enable)
	if got := strings.TrimSpace(psql("postgres", source, importVectorSchemaQuery)); got != "public" {
		t.Fatalf("vector schema = %q", got)
	}
	supported := pgextension.Installed{Name: pgextension.VectorName, Version: "0.8.6"}
	unsupportable := pgextension.Installed{Name: pgextension.VectorName, Version: "9.999"}
	check, err := pgextension.ImportCompatibleSQL([]pgextension.Installed{supported, unsupportable}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgextension.DecodeInstalled(psql("postgres", target, check))
	if err != nil || !slices.Equal(got, []pgextension.Installed{supported}) {
		t.Fatalf("vector import compatibility = %v, %v", got, err)
	}
	psql("postgres", target, enable)
	if got := strings.TrimSpace(psql("postgres", target, importTargetUsedSQL(true))); got != "0" {
		t.Fatalf("target with only managed vector is not reusable: %q", got)
	}
	if got := strings.TrimSpace(psql("postgres", target, importTargetUsedSQL(false))); got != "1" {
		t.Fatalf("restore without vector ignored an existing extension: %q", got)
	}
	psql("postgres", source, `SET ROLE gregale_schema_owner;
		CREATE TABLE documents (label text PRIMARY KEY, embedding vector(3) NOT NULL);
		INSERT INTO documents VALUES ('near', '[0.9,0,0]'), ('far', '[0,1,0]');
		CREATE INDEX documents_hnsw ON documents USING hnsw (embedding vector_l2_ops);
		CREATE MATERIALIZED VIEW restore_identity AS SELECT session_user::text AS role;
		GRANT SELECT ON documents, restore_identity TO PUBLIC;`)
	dir := t.TempDir()
	dump, list := filepath.Join(dir, "dump"), filepath.Join(dir, "list")
	run("pg_dump", "", "--format=custom", "--no-owner", "--no-privileges", "--file="+dump, source)
	toc := filterImportTOC(run("pg_restore", "", "--list", dump), []string{pgextension.VectorName})
	if err := os.WriteFile(list, []byte(toc), 0o600); err != nil {
		t.Fatal(err)
	}
	acl, err := publicACLStatements([]byte(psql("postgres", source, importPublicACLQuery, "-0")))
	if err != nil {
		t.Fatal(err)
	}
	raw := run("pg_restore", "", "--exit-on-error", "--single-transaction", "--no-owner", "--no-privileges", "--restrict-key="+key, "--use-list="+list, "--file=-", dump)
	var script strings.Builder
	script.WriteString(importScriptPrologue(owner))
	epilogue := importScriptEpilogue(target, "0b0b8c1e-5f0a-4d36-9a7e-1c2d3e4f5a6b", acl)
	if err := gateCommit(&script, strings.NewReader(raw), key, epilogue); err != nil {
		t.Fatal(err)
	}
	psql(databaseImportRole, target, script.String())
	const nearest = "SELECT label FROM documents ORDER BY embedding <-> '[1,0,0]'::vector LIMIT 1;"
	result := psql(app, target, "SET enable_seqscan = off; EXPLAIN (COSTS OFF) "+nearest+nearest+"SELECT role FROM restore_identity;")
	if !strings.Contains(result, "Index Scan using documents_hnsw") || !strings.HasSuffix(result, "\nnear\n"+databaseImportRole+"\n") {
		t.Fatalf("imported vector query did not use HNSW or restore used the wrong session role:\n%s", result)
	}
}
