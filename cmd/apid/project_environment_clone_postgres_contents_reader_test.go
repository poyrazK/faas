//go:build !no_pg

// adr:375
package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/state"
)

type contentsReaderProvider struct {
	*cloneSnapshotProvider
	placementChecks int
	borrowed        *pgx.Conn
	onPlacement     func(context.Context, int) error
	onAfterSQL      func(context.Context) error
}

func (p *contentsReaderProvider) FindSnapshotCopyReader(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderRequest) (managedpostgres.SnapshotCopyReaderObservation, error) {
	p.placementChecks++
	actual, err := p.cloneSnapshotProvider.FindSnapshotCopyReader(ctx, d, r)
	if err == nil && p.onPlacement != nil {
		err = p.onPlacement(ctx, p.placementChecks)
	}
	return actual, err
}

func (p *contentsReaderProvider) WithSnapshotCopyReaderDatabaseSQL(ctx context.Context, d managedpostgres.RestoreSourceDefinition, r managedpostgres.SnapshotCopyReaderDatabaseSQLRequest, read managedpostgres.SnapshotCopyReaderSQLRead) error {
	err := p.cloneSnapshotProvider.WithSnapshotCopyReaderDatabaseSQL(ctx, d, r,
		func(ctx context.Context, conn *pgx.Conn, id managedpostgres.SnapshotCopyReaderSQLIdentity) error {
			p.borrowed = conn
			return read(ctx, conn, id)
		})
	if err == nil && p.onAfterSQL != nil {
		err = p.onAfterSQL(ctx)
	}
	return err
}

type contentsReaderFixture struct {
	x      *contentsWorkerFixture
	p      *contentsReaderProvider
	source capturedProjectEnvironmentDatabasePlan
	cfg    copycontents.Config
}

func newContentsReaderFixture(t *testing.T) *contentsReaderFixture {
	t.Helper()
	x := newContentsWorkerFixture(t)
	plans, err := x.f.srv.capturedProjectEnvironmentDatabasePlans(t.Context(), x.f.lease.Operation)
	if err != nil || len(plans) != 1 {
		t.Fatal("original source plan", err)
	}
	p := &contentsReaderProvider{cloneSnapshotProvider: x.provider}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"}, MaxDatabasesPerAccount: 3,
		Backends: []managedpostgres.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "snapshot-worker"}}}, func(string) string { return "" },
		map[string]managedpostgres.Factory{"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return p, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	databases, err := managedpostgres.NewPostgresStore(x.f.pool)
	if err != nil {
		t.Fatal(err)
	}
	// Reading an already owned capture does not require creation to be enabled.
	x.f.srv.managedPostgres, err = managedpostgres.NewService(registry, databases, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	return &contentsReaderFixture{x: x, p: p, source: plans[0], cfg: copycontents.Config{SpoolDir: t.TempDir(), MaxBytes: 16 << 20, SortMemoryBytes: 64, SortDiskBytes: 1 << 20}}
}

func (f *contentsReaderFixture) capture(ctx context.Context) (copycontents.Manifest, error) {
	return f.x.f.srv.projectEnvironmentClonePostgresContentsFromReader(ctx, f.x.f.lease, f.source, f.x.plan, f.x.d.Database.OID,
		api.PostgresCopyCiphertextMaxBytes, contentsWorkerLimits(), f.cfg)
}

func (f *contentsReaderFixture) reserve(t *testing.T) state.ProjectEnvironmentClonePostgresContents {
	t.Helper()
	x := f.x
	a, _, err := x.store.ReserveProjectEnvironmentClonePostgresContents(t.Context(), x.f.lease, state.ProjectEnvironmentClonePostgresContentsRequest{
		Scope: x.d.Scope, DatabaseOID: x.d.Database.OID, InventoryFingerprint: x.d.InventoryFingerprint, KeyID: x.identity.Recipient().String(), ReservedBytes: api.PostgresCopyCiphertextMaxBytes}, contentsWorkerLimits())
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (f *contentsReaderFixture) unpublished(t *testing.T, original state.ProjectEnvironmentClonePostgresContents) {
	t.Helper()
	var id, status, key string
	var held int64
	var empty bool
	if err := f.x.f.pool.QueryRow(t.Context(), `SELECT owner_id::text,state,key_id,reserved_bytes,ciphertext IS NULL AND captured_at IS NULL
 FROM project_environment_clone_postgres_contents WHERE operation_id=$1 AND source_database_id=$2 AND database_oid=$3`,
		original.Scope.OperationID, original.Scope.SourceDatabaseID, int64(original.DatabaseOID)).Scan(&id, &status, &key, &held, &empty); err != nil {
		t.Fatal(err)
	}
	if id != original.OwnerID || status != "reserved" || key != original.KeyID || held != original.ReservedBytes || !empty {
		t.Fatal("failed read changed the original charged reservation")
	}
	entries, err := os.ReadDir(f.cfg.SpoolDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("contents spool not retired", err)
	}
	if f.p.borrowed != nil && !f.p.borrowed.IsClosed() {
		t.Fatal("borrowed SQL connection escaped capture")
	}
}

func TestPGClonePostgresContentsOwnedReaderCapturesSelectedAndRecoversWithoutSource(t *testing.T) {
	f := newContentsReaderFixture(t)
	x := f.x
	x.store.loseRecord = true
	if m, err := f.capture(t.Context()); !errors.Is(err, managedpostgres.ErrUnavailable) || m.Fingerprint() != "" || f.p.readerSelectedSQLCalls != 1 || f.p.readerSelectedSQLAfterCalls != 1 || f.p.placementChecks < 8 {
		t.Fatal("owned selected capture / lost publication response", err)
	}
	first := x.owner(t)
	if first.State != "captured" || f.p.borrowed == nil || !f.p.borrowed.IsClosed() {
		t.Fatal("completed source read was not privately retained and closed")
	}
	old := x.f.lease
	if err := x.store.ReleaseProjectEnvironmentCloneLease(t.Context(), old, 0); err != nil {
		t.Fatal(err)
	}
	var err error
	x.f.lease, err = x.store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	rotated, _ := age.GenerateX25519Identity()
	setSecretRecipient = func() *age.X25519Recipient { return rotated.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{rotated, x.identity} }
	checks := f.p.placementChecks
	f.source, f.cfg = capturedProjectEnvironmentDatabasePlan{}, copycontents.Config{}
	x.f.srv.managedPostgres = nil
	m, err := f.capture(t.Context())
	if err != nil || m.Fingerprint() != first.Sealed.Fingerprint || f.p.placementChecks != checks || f.p.readerSelectedSQLCalls != 1 {
		t.Fatal("recovery reconnected or needed current source/spool", err)
	}
	recovered := x.owner(t)
	if recovered.OwnerID != first.OwnerID || recovered.KeyID != first.KeyID || !bytes.Equal(recovered.Sealed.Ciphertext, first.Sealed.Ciphertext) || !recovered.CapturedAt.Equal(first.CapturedAt) {
		t.Fatal("recovery replaced original ciphertext")
	}
	if _, err := x.store.ProjectEnvironmentClonePostgresContentsForLease(t.Context(), old, x.d.Scope.SourceDatabaseID, x.d.Database.OID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale worker recovered contents", err)
	}
	for _, resource := range x.f.lease.Operation.Resources {
		if resource.Status != "captured" || resource.TargetID != "" {
			t.Fatal("source manifest supplied stage readiness")
		}
	}
}

func TestPGClonePostgresContentsOwnedReaderPostcheckFailureKeepsReservation(t *testing.T) {
	f := newContentsReaderFixture(t)
	original := f.reserve(t)
	f.p.readerSelectedSQLAfterError = managedpostgres.ErrUnavailable
	if m, err := f.capture(t.Context()); !errors.Is(err, managedpostgres.ErrUnavailable) || m.Fingerprint() != "" || f.p.readerSelectedSQLReadError != nil || f.p.readerSelectedSQLAfterCalls != 1 {
		t.Fatal("provider rejected completed Capture but it escaped", err)
	}
	f.unpublished(t, original)
	f.p.readerSelectedSQLAfterError = nil
	if m, err := f.capture(t.Context()); err != nil || m.Fingerprint() == "" || f.p.readerSelectedSQLCalls != 2 {
		t.Fatal("original retained reader could not resume", err)
	}
	if final := f.x.owner(t); final.OwnerID != original.OwnerID || final.KeyID != original.KeyID || final.ReservedBytes != original.ReservedBytes {
		t.Fatal("resumed capture changed ownership")
	}
}

func TestPGClonePostgresContentsOwnedReaderRejectsSubstitutionBeforeContentsRead(t *testing.T) {
	for _, mode := range []string{"source_version", "source_backend", "capture_pin", "reader_pin", "archive_pin", "provider_absent", "provider_birth", "provider_unavailable", "sql_identity"} {
		t.Run(mode, func(t *testing.T) {
			f := newContentsReaderFixture(t)
			original := f.reserve(t)
			x := f.x
			var err error
			switch mode {
			case "source_version":
				f.source.hash = strings.Repeat("e", 64)
			case "source_backend":
				f.source.source.BackendFingerprint = strings.Repeat("d", 64)
			case "capture_pin":
				_, err = x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_snapshot_restores SET target_provider_resource_id='replaced-capture' WHERE operation_id=$1", x.f.lease.Operation.ID)
			case "reader_pin":
				_, err = x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_copy_readers SET endpoint_id='replaced-reader' WHERE operation_id=$1", x.f.lease.Operation.ID)
			case "archive_pin":
				_, err = x.f.pool.Exec(t.Context(), "UPDATE project_environment_clone_postgres_archives SET storage_fingerprint=$2 WHERE operation_id=$1", x.f.lease.Operation.ID, strings.Repeat("b", 64))
			case "provider_absent":
				f.p.hideReader = true
			case "provider_birth":
				f.p.readerActual.CreatedAt = f.p.readerActual.CreatedAt.Add(time.Microsecond)
			case "provider_unavailable":
				f.p.readerActual.Available = false
			case "sql_identity":
				f.p.readerSelectedSQLConnect = func(ctx context.Context, _ string) (*pgx.Conn, error) {
					cfg := x.f.pool.Config().ConnConfig.Copy()
					cfg.RuntimeParams = map[string]string{"default_transaction_read_only": "on"}
					return pgx.ConnectConfig(ctx, cfg)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if m, err := f.capture(t.Context()); err == nil || m.Fingerprint() != "" {
				t.Fatal("substituted original reader supplied contents", err)
			}
			wantSQL := 0
			if mode == "sql_identity" {
				wantSQL = 1
			}
			if f.p.readerSelectedSQLCalls != wantSQL || f.p.readerSelectedSQLAfterCalls != 0 {
				t.Fatal("contents read passed substituted SQL/provider authority")
			}
			f.unpublished(t, original)
		})
	}
}

func TestPGClonePostgresContentsOwnedReaderRechecksDuringReadAndClosesOnLoss(t *testing.T) {
	for _, mode := range []string{"handoff", "reader_unavailable", "native_pin", "provider_replaced", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := newContentsReaderFixture(t)
			original := f.reserve(t)
			x := f.x
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			changed := false
			f.p.onPlacement = func(ctx context.Context, n int) error {
				if n != 5 {
					return nil
				}
				if f.p.borrowed == nil || f.p.borrowed.PgConn().TxStatus() != 'T' {
					t.Fatal("loss injection must follow a real row read inside Capture")
				}
				changed = true
				switch mode {
				case "handoff":
					if err := x.store.ReleaseProjectEnvironmentCloneLease(ctx, x.f.lease, 0); err != nil {
						return err
					}
					var err error
					x.f.lease, err = x.store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
					return err
				case "reader_unavailable":
					r, err := x.store.ProjectEnvironmentClonePostgresCopyReaderForLease(ctx, x.f.lease, x.d.Scope.SourceDatabaseID)
					if err != nil {
						return err
					}
					_, err = x.store.RecordProjectEnvironmentClonePostgresCopyReader(ctx, x.f.lease, x.d.Scope.SourceDatabaseID,
						state.ProjectEnvironmentClonePostgresCopyReaderObservation{EndpointID: r.EndpointID, CreatedAt: r.EndpointCreatedAt, Available: false})
					return err
				case "native_pin":
					_, err := x.f.pool.Exec(ctx, "UPDATE project_environment_clone_postgres_snapshot_restores SET target_provider_resource_id='changed-mid-read' WHERE operation_id=$1", x.f.lease.Operation.ID)
					return err
				case "provider_replaced":
					f.p.readerActual.EndpointID = "replaced-mid-read"
				case "cancel":
					cancel()
				}
				return nil
			}
			if m, err := f.capture(ctx); err == nil || m.Fingerprint() != "" || !changed || f.p.readerSelectedSQLCalls != 1 {
				t.Fatal("authority loss published contents", err)
			}
			f.unpublished(t, original)
		})
	}
}

func TestPGClonePostgresContentsOwnedReaderConcurrentFirstCaptureWins(t *testing.T) {
	f := newContentsReaderFixture(t)
	x := f.x
	original := f.reserve(t)
	var first copycontents.Sealed
	f.p.onPlacement = func(ctx context.Context, n int) error {
		if n != 1 {
			return nil
		}
		key := [32]byte{7}
		m, err := x.read(ctx, x.d, key)
		if err != nil {
			return err
		}
		first, err = copycontents.Seal(x.identity.Recipient(), m)
		if err == nil {
			_, err = x.store.RecordProjectEnvironmentClonePostgresContents(ctx, x.f.lease, x.d.Scope.SourceDatabaseID, x.d.Database.OID, first)
		}
		return err
	}
	if m, err := f.capture(t.Context()); !errors.Is(err, managedpostgres.ErrConflict) || m.Fingerprint() != "" || *x.reads != 1 || f.p.readerSelectedSQLCalls != 1 {
		t.Fatal("read raced a committed first manifest", err)
	}
	f.p.onPlacement = nil
	f.cfg = copycontents.Config{}
	m, err := f.capture(t.Context())
	if err != nil || m.Fingerprint() != first.Fingerprint || *x.reads != 1 || f.p.readerSelectedSQLCalls != 1 {
		t.Fatal("retry did not recover concurrent original capture", err)
	}
	if a := x.owner(t); a.OwnerID != original.OwnerID || !bytes.Equal(a.Sealed.Ciphertext, first.Ciphertext) {
		t.Fatal("race overwrote first ciphertext")
	}
}

func TestPGClonePostgresContentsOwnedReaderRejectsAuthorityLossAfterProviderPostcheck(t *testing.T) {
	for _, mode := range []string{"handoff", "provider_unavailable", "reader_unavailable"} {
		t.Run(mode, func(t *testing.T) {
			f := newContentsReaderFixture(t)
			original := f.reserve(t)
			x := f.x
			checked := false
			f.p.onAfterSQL = func(ctx context.Context) error {
				if f.p.readerSelectedSQLReadError != nil || f.p.readerSelectedSQLAfterCalls != 1 || f.p.borrowed == nil || !f.p.borrowed.IsClosed() {
					t.Fatal("loss must follow successful Capture, SQL close and provider postcheck")
				}
				checked = true
				switch mode {
				case "handoff":
					if err := x.store.ReleaseProjectEnvironmentCloneLease(ctx, x.f.lease, 0); err != nil {
						return err
					}
					var err error
					x.f.lease, err = x.store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
					return err
				case "provider_unavailable":
					f.p.readerActual.Available = false
				case "reader_unavailable":
					r, err := x.store.ProjectEnvironmentClonePostgresCopyReaderForLease(ctx, x.f.lease, x.d.Scope.SourceDatabaseID)
					if err != nil {
						return err
					}
					_, err = x.store.RecordProjectEnvironmentClonePostgresCopyReader(ctx, x.f.lease, x.d.Scope.SourceDatabaseID,
						state.ProjectEnvironmentClonePostgresCopyReaderObservation{EndpointID: r.EndpointID, CreatedAt: r.EndpointCreatedAt, Available: false})
					return err
				}
				return nil
			}
			if m, err := f.capture(t.Context()); err == nil || m.Fingerprint() != "" || !checked || f.p.readerSelectedSQLCalls != 1 {
				t.Fatal("post-provider authority loss supplied contents", err)
			}
			f.unpublished(t, original)
		})
	}
}

func TestPGClonePostgresContentsOwnedReaderBudgetFailureRetiresOnlyReadResources(t *testing.T) {
	for _, mode := range []string{"read_bytes", "sort_disk", "invalid_config"} {
		t.Run(mode, func(t *testing.T) {
			f := newContentsReaderFixture(t)
			original := f.reserve(t)
			cfg := f.x.f.pool.Config().ConnConfig.Copy()
			cfg.Database = f.x.d.Database.Name
			cfg.RuntimeParams = nil
			conn, err := pgx.ConnectConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			_, err = conn.Exec(t.Context(), "INSERT INTO selected_events SELECT i,'spool-budget-row' FROM generate_series(20,35) i")
			_ = conn.Close(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			want := managedpostgres.ErrQuotaExceeded
			switch mode {
			case "read_bytes":
				f.cfg.MaxBytes = 1
			case "sort_disk":
				f.cfg.SortMemoryBytes, f.cfg.SortDiskBytes = 32, 32
			case "invalid_config":
				f.cfg.SortMemoryBytes = 1
				want = managedpostgres.ErrInvalid
			}
			if m, err := f.capture(t.Context()); !errors.Is(err, want) || m.Fingerprint() != "" || f.p.readerSelectedSQLCalls != 1 {
				t.Fatal("bounded reader returned contents", err)
			}
			f.unpublished(t, original)
			if f.p.readerDeletes != 0 || f.p.forkDeletes != 0 || f.p.deletes != 0 {
				t.Fatal("temporary read failure disposed retained original inputs")
			}
		})
	}
}
