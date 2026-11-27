package pgexport

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/pgextension"
)

func TestExportStreamRequiresTheSuccessTrailer(t *testing.T) {
	source := Source{Major: 16, CapturedAt: time.Now(), Extensions: []pgextension.Installed{}}
	dump := bytes.Repeat([]byte("PGDMP"), Chunk)
	var archive, decoded bytes.Buffer
	stream := StreamWriter{Writer: &archive}
	if n, err := stream.Write(dump); err != nil || n != len(dump) {
		t.Fatalf("write = %d, %v", n, err)
	}
	if _, err := ReadStream(bytes.NewReader(archive.Bytes()), &decoded); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("missing success trailer = %v", err)
	}
	if err := stream.Finish(source); err != nil {
		t.Fatal(err)
	}
	decoded.Reset()
	got, err := ReadStream(&archive, &decoded)
	if err != nil || got.Major != source.Major || !got.CapturedAt.Equal(source.CapturedAt) || !bytes.Equal(decoded.Bytes(), dump) {
		t.Fatalf("verified stream = %+v, %v", got, err)
	}
}

func TestPortableExportRoundTrip(t *testing.T) {
	bin, socket := os.Getenv("FAAS_TEST_POSTGRES_EXPORT_BIN"), os.Getenv("FAAS_TEST_POSTGRES_EXPORT_SOCKET")
	restoreSocket := os.Getenv("FAAS_TEST_POSTGRES_EXPORT_RESTORE_SOCKET")
	if !filepath.IsAbs(bin) || !filepath.IsAbs(socket) || !filepath.IsAbs(restoreSocket) || socket == restoreSocket {
		t.Skip("set FAAS_TEST_POSTGRES_EXPORT_BIN, FAAS_TEST_POSTGRES_EXPORT_SOCKET and FAAS_TEST_POSTGRES_EXPORT_RESTORE_SOCKET for two disposable PostgreSQL clusters with pgvector")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancel()
	run := func(host, program, input string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, filepath.Join(bin, program), args...)
		cmd.Env = append(os.Environ(), "PGHOST="+host, "PGUSER=postgres")
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", program, err, out)
		}
		return string(out)
	}
	const source, target = "export_source", "independent_restore"
	run(socket, "createdb", "", source)
	run(restoreSocket, "createdb", "", target)
	psql := func(db, sql string) string {
		t.Helper()
		host := socket
		if db == target {
			host = restoreSocket
		}
		return run(host, "psql", sql, "-XqAtw", "-v", "ON_ERROR_STOP=1", "-d", db, "-f", "-")
	}
	enable, _ := pgextension.EnableSQL(pgextension.VectorName)
	psql(source, enable)
	psql(source, `CREATE TABLE embeddings (label text PRIMARY KEY, v vector(3));
		INSERT INTO embeddings VALUES ('near', '[0.9,0,0]'), ('far', '[0,1,0]');
		CREATE INDEX embeddings_hnsw ON embeddings USING hnsw(v vector_l2_ops);`)
	var dump bytes.Buffer
	manifest, err := Capture(ctx, bin, socket, source, func(cmd *exec.Cmd) *exec.Cmd {
		if filepath.Base(cmd.Path) == "pg_dump" {
			// The operator writes after the snapshot was acquired.
			psql(source, "INSERT INTO embeddings VALUES ('later', '[1,0,0]');")
		}
		return cmd
	}, &dump)
	want := pgextension.Installed{Name: pgextension.VectorName, Version: pgextension.VectorVersion}
	if err != nil || !manifest.Valid() || !slices.Contains(manifest.Extensions, want) {
		t.Fatalf("capture = %+v, %v", manifest, err)
	}
	dumpFile := filepath.Join(t.TempDir(), "embeddings.dump")
	if err := os.WriteFile(dumpFile, dump.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	run(restoreSocket, "pg_restore", "", "--exit-on-error", "--no-owner", "--no-privileges", "--dbname="+target, dumpFile)
	psql(target, "CREATE ROLE export_app NOLOGIN; GRANT SELECT ON embeddings TO export_app;")
	const nearest = "SELECT label FROM embeddings ORDER BY v <-> '[1,0,0]'::vector LIMIT 1;"
	result := psql(target, "SET ROLE export_app; SET enable_seqscan=off; EXPLAIN (COSTS OFF) "+nearest+nearest+"SELECT count(*) FROM embeddings;")
	if !strings.Contains(result, "Index Scan using embeddings_hnsw") || !strings.HasSuffix(result, "\nnear\n2\n") {
		t.Fatalf("independent restore did not preserve the snapshot and index:\n%s", result)
	}
}
