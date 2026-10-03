//go:build !no_pg

// adr:375
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func (p *cloneSnapshotProvider) WithSnapshotCopyReaderDatabaseSQL(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderDatabaseSQLRequest, read managedpostgres.SnapshotCopyReaderSQLRead) error {
	p.readerSelectedSQLCalls++
	if err := r.Validate(d); err != nil {
		return err
	}
	if err := p.validateReader(ctx, d, r.Reader); err != nil {
		return err
	}
	if p.readerSelectedSQLConnect == nil {
		return managedpostgres.ErrUnavailable
	}
	conn, err := p.readerSelectedSQLConnect(ctx, r.Database.Database.Name)
	if err != nil {
		return managedpostgres.ErrUnavailable
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var id managedpostgres.SnapshotCopyReaderSQLIdentity
	if err := conn.QueryRow(ctx, `select pg_catalog.current_setting('server_version_num')::int/10000,pg_catalog.current_database(),current_user,d.oid,r.oid
        from pg_catalog.pg_database d,pg_catalog.pg_roles r where d.datname=pg_catalog.current_database() and r.rolname=current_user`).Scan(
		&id.PostgresMajor, &id.DatabaseName, &id.RoleName, &id.DatabaseOID, &id.RoleOID); err != nil {
		return managedpostgres.ErrUnavailable
	}
	p.readerSelectedSQLReadError = read(ctx, conn, id)
	if err := p.readerSelectedSQLReadError; err != nil {
		return err
	}
	p.readerSelectedSQLAfterCalls++
	return p.readerSelectedSQLAfterError
}

func cloneOwnedReaderArchiveFixture(t *testing.T) (cloneCoordinatorFixture, *cloneSnapshotProvider, capturedProjectEnvironmentDatabasePlan, copyinventory.ExportPlan,
	uint32, *age.X25519Identity, clonePostgresArchiveStorage, *archiveWorkerStorage, string) {
	t.Helper()
	tool := os.Getenv("FAAS_COPY_PG_DUMP")
	if tool == "" {
		t.Skip("FAAS_COPY_PG_DUMP required for owned-reader real export contracts")
	}
	f, store, p, sourceID := cloneReaderWorkerFixture(t, 16)
	var err error
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	name := "grg_selected /?%é\n" + uuid.NewString()[:8]
	if _, err := f.pool.Exec(t.Context(), "create database "+pgx.Identifier{name}.Sanitize()+" template template0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.pool.Exec(context.Background(), "drop database "+pgx.Identifier{name}.Sanitize()+" with (force)"); err != nil {
			t.Error(err)
		}
	})
	config := f.pool.Config().ConnConfig.Copy()
	config.Database = name
	// Provider connection material always includes a nonempty credential.
	// The local Unix-socket trust fixture does not check this password.
	config.Password = "private-fixture-password"
	conn, err := pgx.ConnectConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(t.Context(), "create table selected_events(id int primary key, note text); insert into selected_events values (7,'selected-reader-row')")
	_ = conn.Close(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	plans, err := f.srv.capturedProjectEnvironmentDatabasePlans(t.Context(), f.lease.Operation)
	if err != nil || len(plans) != 1 {
		t.Fatalf("frozen capture plans: %v", err)
	}
	cloneInventorySQLConnection(t, f, p)
	identity, _ := age.GenerateX25519Identity()
	previousRecipient, previousIdentities := setSecretRecipient, mfaIdentities
	setSecretRecipient = func() *age.X25519Recipient { return identity.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { setSecretRecipient, mfaIdentities = previousRecipient, previousIdentities })
	inventory, err := f.srv.readProjectEnvironmentClonePostgresInventory(t.Context(), f.lease, plans[0])
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := store.ProjectEnvironmentClonePostgresInventoryForLease(t.Context(), f.lease, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	exports, err := inventory.PlanExports(sealed.Sealed.Scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	requirements, err := exports.RequirementsForWorker()
	if err != nil {
		t.Fatal(err)
	}
	var oid uint32
	for _, d := range requirements {
		if d.Database.Name == name {
			if d.AuthenticatedReaderDatabase {
				t.Fatal("fixture must select a different database from the inventory connection")
			}
			oid = d.Database.OID
		}
	}
	if oid == 0 {
		t.Fatal("selected database was omitted from frozen inventory")
	}
	p.readerSelectedSQLConnect = func(ctx context.Context, selected string) (*pgx.Conn, error) {
		if selected != name {
			return nil, managedpostgres.ErrConflict
		}
		cfg := config.Copy()
		cfg.Database = selected
		cfg.RuntimeParams = map[string]string{"default_transaction_read_only": "on", "search_path": "pg_catalog"}
		return pgx.ConnectConfig(ctx, cfg)
	}
	local, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := &archiveWorkerStorage{StorageBackend: local}
	artifact := clonePostgresArchiveStorage{ID: "reader-archives", Fingerprint: strings.Repeat("f", 64), Backend: b}
	return f, p, plans[0], exports, oid, identity, artifact, b, tool
}

func TestPGClonePostgresArchiveOwnedReaderExportsSelectedDatabaseAndRecoversWithoutSQL(t *testing.T) {
	f, p, source, exports, oid, key, artifact, b, tool := cloneOwnedReaderArchiveFixture(t)
	receipt, err := f.srv.projectEnvironmentClonePostgresArchiveFromReader(t.Context(), f.lease, source, exports, oid, artifact, 8<<20, archiveWorkerLimits(), tool, 4<<20)
	if err != nil || receipt.PlainBytes < 1000 || p.readerSelectedSQLCalls != 1 || p.readerSQLCalls != 1 || b.puts != 1 {
		t.Fatalf("owned selected real dump: %v (export=%v, selected=%d, after=%d, puts=%d)", err, p.readerSelectedSQLReadError, p.readerSelectedSQLCalls, p.readerSelectedSQLAfterCalls, b.puts)
	}
	archives := f.srv.store.(state.ProjectEnvironmentClonePostgresArchiveStore)
	owner, err := archives.ProjectEnvironmentClonePostgresArchiveForLease(t.Context(), f.lease, source.source.ID, oid)
	if err != nil {
		t.Fatal(err)
	}
	requirements, _ := exports.RequirementsForWorker()
	var selected copyinventory.DatabaseExport
	for _, d := range requirements {
		if d.Database.OID == oid {
			selected = d
		}
	}
	cipher, err := b.StorageBackend.Get(t.Context(), owner.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := copyarchive.Open([]*age.X25519Identity{key}, selected, cipher)
	if err != nil {
		t.Fatal(err)
	}
	dump, err := io.ReadAll(plain)
	_ = cipher.Close()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), filepath.Join(filepath.Dir(tool), "pg_restore"), "--file=-")
	cmd.Stdin = bytes.NewReader(dump)
	sql, err := cmd.Output()
	if err != nil || !bytes.Contains(sql, []byte("selected-reader-row")) || !bytes.Contains(sql, []byte("selected_events")) {
		t.Fatalf("dump did not contain the selected database: %v", err)
	}
	f.srv.managedPostgres = nil
	recovered, err := f.srv.projectEnvironmentClonePostgresArchiveFromReader(t.Context(), f.lease, source, exports, oid, artifact, 1, archiveWorkerLimits(), "", 0)
	if err != nil || !copyarchive.SameReceipt(receipt, recovered) || p.readerSelectedSQLCalls != 1 || b.puts != 1 {
		t.Fatalf("retained recovery redumped/reconnected: %v", err)
	}
	if f.lease.Operation.Resources[0].TargetID != "" || f.lease.Operation.Resources[0].Status != "captured" {
		t.Fatal("selected dump supplied dataset readiness")
	}
}

func TestPGClonePostgresArchiveOwnedReaderPostDumpFailureCannotPublishAndRetainsUnknownWrite(t *testing.T) {
	f, p, source, exports, oid, _, artifact, b, tool := cloneOwnedReaderArchiveFixture(t)
	p.readerSelectedSQLAfterError = managedpostgres.ErrConflict
	if r, err := f.srv.projectEnvironmentClonePostgresArchiveFromReader(t.Context(), f.lease, source, exports, oid, artifact, 8<<20, archiveWorkerLimits(), tool, 4<<20); !errors.Is(err, managedpostgres.ErrUnavailable) || r != (copyarchive.Receipt{}) || p.readerSelectedSQLCalls != 1 || p.readerSelectedSQLAfterCalls != 1 || p.readerSelectedSQLReadError != nil {
		t.Fatalf("post-dump provider rejection supplied receipt: %v", err)
	}
	archives := f.srv.store.(state.ProjectEnvironmentClonePostgresArchiveStore)
	owner, err := archives.ProjectEnvironmentClonePostgresArchiveForLease(t.Context(), f.lease, source.source.ID, oid)
	if err != nil || owner.State != "uploading" || owner.ReservedBytes != 8<<20 {
		t.Fatalf("failed upload released ownership: %v", err)
	}
	if stream, err := b.StorageBackend.Get(t.Context(), owner.StorageKey); !storage.IsNotFound(err) {
		if stream != nil {
			_ = stream.Close()
		}
		t.Fatalf("failed final placement check published ciphertext: %v", err)
	}
	p.readerSelectedSQLAfterError = nil
	if _, err := f.srv.projectEnvironmentClonePostgresArchiveFromReader(t.Context(), f.lease, source, exports, oid, artifact, 8<<20, archiveWorkerLimits(), tool, 4<<20); !errors.Is(err, managedpostgres.ErrUnavailable) || p.readerSelectedSQLCalls != 1 || b.puts != 1 {
		t.Fatalf("uncertain upload redumped source: %v", err)
	}
}
