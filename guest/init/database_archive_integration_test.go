package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/pgarchive"
	"github.com/onebox-faas/faas/pkg/pgexport"
	"github.com/onebox-faas/faas/pkg/pgextension"
)

func TestRestorePortableVectorArchiveWithPostgreSQL(t *testing.T) {
	bin, socket := os.Getenv("FAAS_TEST_NATIVE_VECTOR_BIN"), os.Getenv("FAAS_TEST_NATIVE_VECTOR_SOCKET")
	if !filepath.IsAbs(bin) || !filepath.IsAbs(socket) {
		t.Skip("set FAAS_TEST_NATIVE_VECTOR_BIN and FAAS_TEST_NATIVE_VECTOR_SOCKET for a disposable PostgreSQL cluster with pgvector")
	}
	const (
		source = "archive_source"
		target = "archive_target"
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
			t.Fatalf("%s: %v: %s", program, err, stderr.String())
		}
		return string(out)
	}
	psql := func(user, db, sql string) string {
		t.Helper()
		return run("psql", sql, "-XqtwA", "-v", "ON_ERROR_STOP=1", "-U", user, "-d", db, "-f", "-")
	}
	psql("postgres", "postgres", "CREATE ROLE gregale_schema_owner NOLOGIN; CREATE ROLE archive_app LOGIN;")
	run("createdb", "", "--owner", pgarchive.Owner, source)
	run("createdb", "", "--owner", pgarchive.Owner, target)
	psql("postgres", target, "SET ROLE gregale_schema_owner; CREATE FUNCTION public.only_object() RETURNS int LANGUAGE SQL AS 'SELECT 1';")
	if got := strings.TrimSpace(psql("postgres", target, archivePublicObjectsSQL)); got != "t" {
		t.Fatalf("standalone routine did not block an empty-target check: %s", got)
	}
	psql("postgres", target, "DROP FUNCTION public.only_object();")
	object := strings.TrimSpace(psql("postgres", target, "SELECT pg_catalog.lo_create(0);"))
	if got := strings.TrimSpace(psql("postgres", target, archivePublicObjectsSQL)); got != "t" {
		t.Fatalf("large object did not block an empty-target check: %s", got)
	}
	psql("postgres", target, "SELECT pg_catalog.lo_unlink("+object+");")
	if got := strings.TrimSpace(psql("postgres", target, archivePublicObjectsSQL)); got != "f" {
		t.Fatalf("fresh target did not pass: %s", got)
	}
	enable, _ := pgextension.EnableSQL(pgextension.VectorName)
	psql("postgres", source, enable)
	psql("postgres", source, `SET ROLE gregale_schema_owner;
		CREATE TABLE documents (label text PRIMARY KEY, embedding vector(3) NOT NULL);
		INSERT INTO documents VALUES ('near', '[0.9,0,0]'), ('far', '[0,1,0]');
		CREATE INDEX documents_hnsw ON documents USING hnsw (embedding vector_l2_ops);`)
	var dump bytes.Buffer
	info, err := pgexport.Capture(ctx, bin, socket, source, func(cmd *exec.Cmd) *exec.Cmd { return cmd }, &dump)
	if err != nil || info.VectorSchema != "public" || info.DatabaseBytes <= 0 {
		t.Fatalf("source capture = %+v, %v", info, err)
	}
	digest := sha256.Sum256(dump.Bytes())
	manifest := pgarchive.Manifest{Source: info, SizeBytes: int64(dump.Len()), SHA256: hex.EncodeToString(digest[:])}
	corrupt := bytes.Clone(dump.Bytes())
	corrupt[len(corrupt)-1] ^= 1
	if err := manifest.Copy(io.Discard, bytes.NewReader(corrupt)); !errors.Is(err, pgarchive.ErrArchive) {
		t.Fatalf("corrupt upload was accepted: %v", err)
	}
	if present := strings.TrimSpace(psql("postgres", target, importTargetUsedSQL(false))); present != "0" {
		t.Fatalf("target changed after the corrupt upload: %s", present)
	}
	dir := t.TempDir()
	dumpPath, listPath := filepath.Join(dir, "upload.dump"), filepath.Join(dir, "list")
	staged, err := os.Create(dumpPath)
	if err != nil {
		t.Fatal(err)
	}
	err = manifest.Copy(staged, &dump)
	if closeErr := staged.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	contents := run("pg_restore", "", "--list", dumpPath)
	if !archiveTOCMatches(contents, manifest.Major, manifest.Extensions) || archiveTOCMatches(contents, manifest.Major+1, manifest.Extensions) {
		t.Fatalf("archive inventory does not agree with the manifest:\n%s", contents)
	}
	if err := os.WriteFile(listPath, []byte(filterImportTOC(contents, []string{pgextension.VectorName})), 0o600); err != nil {
		t.Fatal(err)
	}
	psql("postgres", "postgres", importRoleStatement(pgarchive.Owner))
	psql("postgres", target, enable)
	if got := strings.TrimSpace(psql("postgres", target, archivePublicObjectsSQL)); got != "f" {
		t.Fatalf("a provider-installed vector must remain eligible for retry: %s", got)
	}
	archiveSQL := run("pg_restore", "", "--exit-on-error", "--single-transaction", "--no-owner", "--no-privileges",
		"--restrict-key="+key, "--use-list="+listPath, "--file=-", dumpPath)
	var script strings.Builder
	script.WriteString(importScriptPrologue(pgarchive.Owner))
	epilogue := importScriptEpilogue(target, "0b0b8c1e-5f0a-4d36-9a7e-1c2d3e4f5a6b", nil)
	if err := gateCommit(&script, strings.NewReader(archiveSQL), key, epilogue); err != nil {
		t.Fatal(err)
	}
	psql(databaseImportRole, target, script.String())
	psql("postgres", target, "GRANT SELECT ON documents TO archive_app;")
	const nearest = "SELECT label FROM documents ORDER BY embedding <-> '[1,0,0]'::vector LIMIT 1;"
	result := psql("archive_app", target, "SET enable_seqscan=off; EXPLAIN (COSTS OFF) "+nearest+nearest)
	if !strings.Contains(result, "Index Scan using documents_hnsw") || !strings.HasSuffix(result, "\nnear\n") {
		t.Fatalf("archive did not restore the indexed app query:\n%s", result)
	}
}
