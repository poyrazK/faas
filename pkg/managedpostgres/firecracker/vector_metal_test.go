//go:build metal && linux

package firecracker

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/pgextension"
)

const (
	vectorSourceMajor = 16
	vectorSeedSQL     = `CREATE TABLE documents (label text PRIMARY KEY, embedding vector(3) NOT NULL);
		INSERT INTO documents VALUES ('original', '[0.9,0,0]'), ('distant', '[0,1,0]');
		CREATE INDEX documents_hnsw ON documents USING hnsw (embedding vector_l2_ops);`
)

func TestMetalManagedVectorImportAndRestore(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_VECTOR_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_VECTOR_BASE to a PostgreSQL 16 base with managed pgvector")
	}
	sourceTLS := metalServerTLS(t, "localhost")
	leaf, err := x509.ParseCertificate(sourceTLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	stack := newMetalStackWith(t, workdir, map[int]string{vectorSourceMajor: base}, func(cfg *dbvm.RuntimeConfig) {
		cfg.ImportRoots, cfg.ImportAllowPrivate = roots, true
	})
	p, connect := stack.p, connectThroughProxy(stack.proxyAddr)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	source, target, restored := uuid.NewString(), uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		for _, id := range []string{restored, target, source} {
			_ = stack.sched.Delete(context.Background(), id)
		}
	})
	spec := devSpec()
	spec.PostgresMajor, spec.RestoreWindowSeconds = vectorSourceMajor, 3600
	for _, id := range []string{source, target} {
		provisionReady(ctx, t, p, managedpostgres.ProvisionRequest{ResourceID: id, Spec: spec})
	}

	wantVersion := pgextension.Installed{Name: pgextension.VectorName, Version: pgextension.VectorVersion}
	for range 2 {
		catalog, _, err := p.Extensions(ctx, source, pgextension.VectorName, 0)
		if err != nil || !slices.Contains(catalog.Installed, wantVersion) {
			t.Fatalf("enable managed vector: %+v, %v", catalog, err)
		}
	}
	migration := issueVectorCredential(ctx, t, p, source, "vector-import-migration", managedpostgres.CredentialMigration)
	metalQuery(ctx, t, connect, migration, vectorSeedSQL)
	port := vectorImportSource(t, stack.rt, source, sourceTLS)
	request := managedpostgres.ImportRequest{ResourceID: target, OperationID: uuid.NewString(), Source: managedpostgres.ImportSource{
		Host: "localhost", Port: port, Database: migration.Database, User: migration.Username, Password: migration.Password,
	}}
	if result, err := p.CustomerImport(ctx, request, true); err != nil || result.State != managedpostgres.ImportChecked {
		t.Fatalf("managed vector import check: %+v, %v", result, err)
	}
	if result, err := p.CustomerImport(ctx, request, false); err != nil || result.State != managedpostgres.ImportRunning {
		t.Fatalf("start managed vector import: %+v, %v", result, err)
	}
	for {
		result, err := p.ImportStatus(ctx, target)
		if err != nil || result.State != managedpostgres.ImportRunning && result.State != managedpostgres.ImportDone {
			t.Fatalf("managed vector import: %+v, %v", result, err)
		}
		if result.State == managedpostgres.ImportDone {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}

	app := issueVectorCredential(ctx, t, p, target, "vector-app", managedpostgres.CredentialReadWrite)
	expectVectorNeighbor(ctx, t, connect, app, vectorSourceMajor, "original")
	var dump bytes.Buffer
	exported, err := p.ExportDatabase(ctx, target, spec.PostgresMajor, &dump)
	if err != nil || !slices.Contains(exported.Extensions, wantVersion) || !bytes.HasPrefix(dump.Bytes(), []byte("PGDMP")) {
		t.Fatalf("export through the native database VM: %+v, %v", exported, err)
	}
	point := time.Now().UTC().Truncate(time.Microsecond)
	time.Sleep(1100 * time.Millisecond)
	metalQuery(ctx, t, connect, app, "INSERT INTO documents VALUES ('later', '[1,0,0]')")
	expectVectorNeighbor(ctx, t, connect, app, vectorSourceMajor, "later")

	restore := managedpostgres.RestoreRequest{ResourceID: restored, SourceResourceID: target, Spec: spec, PointInTime: point, IdempotencyKey: restored}
	waitForVectorDatabase(ctx, t, spec.PostgresMajor, func() (managedpostgres.ObservedDatabase, error) { return p.Restore(ctx, restore) })
	copyApp := issueVectorCredential(ctx, t, p, restored, "vector-restored-app", managedpostgres.CredentialReadWrite)
	expectVectorNeighbor(ctx, t, connect, copyApp, spec.PostgresMajor, "original")
}

func issueVectorCredential(ctx context.Context, t *testing.T, p *Provider, id, key string, access managedpostgres.CredentialAccess) managedpostgres.CredentialMaterial {
	t.Helper()
	result, err := p.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key, Access: access})
	if err != nil {
		t.Fatal(err)
	}
	if access == managedpostgres.CredentialReadWrite {
		pooled := slices.IndexFunc(result.Endpoints, func(e managedpostgres.Endpoint) bool { return e.Role == managedpostgres.EndpointPooled })
		if pooled < 0 {
			t.Fatal("app binding has no pooled endpoint")
		}
		result.Endpoints = result.Endpoints[pooled : pooled+1]
	}
	return result
}

func expectVectorNeighbor(ctx context.Context, t *testing.T, connect Connector, app managedpostgres.CredentialMaterial, major int, want string) {
	t.Helper()
	conn, err := connect(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var actualMajor int
	if err := tx.QueryRow(ctx, "SELECT current_setting('server_version_num')::int / 10000").Scan(&actualMajor); err != nil || actualMajor != major {
		t.Fatalf("server major = %d, %v, want %d", actualMajor, err, major)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatal(err)
	}
	const query = "SELECT label FROM documents ORDER BY embedding <-> '[1,0,0]'::vector LIMIT 1"
	rows, err := tx.Query(ctx, "EXPLAIN (COSTS OFF) "+query)
	if err != nil {
		t.Fatal(err)
	}
	indexUsed := false
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		indexUsed = indexUsed || strings.Contains(line, "Index Scan using documents_hnsw")
	}
	rows.Close()
	if err := rows.Err(); err != nil || !indexUsed {
		t.Fatalf("similarity query did not use the HNSW index: %v", err)
	}
	var label string
	if err := tx.QueryRow(ctx, query).Scan(&label); err != nil || label != want {
		t.Fatalf("nearest neighbor = %q, %v, want %q", label, err, want)
	}
}

func waitForVectorDatabase(ctx context.Context, t *testing.T, major int, observe func() (managedpostgres.ObservedDatabase, error)) {
	t.Helper()
	for {
		result, err := observe()
		if err != nil || result.Status != managedpostgres.ProviderStatusPending && result.Status != managedpostgres.ProviderStatusReady {
			t.Fatalf("database operation on PostgreSQL %d: %+v, %v", major, result, err)
		}
		if result.Status == managedpostgres.ProviderStatusReady {
			if result.Spec.PostgresMajor != major {
				t.Fatalf("ready on PostgreSQL %d, want %d", result.Spec.PostgresMajor, major)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

// The importer verifies a PostgreSQL TLS endpoint. This fixture adds TLS
// before relaying the connection to the disposable native source VM.
func vectorImportSource(t *testing.T, runtime *dbvm.Runtime, id string, tlsConfig *tls.Config) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				var request [8]byte
				if _, err := io.ReadFull(conn, request[:]); err != nil || !bytes.Equal(request[:], []byte{0, 0, 0, 8, 4, 210, 22, 47}) {
					return
				}
				if _, err := conn.Write([]byte{'S'}); err != nil {
					return
				}
				source := tls.Server(conn, tlsConfig)
				vm, err := runtime.Dial(t.Context(), id)
				if err != nil {
					return
				}
				defer func() { _ = vm.Close() }()
				go func() {
					_, _ = io.Copy(vm, source)
					_ = vm.Close()
				}()
				_, _ = io.Copy(source, vm)
			}()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}
