// adr:531
package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
)

type serviceTargetDatabaseSQLProvider struct {
	serviceSnapshotCopyProvider
	sqlCalls   int
	fault      string
	sqlRequest SnapshotCopyTargetDatabaseSQLRequest
	late       SnapshotCopyTargetSQLRun
	started    chan struct{}
	done       chan error
}

func (p *serviceTargetDatabaseSQLProvider) WithSnapshotCopyTargetDatabaseSQL(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyTargetDatabaseSQLRequest, run SnapshotCopyTargetSQLRun) error {
	p.sqlCalls++
	p.definition, p.sqlRequest = d, r
	_, p.deadline = ctx.Deadline()
	i := SnapshotCopyTargetSQLIdentity{PostgresMajor: d.Spec.PostgresMajor, DatabaseName: r.Target.DatabaseName, DatabaseOID: r.Target.DatabaseOID, RoleName: r.Target.RoleName, RoleOID: r.Target.RoleOID}
	conn := new(pgx.Conn)
	switch p.fault {
	case "no_callback":
		return nil
	case "late_callback":
		p.late = run
		return nil
	case "return_while_running":
		go func() { p.done <- run(ctx, conn, i) }()
		<-p.started
		return nil
	case "nil_connection":
		conn = nil
	case "major":
		i.PostgresMajor++
	case "database":
		i.DatabaseName = "other"
	case "database_oid":
		i.DatabaseOID++
	case "role":
		i.RoleName = "other"
	case "role_oid":
		i.RoleOID++
	}
	err := run(context.Background(), conn, i)
	if p.fault == "repeat" {
		_ = run(ctx, conn, i)
		return nil
	}
	if p.fault == "ignore_error" {
		return nil
	}
	return err
}

func targetSQLServiceFixture(t *testing.T) (*Service, *serviceTargetDatabaseSQLProvider, RestoreSourceDefinition, SnapshotCopyTargetDatabaseSQLRequest) {
	t.Helper()
	_, original, d, prep := snapshotCopyServiceFixture(t)
	p := &serviceTargetDatabaseSQLProvider{serviceSnapshotCopyProvider: *original}
	s := testService(t, testRegistry(t, p, nil), NewMemoryStore())
	prep.ResourceID = uuid.NewString()
	prep.ExpectedProviderResourceID = "independent-project"
	prep.ExpectedCreatedAt = prep.CaptureCreatedAt.Add(time.Minute)
	scope := copyinventory.Scope{PostgresMajor: d.Spec.PostgresMajor, OperationID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString(), SourceDatabaseID: uuid.NewString(), CaptureDatabaseID: uuid.NewString(),
		SourceVersion: strings.Repeat("a", 64), BackendID: d.BackendID, BackendFingerprint: d.BackendFingerprint, SourceProviderResourceID: d.ProviderResourceID, SourceDataResourceID: d.DataResourceID,
		ProviderSnapshotID: prep.Capture.ProviderSnapshotID, CaptureProviderResourceID: prep.Capture.ExpectedTargetResourceID, CapturePoint: prep.Capture.Snapshot.PointInTime, SnapshotCreatedAt: prep.SnapshotCreatedAt, CaptureCreatedAt: prep.CaptureCreatedAt}
	r := SnapshotCopyTargetDatabaseSQLRequest{Preparation: prep, Target: copyarchive.RestoreTarget{Scope: scope, OwnerID: prep.ResourceID, ProviderResourceID: prep.ExpectedProviderResourceID, ProviderCreatedAt: prep.ExpectedCreatedAt,
		DataResourceID: "independent-project/br-root", EndpointID: "ep-independent", EndpointCreatedAt: prep.ExpectedCreatedAt, DatabaseName: "private /?%&数据库\n", DatabaseOID: 42, RoleName: "private_target_role", RoleOID: 43}}
	if err := r.Validate(d); err != nil {
		t.Fatal(err)
	}
	return s, p, d, r
}

func TestSnapshotCopyTargetDatabaseSQLServicePinsIdentityAndKeepsOwnedRecoveryUngated(t *testing.T) {
	s, p, d, r := targetSQLServiceFixture(t)
	s.provisioningEnabled = func() bool { return false }
	s.provisioningAllowed = func(context.Context, string) bool { return false }
	s.admit = func(context.Context, string) error { return ErrQuotaExceeded }
	calls := 0
	err := s.WithSnapshotCopyTargetDatabaseSQL(t.Context(), d, r, func(ctx context.Context, _ *pgx.Conn, i SnapshotCopyTargetSQLIdentity) error {
		calls++
		if _, ok := ctx.Deadline(); !ok || i.DatabaseOID != r.Target.DatabaseOID || i.RoleOID != r.Target.RoleOID {
			t.Fatal("target SQL identity/deadline changed")
		}
		return nil
	})
	if err != nil || calls != 1 || p.sqlCalls != 1 || !p.deadline || p.sqlRequest != r || p.definition != d || p.copyCreates+p.copyFinds != 0 {
		t.Fatalf("owned target borrow: %v", err)
	}
	i := SnapshotCopyTargetSQLIdentity{DatabaseName: r.Target.DatabaseName, RoleName: r.Target.RoleName}
	raw, _ := json.Marshal(i)
	for _, text := range []string{string(raw), fmt.Sprint(i), fmt.Sprintf("%#v", i)} {
		if strings.Contains(text, i.DatabaseName) || strings.Contains(text, i.RoleName) {
			t.Fatal("target SQL identity exposed private names")
		}
	}
}

func TestSnapshotCopyTargetDatabaseSQLServiceRejectsScopeAndAuthorityBeforeProviderIO(t *testing.T) {
	for _, fault := range []string{"owner", "project", "project_time", "source", "backend", "snapshot", "capture", "point", "snapshot_time", "capture_time", "oid", "role_oid", "unpinned", "nil_callback", "unsupported", "registry"} {
		t.Run(fault, func(t *testing.T) {
			s, p, d, r := targetSQLServiceFixture(t)
			run := SnapshotCopyTargetSQLRun(func(context.Context, *pgx.Conn, SnapshotCopyTargetSQLIdentity) error {
				t.Fatal("invalid request reached callback")
				return nil
			})
			switch fault {
			case "owner":
				r.Target.OwnerID = uuid.NewString()
			case "project":
				r.Target.ProviderResourceID = "other-project"
			case "project_time":
				r.Target.ProviderCreatedAt = r.Target.ProviderCreatedAt.Add(-time.Second)
			case "source":
				r.Target.Scope.SourceDataResourceID = "project/other"
			case "backend":
				r.Target.Scope.BackendFingerprint = strings.Repeat("e", 64)
			case "snapshot":
				r.Target.Scope.ProviderSnapshotID = "snapshot-other"
			case "capture":
				r.Target.Scope.CaptureProviderResourceID = "project/other"
			case "point":
				r.Target.Scope.CapturePoint = r.Target.Scope.CapturePoint.Add(-time.Second)
			case "snapshot_time":
				r.Target.Scope.SnapshotCreatedAt = r.Target.Scope.SnapshotCreatedAt.Add(-time.Second)
			case "capture_time":
				r.Target.Scope.CaptureCreatedAt = r.Target.Scope.CaptureCreatedAt.Add(time.Second)
			case "oid":
				r.Target.DatabaseOID = 0
			case "role_oid":
				r.Target.RoleOID = 0
			case "unpinned":
				r.Preparation.ExpectedProviderResourceID = ""
				r.Preparation.ExpectedCreatedAt = time.Time{}
			case "nil_callback":
				run = nil
			case "unsupported":
				s = testService(t, testRegistry(t, &p.serviceSnapshotCopyProvider, nil), NewMemoryStore())
			case "registry":
				d.BackendFingerprint = strings.Repeat("e", 64)
				r.Target.Scope.BackendFingerprint = d.BackendFingerprint
			}
			if err := s.WithSnapshotCopyTargetDatabaseSQL(t.Context(), d, r, run); err == nil || p.sqlCalls != 0 {
				t.Fatalf("invalid target SQL input reached provider: %v", err)
			}
		})
	}
}

func TestSnapshotCopyTargetDatabaseSQLServiceRejectsRepeatedLateAndSubstitutedCallbacks(t *testing.T) {
	for _, fault := range []string{"major", "database", "database_oid", "role", "role_oid", "no_callback", "nil_connection", "repeat", "late_callback"} {
		t.Run(fault, func(t *testing.T) {
			s, p, d, r := targetSQLServiceFixture(t)
			p.fault = fault
			calls := 0
			err := s.WithSnapshotCopyTargetDatabaseSQL(t.Context(), d, r, func(context.Context, *pgx.Conn, SnapshotCopyTargetSQLIdentity) error { calls++; return nil })
			if !errors.Is(err, ErrConflict) || calls > 1 || fault != "repeat" && calls != 0 {
				t.Fatalf("callback authority accepted: %v", err)
			}
			if p.late != nil {
				i := SnapshotCopyTargetSQLIdentity{PostgresMajor: d.Spec.PostgresMajor, DatabaseName: r.Target.DatabaseName, DatabaseOID: r.Target.DatabaseOID, RoleName: r.Target.RoleName, RoleOID: r.Target.RoleOID}
				if err := p.late(t.Context(), new(pgx.Conn), i); !errors.Is(err, ErrConflict) || calls != 0 {
					t.Fatalf("late provider callback acquired write authority: %v", err)
				}
			}
		})
	}
	s, p, d, r := targetSQLServiceFixture(t)
	p.fault = "ignore_error"
	want := errors.New("owned import unavailable")
	if err := s.WithSnapshotCopyTargetDatabaseSQL(t.Context(), d, r, func(context.Context, *pgx.Conn, SnapshotCopyTargetSQLIdentity) error { return want }); !errors.Is(err, want) {
		t.Fatalf("provider discarded callback failure: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.WithSnapshotCopyTargetDatabaseSQL(ctx, d, r, func(context.Context, *pgx.Conn, SnapshotCopyTargetSQLIdentity) error {
		t.Fatal("cancelled callback ran")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled target borrow: %v", err)
	}
}

func TestSnapshotCopyTargetDatabaseSQLServiceRejectsProviderReturnBeforeCallbackCompletion(t *testing.T) {
	s, p, d, r := targetSQLServiceFixture(t)
	p.fault, p.started, p.done = "return_while_running", make(chan struct{}), make(chan error, 1)
	err := s.WithSnapshotCopyTargetDatabaseSQL(t.Context(), d, r, func(ctx context.Context, _ *pgx.Conn, _ SnapshotCopyTargetSQLIdentity) error {
		close(p.started)
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("running callback became a completed borrow: %v", err)
	}
	select {
	case err := <-p.done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("early-return callback kept authority: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("early provider return did not cancel callback")
	}
}
