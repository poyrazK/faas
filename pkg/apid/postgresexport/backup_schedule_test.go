package postgresexport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/pgexport"
	"github.com/onebox-faas/faas/pkg/pgextension"
	"github.com/onebox-faas/faas/pkg/storage"
)

type scheduledDumpSource struct {
	database managedpostgres.Database
	source   *pgxpool.Pool
	captures int
}

func (s *scheduledDumpSource) ExportSource(ctx context.Context, owner, id string) (managedpostgres.Database, error) {
	if id != s.database.ID || owner != s.database.AccountID || managedpostgres.Organization(ctx) != s.database.OrgID {
		return managedpostgres.Database{}, managedpostgres.ErrNotFound
	}
	return s.database, nil
}

func (s *scheduledDumpSource) ExportDatabase(ctx context.Context, owner, id string, generation int64, dst io.Writer) (pgexport.Source, error) {
	if _, err := s.ExportSource(ctx, owner, id); err != nil || generation != s.database.ObservedGeneration {
		return pgexport.Source{}, managedpostgres.ErrNotFound
	}
	s.captures++
	cmd := backupFixtureCommand(ctx, s.source, "pg_dump", "--format=custom", "--no-owner", "--no-acl")
	var stderr bytes.Buffer
	cmd.Stderr, cmd.Stdout = &stderr, dst
	if err := cmd.Run(); err != nil {
		return pgexport.Source{}, fmt.Errorf("pg_dump: %w: %s", err, stderr.String())
	}
	metadata := pgexport.Source{CapturedAt: time.Now(), Extensions: []pgextension.Installed{}}
	err := s.source.QueryRow(ctx, `SELECT current_setting('server_version_num')::int / 10000,
		pg_database_size(current_database())`).Scan(&metadata.Major, &metadata.DatabaseBytes)
	return metadata, err
}

func backupFixtureCommand(ctx context.Context, database *pgxpool.Pool, executable string, options ...string) *exec.Cmd {
	cfg := database.Config().ConnConfig
	args := []string{"--host", cfg.Host, "--port", fmt.Sprint(cfg.Port), "--username", cfg.User, "--dbname", cfg.Database, "--no-password"}
	cmd := exec.CommandContext(ctx, executable, append(args, options...)...)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+cfg.Password)
	return cmd
}

type scheduledBackupObjects struct {
	dest    BackupDestination
	objects map[string][]byte
	puts    int
	lostACK bool
}

func (b *scheduledBackupObjects) CheckBackupDestination(_ context.Context, dest BackupDestination) error {
	if b.dest != dest {
		return managedpostgres.ErrNotFound
	}
	return nil
}

func (b *scheduledBackupObjects) PutBackupObject(ctx context.Context, dest BackupDestination, key, mediaType, digest string, size int64, body io.Reader) error {
	if err := b.CheckBackupDestination(ctx, dest); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(body, size+1))
	sum := sha256.Sum256(data)
	if err != nil || int64(len(data)) != size || hex.EncodeToString(sum[:]) != digest {
		return pgexport.ErrIncomplete
	}
	if old, exists := b.objects[key]; exists {
		if !bytes.Equal(old, data) {
			return managedpostgres.ErrConflict
		}
		return nil
	}
	b.objects[key] = data
	b.puts++
	if mediaType == "application/json" && !b.lostACK {
		b.lostACK = true
		return managedpostgres.ErrUnavailable
	}
	return nil
}

func TestScheduledBackupRetryRestoresPortableArchive(t *testing.T) {
	catalog := pgtest.OpenDatabase(t)
	sourceDB := pgtest.OpenDatabase(t)
	restored := pgtest.OpenDatabase(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if _, err := sourceDB.Exec(ctx, "CREATE TABLE orders (id integer PRIMARY KEY, description text); INSERT INTO orders VALUES (7, 'paid')"); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Exec(ctx, "CREATE TABLE managed_postgres_databases (id uuid PRIMARY KEY, account_id uuid NOT NULL, org_id uuid NOT NULL, state text NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"20261126083649234_native_postgres_exports.sql", "20261126083649616_native_postgres_backup_schedules.sql"} {
		raw, err := os.ReadFile("../../../migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(raw), "-- +goose Down")
		if _, err := catalog.Exec(ctx, up); err != nil {
			t.Fatal(err)
		}
	}
	owner, org, database := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := catalog.Exec(ctx, "INSERT INTO managed_postgres_databases VALUES ($1, $2, $3, 'ready')", database, owner, org); err != nil {
		t.Fatal(err)
	}
	ctx = managedpostgres.WithOwnership(ctx, org, owner)
	driver := &scheduledDumpSource{source: sourceDB, database: managedpostgres.Database{
		AccountID: owner, OrgID: org, ID: database, ObservedGeneration: 4,
	}}
	staging, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	exports := New(catalog, staging, driver, identity.Recipient, func() []*age.X25519Identity { return []*age.X25519Identity{identity} })
	dest := BackupDestination{OrgID: org, BucketAccountID: uuid.NewString(), BucketAppID: uuid.NewString(), BucketID: uuid.NewString()}
	objects := &scheduledBackupObjects{dest: dest, objects: map[string][]byte{}}
	schedules := NewBackupSchedules(catalog, exports, objects)

	schedule, err := schedules.Set(ctx, owner, database, "03:00", dest)
	if err != nil || schedule.State != "scheduled" || schedule.NextRunAt.UTC().Format("15:04") != "03:00" {
		t.Fatalf("set schedule: %+v %v", schedule, err)
	}
	if _, err := schedules.Get(ctx, owner, uuid.NewString(), database); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("unrelated organization read: %v", err)
	}
	foreign := dest
	foreign.OrgID = uuid.NewString()
	if _, err := schedules.Set(ctx, owner, database, "03:00", foreign); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("unrelated destination accepted: %v", err)
	}
	if _, err := catalog.Exec(ctx, "UPDATE native_postgres_backup_schedules SET next_run_at = now() - interval '1 day', retry_at = now() WHERE database_id=$1", database); err != nil {
		t.Fatal(err)
	}
	sweepNow := func() {
		t.Helper()
		if _, err := catalog.Exec(ctx, "UPDATE native_postgres_backup_schedules SET retry_at = now() WHERE database_id=$1", database); err != nil {
			t.Fatal(err)
		}
		if err := schedules.sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	sweepNow()
	claim, err := exports.claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := exports.run(ctx, claim); err != nil {
		t.Fatal(err)
	}
	sweepNow()
	pending, err := schedules.Get(ctx, owner, org, database)
	if err != nil || pending.LastSuccessAt != nil || pending.ActiveRunID != claim.id || pending.LastError != "delivery_failed" {
		t.Fatalf("uncertain manifest commit: %+v %v", pending, err)
	}
	schedules = NewBackupSchedules(catalog, exports, objects)
	replay, err := schedules.Set(ctx, owner, database, "03:00", dest)
	if err != nil || replay.ActiveRunID != claim.id {
		t.Fatalf("configuration replay reset the run: %+v %v", replay, err)
	}
	sweepNow()
	complete, err := schedules.Get(ctx, owner, org, database)
	if err != nil || complete.LastSuccessAt == nil || complete.State != "scheduled" || complete.LastError != "" || !complete.NextRunAt.After(time.Now()) {
		t.Fatalf("completed schedule: %+v %v", complete, err)
	}
	if len(objects.objects) != 2 || objects.puts != 2 || driver.captures != 1 {
		t.Fatalf("duplicate backup: objects=%d puts=%d captures=%d", len(objects.objects), objects.puts, driver.captures)
	}
	var manifest api.ManagedPostgresExportManifest
	archive := objects.objects[complete.LastArchive]
	sum := sha256.Sum256(archive)
	if err := json.Unmarshal(objects.objects[complete.LastArchive+".manifest.json"], &manifest); err != nil ||
		manifest.SizeBytes != int64(len(archive)) || manifest.SHA256 != hex.EncodeToString(sum[:]) ||
		!manifest.ArchiveManifest().VerifyReceipt(manifest.RestoreReceipt, identity.String()) {
		t.Fatalf("published manifest mismatch: %+v %v", manifest, err)
	}
	restore := backupFixtureCommand(ctx, restored, "pg_restore", "--exit-on-error", "--no-owner", "--no-acl")
	restore.Stdin = bytes.NewReader(archive)
	if output, err := restore.CombinedOutput(); err != nil {
		t.Fatalf("restore scheduled archive: %v: %s", err, output)
	}
	var description string
	if err := restored.QueryRow(ctx, "SELECT description FROM orders WHERE id=7").Scan(&description); err != nil || description != "paid" {
		t.Fatalf("restored row: %q %v", description, err)
	}
	if err := schedules.Delete(ctx, owner, org, database); err != nil {
		t.Fatal(err)
	}
	if _, err := schedules.Get(ctx, owner, org, database); !errors.Is(err, managedpostgres.ErrNotFound) || len(objects.objects) != 2 {
		t.Fatalf("schedule deletion affected backup objects: %v", err)
	}
}
