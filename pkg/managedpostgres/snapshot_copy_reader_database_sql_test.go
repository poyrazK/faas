// adr: 583
package managedpostgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
)

type serviceReaderDatabaseSQLProvider struct {
	serviceReaderSQLProvider
	selectedCalls int
	selected      SnapshotCopyReaderDatabaseSQLRequest
	fault         string
}

func (p *serviceReaderDatabaseSQLProvider) WithSnapshotCopyReaderDatabaseSQL(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyReaderDatabaseSQLRequest, read SnapshotCopyReaderSQLRead) error {
	p.selectedCalls++
	p.definition, p.selected = d, r
	_, p.deadline = ctx.Deadline()
	x := r.Database
	id := SnapshotCopyReaderSQLIdentity{PostgresMajor: d.Spec.PostgresMajor, DatabaseName: x.Database.Name, DatabaseOID: x.Database.OID, RoleName: "reader", RoleOID: x.AuthenticatedReaderRoleOID}
	switch p.fault {
	case "database_oid":
		id.DatabaseOID++
	case "role_oid":
		id.RoleOID++
	case "database_name":
		id.DatabaseName = "other"
	case "major":
		id.PostgresMajor++
	case "no_callback":
		return nil
	}
	// A hostile provider drops the deadline; the service must restore its owned context.
	err := read(context.WithoutCancel(ctx), new(pgx.Conn), id)
	if p.fault == "repeat" {
		_ = read(ctx, new(pgx.Conn), id)
		return nil
	}
	if p.fault == "ignore_error" {
		return nil
	}
	return err
}

func readerDatabaseSQLServiceFixture(t *testing.T) (*Service, *serviceReaderDatabaseSQLProvider, RestoreSourceDefinition, SnapshotCopyReaderDatabaseSQLRequest) {
	t.Helper()
	_, original, d, r := readerSQLServiceFixture(t)
	p := &serviceReaderDatabaseSQLProvider{serviceReaderSQLProvider: *original}
	s := testService(t, testRegistry(t, p, nil), NewMemoryStore())
	scope := copyinventory.Scope{PostgresMajor: d.Spec.PostgresMajor, OperationID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString(),
		SourceDatabaseID: uuid.NewString(), CaptureDatabaseID: uuid.NewString(), SourceVersion: strings.Repeat("a", 64),
		BackendID: d.BackendID, BackendFingerprint: d.BackendFingerprint, SourceProviderResourceID: d.ProviderResourceID, SourceDataResourceID: d.DataResourceID,
		ProviderSnapshotID: r.Capture.ProviderSnapshotID, CaptureProviderResourceID: r.Capture.ExpectedTargetResourceID,
		CapturePoint: r.Capture.Snapshot.PointInTime, SnapshotCreatedAt: r.SnapshotCreatedAt, CaptureCreatedAt: r.CaptureCreatedAt}
	x := copyinventory.DatabaseExport{Scope: scope, InventoryFingerprint: strings.Repeat("b", 64), Database: copyinventory.Database{OID: 42, Name: "private /?%&数据库\n"},
		CapturedAllowConnections: true, AuthenticatedReaderRoleOID: 43}
	request := SnapshotCopyReaderDatabaseSQLRequest{Reader: r, Database: x}
	if err := request.Validate(d); err != nil {
		t.Fatal(err)
	}
	return s, p, d, request
}

func TestSnapshotCopyReaderDatabaseSQLServicePinsSelectedOIDAndKeepsRecoveryUngated(t *testing.T) {
	s, p, d, r := readerDatabaseSQLServiceFixture(t)
	s.provisioningEnabled = func() bool { return false }
	s.provisioningAllowed = func(context.Context, string) bool { return false }
	s.admit = func(context.Context, string) error { return ErrQuotaExceeded }
	calls := 0
	err := s.WithSnapshotCopyReaderDatabaseSQL(t.Context(), d, r, func(ctx context.Context, _ *pgx.Conn, id SnapshotCopyReaderSQLIdentity) error {
		calls++
		if _, ok := ctx.Deadline(); !ok || id.DatabaseOID != r.Database.Database.OID || id.DatabaseName != r.Database.Database.Name || id.RoleOID != r.Database.AuthenticatedReaderRoleOID {
			t.Fatal("selected callback lost its exact SQL pins/deadline")
		}
		return nil
	})
	if err != nil || calls != 1 || p.selectedCalls != 1 || p.sqlCalls != 0 || !p.deadline || p.definition != d || p.selected.Reader != r.Reader ||
		!p.selected.Database.Scope.Equal(r.Database.Scope) || p.readerCreates+p.readerDeletes != 0 {
		t.Fatalf("selected SQL recovery: %v", err)
	}
}

func TestSnapshotCopyReaderDatabaseSQLServiceRejectsScopeAndSelectionBeforeIO(t *testing.T) {
	for _, fault := range []string{"source", "backend", "snapshot", "capture", "point", "snapshot_time", "capture_time", "closed", "nil_callback", "zero_oid", "zero_role", "nul_name", "fingerprint", "unpinned", "unsupported"} {
		t.Run(fault, func(t *testing.T) {
			s, p, d, r := readerDatabaseSQLServiceFixture(t)
			read := SnapshotCopyReaderSQLRead(func(context.Context, *pgx.Conn, SnapshotCopyReaderSQLIdentity) error {
				t.Fatal("invalid selected SQL callback")
				return nil
			})
			switch fault {
			case "source":
				r.Database.Scope.SourceDataResourceID = "project/other"
			case "backend":
				r.Database.Scope.BackendFingerprint = strings.Repeat("c", 64)
			case "snapshot":
				r.Database.Scope.ProviderSnapshotID = "snapshot-other"
			case "capture":
				r.Database.Scope.CaptureProviderResourceID = "project/other"
			case "point":
				r.Database.Scope.CapturePoint = r.Database.Scope.CapturePoint.Add(-time.Second)
			case "snapshot_time":
				r.Database.Scope.SnapshotCreatedAt = r.Database.Scope.SnapshotCreatedAt.Add(-time.Second)
			case "capture_time":
				r.Database.Scope.CaptureCreatedAt = r.Database.Scope.CaptureCreatedAt.Add(time.Second)
			case "closed":
				r.Database.CapturedAllowConnections = false
			case "nil_callback":
				read = nil
			case "zero_oid":
				r.Database.Database.OID = 0
			case "zero_role":
				r.Database.AuthenticatedReaderRoleOID = 0
			case "nul_name":
				r.Database.Database.Name += "\x00"
			case "fingerprint":
				r.Database.InventoryFingerprint = strings.Repeat("z", 64)
			case "unpinned":
				r.Reader.ExpectedEndpointID, r.Reader.ExpectedCreatedAt = "", time.Time{}
			case "unsupported":
				s = testService(t, testRegistry(t, &p.serviceReaderSQLProvider, nil), NewMemoryStore())
			}
			if err := s.WithSnapshotCopyReaderDatabaseSQL(t.Context(), d, r, read); err == nil || p.selectedCalls != 0 || p.sqlCalls != 0 {
				t.Fatalf("invalid selected input reached provider: %v", err)
			}
		})
	}
}

func TestSnapshotCopyReaderDatabaseSQLServiceRejectsSubstitutionAndLostCallbackAuthority(t *testing.T) {
	for _, fault := range []string{"database_oid", "role_oid", "database_name", "major", "no_callback", "repeat"} {
		t.Run(fault, func(t *testing.T) {
			s, p, d, r := readerDatabaseSQLServiceFixture(t)
			p.fault = fault
			calls := 0
			if err := s.WithSnapshotCopyReaderDatabaseSQL(t.Context(), d, r, func(context.Context, *pgx.Conn, SnapshotCopyReaderSQLIdentity) error { calls++; return nil }); !errors.Is(err, ErrConflict) || calls > 1 || fault != "repeat" && calls != 0 {
				t.Fatalf("selected callback substituted identity/authority: %v", err)
			}
		})
	}
	s, p, d, r := readerDatabaseSQLServiceFixture(t)
	p.fault = "ignore_error"
	want := errors.New("private export unavailable")
	if err := s.WithSnapshotCopyReaderDatabaseSQL(t.Context(), d, r, func(context.Context, *pgx.Conn, SnapshotCopyReaderSQLIdentity) error { return want }); !errors.Is(err, want) {
		t.Fatalf("provider discarded export error: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.WithSnapshotCopyReaderDatabaseSQL(ctx, d, r, func(context.Context, *pgx.Conn, SnapshotCopyReaderSQLIdentity) error {
		t.Fatal("cancelled selected SQL callback")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled selected read: %v", err)
	}
}
