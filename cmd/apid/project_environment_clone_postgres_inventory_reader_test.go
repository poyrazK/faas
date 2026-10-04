//go:build !no_pg

// adr: 583
package main

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

func (p *cloneSnapshotProvider) WithSnapshotCopyReaderSQL(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderRequest, read managedpostgres.SnapshotCopyReaderSQLRead) error {
	p.readerSQLCalls++
	if err := p.validateReader(ctx, d, r); err != nil {
		return err
	}
	if p.readerSQLConn == nil {
		return managedpostgres.ErrUnavailable
	}
	defer func(cleanupCtx context.Context) { _ = p.readerSQLConn.Close(context.WithoutCancel(cleanupCtx)) }(ctx)
	return read(ctx, p.readerSQLConn, p.readerSQLIdentity)
}

type cloneReaderInventoryFailureStore struct {
	*cloneReaderFailureStore
	loseInventory bool
}

func (s *cloneReaderInventoryFailureStore) RecordProjectEnvironmentClonePostgresInventory(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, sealed copyinventory.Sealed) (state.ProjectEnvironmentClonePostgresInventory, bool, error) {
	r, created, err := s.PgStore.RecordProjectEnvironmentClonePostgresInventory(ctx, l, id, sealed)
	if err == nil && s.loseInventory {
		s.loseInventory = false
		return r, created, errors.New("committed SQL inventory reply lost")
	}
	return r, created, err
}

func cloneInventorySQLConnection(t *testing.T, f cloneCoordinatorFixture, p *cloneSnapshotProvider) {
	t.Helper()
	config := f.pool.Config().ConnConfig.Copy()
	config.RuntimeParams = map[string]string{"default_transaction_read_only": "on", "search_path": "pg_catalog"}
	var err error
	p.readerSQLConn, err = pgx.ConnectConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.readerSQLConn.Close(context.Background()) })
	p.readerSQLIdentity.DatabaseName, p.readerSQLIdentity.RoleName = config.Database, config.User
	if err := p.readerSQLConn.QueryRow(t.Context(), "select current_setting('server_version_num')::int/10000,d.oid,r.oid from pg_database d,pg_roles r where d.datname=current_database() and r.rolname=current_user").Scan(
		&p.readerSQLIdentity.PostgresMajor, &p.readerSQLIdentity.DatabaseOID, &p.readerSQLIdentity.RoleOID); err != nil {
		t.Fatal(err)
	}
}

func TestPGClonePostgresSnapshotInventoryOwnedReaderSealsSQLAndRecoversCommittedReply(t *testing.T) {
	f, original, p, sourceID := cloneReaderWorkerFixture(t, 16)
	store := &cloneReaderInventoryFailureStore{cloneReaderFailureStore: original, loseInventory: true}
	f.srv.store = store
	var err error
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	cloneInventorySQLConnection(t, f, p)
	plans, err := f.srv.capturedProjectEnvironmentDatabasePlans(t.Context(), f.lease.Operation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.readProjectEnvironmentClonePostgresInventory(t.Context(), f.lease, plans[0]); err == nil || p.readerSQLCalls != 1 || !p.readerSQLConn.IsClosed() {
		t.Fatalf("committed inventory reply loss: %v", err)
	}
	receipt, err := store.ProjectEnvironmentClonePostgresInventoryForLease(t.Context(), f.lease, sourceID)
	if err != nil || receipt.Sealed.CiphertextSHA256 == "" || receipt.Sealed.Scope.CaptureProviderResourceID != p.readerRequest.Capture.ExpectedTargetResourceID {
		t.Fatalf("SQL inventory lost capture identity: %+v %v", receipt.Sealed.Scope, err)
	}
	f.srv.managedPostgres = nil
	inventory, err := f.srv.readProjectEnvironmentClonePostgresInventory(t.Context(), f.lease, plans[0])
	if err != nil || p.readerSQLCalls != 1 || inventory.Summary().Fingerprint != receipt.Sealed.Fingerprint || inventory.Summary().Databases == 0 || inventory.Summary().Roles == 0 {
		t.Fatalf("sealed recovery contacted SQL or recaptured: %+v %v", inventory.Summary(), err)
	}
	if f.lease.Operation.Resources[0].TargetID != "" || f.lease.Operation.Resources[0].Status != "captured" || p.deadlineMissing {
		t.Fatal("cluster inventory authorized dataset publication or lost deadline")
	}
}

func TestPGClonePostgresSnapshotInventoryOwnedReaderRejectsUnobservedAndWrongMajorSQL(t *testing.T) {
	f, _, p, _ := cloneReaderWorkerFixture(t, 16)
	p.readerPending = true
	var err error
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := f.srv.capturedProjectEnvironmentDatabasePlans(t.Context(), f.lease.Operation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.readProjectEnvironmentClonePostgresInventory(t.Context(), f.lease, plans[0]); !errors.Is(err, managedpostgres.ErrConflict) || p.readerSQLCalls != 0 {
		t.Fatalf("unavailable reader reached SQL: %v", err)
	}
	p.readerActual.Available = true
	f.lease, _, err = f.srv.prepareProjectEnvironmentClonePostgresCopyReaders(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	cloneInventorySQLConnection(t, f, p)
	p.readerSQLIdentity.PostgresMajor++
	if _, err := f.srv.readProjectEnvironmentClonePostgresInventory(t.Context(), f.lease, plans[0]); !errors.Is(err, managedpostgres.ErrConflict) || p.readerSQLCalls != 1 || !p.readerSQLConn.IsClosed() {
		t.Fatalf("wrong major SQL supplied inventory: %v", err)
	}
}
