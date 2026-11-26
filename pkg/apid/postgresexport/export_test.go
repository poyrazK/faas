package postgresexport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/pgexport"
	"github.com/onebox-faas/faas/pkg/pgextension"
	"github.com/onebox-faas/faas/pkg/storage"
)

type testSource struct{ fail bool }

func (*testSource) ExportSource(_ context.Context, owner, id string) (managedpostgres.Database, error) {
	return managedpostgres.Database{ID: id, AccountID: owner, OrgID: "test-org", ObservedGeneration: 4}, nil
}

const testDump = "PGDMP fixture archive contents"

func (s *testSource) ExportDatabase(_ context.Context, _, _ string, generation int64, dst io.Writer) (pgexport.Source, error) {
	if generation != 4 {
		return pgexport.Source{}, managedpostgres.ErrConflict
	}
	if _, err := io.WriteString(dst, testDump); err != nil {
		return pgexport.Source{}, err
	}
	if s.fail {
		return pgexport.Source{}, pgexport.ErrIncomplete
	}
	return pgexport.Source{Major: 16, CapturedAt: time.Now(), Extensions: []pgextension.Installed{}}, nil
}

func TestExportDurablePublicationAndExpiry(t *testing.T) {
	dsn := os.Getenv("FAAS_TEST_POSTGRES_EXPORT_JOB_DSN")
	if dsn == "" {
		t.Skip("set FAAS_TEST_POSTGRES_EXPORT_JOB_DSN to an empty disposable PostgreSQL database")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	sql, err := os.ReadFile("../../../migrations/20261126083649234_native_postgres_exports.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(sql), "-- +goose Down")
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	source := &testSource{}
	exports := New(pool, backend, source, key.Recipient, func() []*age.X25519Identity { return []*age.X25519Identity{key} })
	const owner, org = "test-owner", "test-org"
	database, id := uuid.NewString(), uuid.NewString()
	for range 2 {
		job, err := exports.Start(ctx, owner, database, id)
		if err != nil || job.State != "queued" || job.ID != id {
			t.Fatalf("job = %+v, %v", job, err)
		}
	}
	if _, err := exports.Start(ctx, owner, database, uuid.NewString()); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("second active export = %v", err)
	}
	if _, _, err := exports.Open(ctx, owner, org, database, id); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("queued download = %v", err)
	}
	job, err := exports.claim(ctx)
	if err != nil || job.id != id {
		t.Fatalf("claim = %+v, %v", job, err)
	}
	// A delayed attempt can finish writing its own object after reclaim,
	// without replacing the new attempt's archive or obtaining publication.
	if _, err := pool.Exec(ctx, "UPDATE native_postgres_exports SET lease_until=clock_timestamp() WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := exports.claim(ctx)
	if err != nil || reclaimed.id != id || reclaimed.token == job.token {
		t.Fatalf("reclaim = %+v, %v", reclaimed, err)
	}
	if err := exports.run(ctx, job); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("expired attempt published: %v", err)
	}
	job = reclaimed
	if err := exports.run(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, _, err := exports.Open(ctx, owner, "another-org", database, id); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("foreign organization download = %v", err)
	}
	manifest, body, err := exports.Open(ctx, owner, org, database, id)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(body)
	_ = body.Close()
	wantHash := sha256.Sum256([]byte(testDump))
	if err != nil || string(raw) != testDump || manifest.SHA256 != hex.EncodeToString(wantHash[:]) || manifest.SizeBytes != int64(len(testDump)) {
		t.Fatalf("download = %q, %+v, %v", raw, manifest, err)
	}
	ciphertext, err := backend.Get(ctx, storageKey(id, job.token))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := io.ReadAll(ciphertext)
	_ = ciphertext.Close()
	if err != nil || !strings.HasPrefix(string(encrypted), "age-encryption.org/v1\n") || strings.Contains(string(encrypted), testDump) {
		t.Fatal("backend did not keep encrypted bytes")
	}
	source.fail = true
	failedID := uuid.NewString()
	if _, err := exports.Start(ctx, owner, database, failedID); err != nil {
		t.Fatal(err)
	}
	failedJob, err := exports.claim(ctx)
	if err != nil || failedJob.id != failedID {
		t.Fatalf("claim = %+v, %v", failedJob, err)
	}
	if err := exports.run(ctx, failedJob); !errors.Is(err, pgexport.ErrIncomplete) {
		t.Fatalf("failed job returned %v", err)
	}
	status, err := exports.Status(ctx, owner, org, database, failedID)
	if err != nil || status.State != "failed" || status.Manifest != nil {
		t.Fatalf("failed status = %+v, %v", status, err)
	}
	if _, err := backend.Get(ctx, storageKey(failedID, failedJob.token)); !storage.IsNotFound(err) {
		t.Fatalf("partial output survived: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE native_postgres_exports SET expires_at=clock_timestamp(); UPDATE native_postgres_export_objects SET expires_at=clock_timestamp()"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := exports.Open(ctx, owner, org, database, id); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("expired download = %v", err)
	}
	if err := exports.expire(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Get(ctx, storageKey(id, job.token)); !storage.IsNotFound(err) {
		t.Fatalf("expired object survived: %v", err)
	}
}
