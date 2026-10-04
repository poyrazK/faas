// adr:566
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type serviceReaderSQLProvider struct {
	serviceReaderDeletionProvider
	sqlCalls int
	sqlFault string
}

func (p *serviceReaderSQLProvider) WithSnapshotCopyReaderSQL(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyReaderRequest, read SnapshotCopyReaderSQLRead) error {
	p.sqlCalls++
	p.definition, p.readerRequest = d, r
	_, p.deadline = ctx.Deadline()
	identity := SnapshotCopyReaderSQLIdentity{PostgresMajor: d.Spec.PostgresMajor, DatabaseName: "private_catalogue", RoleName: "private_owner", DatabaseOID: 42, RoleOID: 43}
	conn := new(pgx.Conn)
	switch p.sqlFault {
	case "no_callback":
		return nil
	case "no_connection":
		conn = nil
	case "major":
		identity.PostgresMajor++
	case "database_oid":
		identity.DatabaseOID = 0
	case "role_oid":
		identity.RoleOID = 0
	case "role":
		identity.RoleName = ""
	}
	// A hostile provider drops the deadline; the service must restore its owned context.
	err := read(context.WithoutCancel(ctx), conn, identity)
	if p.sqlFault == "repeat" {
		_ = read(ctx, conn, identity)
		return nil // Even ignored provider errors must not grant success.
	}
	if p.sqlFault == "ignore_error" {
		return nil
	}
	return err
}

func readerSQLServiceFixture(t *testing.T) (*Service, *serviceReaderSQLProvider, RestoreSourceDefinition, SnapshotCopyReaderRequest) {
	t.Helper()
	_, original, d, deletion := readerDeletionServiceFixture(t)
	p := &serviceReaderSQLProvider{serviceReaderDeletionProvider: *original}
	s := testService(t, testRegistry(t, p, nil), NewMemoryStore())
	return s, p, d, deletion.Reader
}

func TestSnapshotCopyReaderSQLServiceKeepsFrozenInputDeadlineAndUngatedRecovery(t *testing.T) {
	s, p, d, r := readerSQLServiceFixture(t)
	s.provisioningEnabled = func() bool { return false }
	s.provisioningAllowed = func(context.Context, string) bool { return false }
	s.admit = func(context.Context, string) error { return ErrQuotaExceeded }
	called := 0
	err := s.WithSnapshotCopyReaderSQL(t.Context(), d, r, func(ctx context.Context, _ *pgx.Conn, identity SnapshotCopyReaderSQLIdentity) error {
		called++
		if _, ok := ctx.Deadline(); !ok || identity.PostgresMajor != d.Spec.PostgresMajor || identity.DatabaseOID != 42 || identity.RoleOID != 43 {
			t.Fatal("SQL callback lost deadline or identities")
		}
		return nil
	})
	if err != nil || called != 1 || p.sqlCalls != 1 || !p.deadline || p.definition != d || p.readerRequest != r || p.readerCreates+p.readerDeletes != 0 {
		t.Fatalf("private SQL recovery: %v", err)
	}
}

func TestSnapshotCopyReaderSQLServiceRejectsInvalidPinsAndCallbackAuthority(t *testing.T) {
	for _, fault := range []string{"no_callback", "no_connection", "major", "database_oid", "role_oid", "role", "repeat"} {
		t.Run(fault, func(t *testing.T) {
			s, p, d, r := readerSQLServiceFixture(t)
			p.sqlFault = fault
			called := 0
			err := s.WithSnapshotCopyReaderSQL(t.Context(), d, r, func(context.Context, *pgx.Conn, SnapshotCopyReaderSQLIdentity) error { called++; return nil })
			if !errors.Is(err, ErrConflict) || called > 1 || fault != "repeat" && called != 0 {
				t.Fatalf("invalid SQL callback accepted: calls=%d err=%v", called, err)
			}
		})
	}
	for _, fault := range []string{"endpoint", "time_pin", "source", "fingerprint", "unsupported", "nil_callback"} {
		t.Run(fault, func(t *testing.T) {
			s, p, d, r := readerSQLServiceFixture(t)
			var read SnapshotCopyReaderSQLRead = func(context.Context, *pgx.Conn, SnapshotCopyReaderSQLIdentity) error {
				t.Fatal("invalid request reached SQL")
				return nil
			}
			switch fault {
			case "endpoint":
				r.ExpectedEndpointID, r.ExpectedCreatedAt = "", time.Time{}
			case "time_pin":
				r.ExpectedCreatedAt = time.Time{}
			case "source":
				d.DataResourceID = "project/other"
			case "fingerprint":
				d.BackendFingerprint = "changed"
			case "unsupported":
				s = testService(t, testRegistry(t, &p.serviceReaderDeletionProvider, nil), NewMemoryStore())
			case "nil_callback":
				read = nil
			}
			if err := s.WithSnapshotCopyReaderSQL(t.Context(), d, r, read); err == nil || p.sqlCalls != 0 {
				t.Fatalf("invalid SQL request reached provider: %v", err)
			}
		})
	}
}

func TestSnapshotCopyReaderSQLServicePreservesCallbackFailureAndCancellation(t *testing.T) {
	s, p, d, r := readerSQLServiceFixture(t)
	p.sqlFault = "ignore_error"
	want := errors.New("inventory callback unavailable")
	if err := s.WithSnapshotCopyReaderSQL(t.Context(), d, r, func(context.Context, *pgx.Conn, SnapshotCopyReaderSQLIdentity) error { return want }); !errors.Is(err, want) {
		t.Fatalf("provider discarded callback failure: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.WithSnapshotCopyReaderSQL(ctx, d, r, func(context.Context, *pgx.Conn, SnapshotCopyReaderSQLIdentity) error {
		t.Fatal("cancelled SQL callback ran")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("provider discarded cancellation: %v", err)
	}
}
