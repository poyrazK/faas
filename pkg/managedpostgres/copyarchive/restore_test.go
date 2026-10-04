// adr: 581
package copyarchive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

func stageRealArchive(t *testing.T, f archiveFixture) (*StagedArchive, *memoryArchiveBackend, string) {
	t.Helper()
	id, _ := age.GenerateX25519Identity()
	key := "postgres-copies/" + f.requirement.Scope.OperationID + "/" + uuid.NewString() + ".age"
	b := &memoryArchiveBackend{}
	r, err := Upload(t.Context(), b, key, f.requirement, []*age.X25519Identity{id}, 8<<20, func(ctx context.Context, w io.Writer) (Receipt, error) {
		return Export(ctx, f.source, f.requirement, f.pgDump, id.Recipient(), w, 4<<20)
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := StageRetained(t.Context(), b, key, f.requirement, []*age.X25519Identity{id}, r, t.TempDir(), 4<<20, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, b, key
}

func localRestoreTarget(t *testing.T, f archiveFixture) (RestoreTarget, RestorePlacement) {
	t.Helper()
	target := RestoreTarget{Scope: f.requirement.Scope, OwnerID: uuid.NewString(), ProviderResourceID: "independent-test-project", DataResourceID: "independent-test-project/br-target",
		EndpointID: "ep-target", ProviderCreatedAt: f.requirement.Scope.CaptureCreatedAt.Add(time.Second), EndpointCreatedAt: f.requirement.Scope.CaptureCreatedAt.Add(2 * time.Second),
		DatabaseName: f.targetName, RoleName: f.owner}
	if err := f.target.QueryRow(t.Context(), "SELECT d.oid,r.oid FROM pg_database d,pg_roles r WHERE d.datname=current_database() AND r.rolname=current_user").Scan(&target.DatabaseOID, &target.RoleOID); err != nil {
		t.Fatal(err)
	}
	// Local fixtures qualify distinct SQL databases and ordinary owner rights.
	// The provider pins are synthetic; no remote/project isolation is claimed.
	placement := func(ctx context.Context, conn *pgx.Conn, got RestoreTarget) error {
		if conn != f.target || got != target || conn.Config().Database == f.source.Config().Database || conn.Config().Database != got.DatabaseName || conn.Config().Host != f.root.Config().Host {
			return pgerrors.ErrConflict
		}
		return nil
	}
	return target, placement
}

func TestRestoreAuthenticatedRealArchivePreservesTestedContentAndIsolatesWrites(t *testing.T) {
	f := newArchiveFixture(t)
	s, b, key := stageRealArchive(t, f)
	target, placement := localRestoreTarget(t, f)
	calls := 0
	checked := func(ctx context.Context, conn *pgx.Conn, t RestoreTarget) error {
		calls++
		return placement(ctx, conn, t)
	}
	b.objects[key] = []byte("substituted-after-staging")
	gets := b.gets
	r, err := s.Restore(t.Context(), f.target, target, filepath.Join(filepath.Dir(f.pgDump), "pg_restore"), checked)
	if err != nil || !SameReceipt(r.Input, s.receipt) || r.Target != target || calls != 2 || b.gets != gets || !s.attempted {
		t.Fatalf("private real restore: %v", err)
	}
	verifyArchiveDataset(t, f)
	if r, err := s.Restore(t.Context(), f.target, target, filepath.Join(filepath.Dir(f.pgDump), "pg_restore"), checked); r != (RestoreExecution{}) || !errors.Is(err, pgerrors.ErrConflict) || calls != 2 {
		t.Fatalf("successful execution was repeated: %v", err)
	}
	encoded, _ := json.Marshal(r)
	for _, formatted := range []string{string(encoded), fmt.Sprint(r), fmt.Sprintf("%#v", r), fmt.Sprintf("%#v", target)} {
		for _, secret := range []string{target.DatabaseName, target.RoleName, "archive-password-not-in-argv", s.file.Name()} {
			if strings.Contains(formatted, secret) {
				t.Fatal("restore result exposed private connection/input")
			}
		}
	}
}

func TestRestoreRejectsSubstitutedTargetAndDispatchAuthorityBeforeSQLWrite(t *testing.T) {
	f := newArchiveFixture(t)
	s, _, _ := stageRealArchive(t, f)
	target, placement := localRestoreTarget(t, f)
	tool := filepath.Join(filepath.Dir(f.pgDump), "pg_restore")
	for _, tc := range []struct {
		name string
		edit func(*RestoreTarget)
		want error
	}{
		{"source_database_oid", func(d *RestoreTarget) { d.DatabaseOID = f.requirement.Database.OID }, pgerrors.ErrConflict},
		{"source_database_name", func(d *RestoreTarget) { d.DatabaseName = f.requirement.Database.Name }, pgerrors.ErrConflict},
		{"role_oid", func(d *RestoreTarget) { d.RoleOID++ }, pgerrors.ErrConflict},
		{"role_name", func(d *RestoreTarget) { d.RoleName = f.member }, pgerrors.ErrConflict},
		{"major", func(d *RestoreTarget) { d.Scope.PostgresMajor++ }, pgerrors.ErrConflict},
		{"operation", func(d *RestoreTarget) { d.Scope.OperationID = uuid.NewString() }, pgerrors.ErrConflict},
		{"source_provider", func(d *RestoreTarget) { d.ProviderResourceID = d.Scope.SourceProviderResourceID }, pgerrors.ErrInvalid},
		{"capture_data", func(d *RestoreTarget) { d.DataResourceID = d.Scope.CaptureProviderResourceID }, pgerrors.ErrInvalid},
		{"source_endpoint", func(d *RestoreTarget) { d.EndpointID = d.Scope.SourceDataResourceID }, pgerrors.ErrInvalid},
		{"aliased_target_pins", func(d *RestoreTarget) { d.EndpointID = d.DataResourceID }, pgerrors.ErrInvalid},
		{"owner_alias", func(d *RestoreTarget) { d.OwnerID = d.Scope.SourceDatabaseID }, pgerrors.ErrInvalid},
		{"owner_invalid", func(d *RestoreTarget) { d.OwnerID = "private-invalid-owner" }, pgerrors.ErrInvalid},
		{"old_project", func(d *RestoreTarget) { d.ProviderCreatedAt = d.Scope.CapturePoint }, pgerrors.ErrInvalid},
		{"old_endpoint", func(d *RestoreTarget) { d.EndpointCreatedAt = d.ProviderCreatedAt.Add(-time.Second) }, pgerrors.ErrInvalid},
		{"future_endpoint", func(d *RestoreTarget) { d.EndpointCreatedAt = time.Now().Add(time.Hour).Truncate(time.Microsecond) }, pgerrors.ErrInvalid},
		{"time_precision", func(d *RestoreTarget) { d.ProviderCreatedAt = d.ProviderCreatedAt.Add(time.Nanosecond) }, pgerrors.ErrInvalid},
		{"invalid_database", func(d *RestoreTarget) { d.DatabaseName = "bad\x00name" }, pgerrors.ErrInvalid},
		{"invalid_provider", func(d *RestoreTarget) { d.ProviderResourceID = "bad\nresource" }, pgerrors.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed, calls := target, 0
			tc.edit(&changed)
			checked := func(ctx context.Context, conn *pgx.Conn, target RestoreTarget) error {
				calls++
				return placement(ctx, conn, target)
			}
			if r, err := s.Restore(t.Context(), f.target, changed, tool, checked); r != (RestoreExecution{}) || !errors.Is(err, tc.want) || s.attempted || calls != 0 {
				t.Fatalf("invalid target reached dispatch: %v", err)
			}
		})
	}
	if r, err := s.Restore(t.Context(), f.source, target, tool, placement); r != (RestoreExecution{}) || !errors.Is(err, pgerrors.ErrConflict) || s.attempted {
		t.Fatalf("source connection supplied to restore: %v", err)
	}
	for _, check := range []RestorePlacement{nil, func(context.Context, *pgx.Conn, RestoreTarget) error {
		return fmt.Errorf("private-provider-token: %w", pgerrors.ErrConflict)
	}} {
		if r, err := s.Restore(t.Context(), f.target, target, tool, check); r != (RestoreExecution{}) || err == nil || s.attempted || strings.Contains(fmt.Sprint(err), "private-provider") {
			t.Fatalf("missing/rejected placement dispatched restore: %v", err)
		}
	}
	if r, err := s.Restore(t.Context(), f.target, target, "relative-pg_restore", placement); r != (RestoreExecution{}) || !errors.Is(err, pgerrors.ErrInvalid) || s.attempted {
		t.Fatalf("relative executable dispatched restore: %v", err)
	}
	if r, err := new(StagedArchive).Restore(t.Context(), f.target, target, tool, placement); r != (RestoreExecution{}) || !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatalf("unconstructed staged archive granted restore: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if r, err := s.Restore(t.Context(), f.target, target, tool, placement); r != (RestoreExecution{}) || !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatalf("closed archive granted restore: %v", err)
	}
	assertNoRestoredSchema(t, f.target)
}

func assertNoRestoredSchema(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	var absent bool
	if err := conn.QueryRow(t.Context(), "SELECT NOT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='app')").Scan(&absent); err != nil || !absent {
		t.Fatalf("failed/rejected restore left partial schema: %v", err)
	}
}

func TestRestoreActualSQLFailureRollsBackSchemaAndPreservesExistingLargeObject(t *testing.T) {
	f := newArchiveFixture(t)
	s, _, _ := stageRealArchive(t, f)
	target, placement := localRestoreTarget(t, f)
	var oid uint32
	if err := f.source.QueryRow(t.Context(), "SELECT oid FROM app.large_object_refs").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	// This collision is encountered after schema/table creation. An actual
	// pg_restore error must roll back those earlier SQL writes as well.
	if _, err := f.target.Exec(t.Context(), "SELECT lo_from_bytea($1,decode('010203','hex'))", oid); err != nil {
		t.Fatal(err)
	}
	if r, err := s.Restore(t.Context(), f.target, target, filepath.Join(filepath.Dir(f.pgDump), "pg_restore"), placement); r != (RestoreExecution{}) || !errors.Is(err, pgerrors.ErrUnavailable) || !s.attempted {
		t.Fatalf("failed transaction returned execution: %v", err)
	}
	assertNoRestoredSchema(t, f.target)
	var unchanged bool
	if err := f.target.QueryRow(t.Context(), "SELECT encode(lo_get($1),'hex')='010203'", oid).Scan(&unchanged); err != nil || !unchanged {
		t.Fatalf("failed transaction changed existing target object: %v", err)
	}
	if err := f.source.QueryRow(t.Context(), "SELECT encode(lo_get($1),'hex')='deadbeef00'", oid).Scan(&unchanged); err != nil || !unchanged {
		t.Fatalf("failed target transaction changed source: %v", err)
	}
	if r, err := s.Restore(t.Context(), f.target, target, filepath.Join(filepath.Dir(f.pgDump), "pg_restore"), placement); r != (RestoreExecution{}) || !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatalf("uncertain/failed dispatch was repeated: %v", err)
	}
}

func TestRestorePostCommitPlacementFailureReturnsNoCompletionAndCannotRepeat(t *testing.T) {
	f := newArchiveFixture(t)
	s, _, _ := stageRealArchive(t, f)
	target, placement := localRestoreTarget(t, f)
	calls := 0
	check := func(ctx context.Context, conn *pgx.Conn, target RestoreTarget) error {
		calls++
		if calls == 2 {
			return errors.New("private-provider-post-commit-diagnostic")
		}
		return placement(ctx, conn, target)
	}
	if r, err := s.Restore(t.Context(), f.target, target, filepath.Join(filepath.Dir(f.pgDump), "pg_restore"), check); r != (RestoreExecution{}) || !errors.Is(err, pgerrors.ErrUnavailable) || !s.attempted || calls != 2 || strings.Contains(fmt.Sprint(err), "private-provider") {
		t.Fatalf("lost post-commit placement returned success: %v", err)
	}
	// SQL did commit. The zero result must never authorize repeating or stage
	// publication; a future durable importer must recover independent evidence.
	verifyArchiveDataset(t, f)
	if r, err := s.Restore(t.Context(), f.target, target, filepath.Join(filepath.Dir(f.pgDump), "pg_restore"), check); r != (RestoreExecution{}) || !errors.Is(err, pgerrors.ErrConflict) || calls != 2 {
		t.Fatalf("post-commit unknown write repeated: %v", err)
	}
}

func writeFakeRestore(t *testing.T, major int, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pg_restore")
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf 'pg_restore (PostgreSQL) %d.1\\n'; exit 0; fi\n%s\n", major, body)
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRestoreChecksTransactionModeTargetIdentityToolsDiagnosticsAndCancellation(t *testing.T) {
	f := newArchiveFixture(t)
	target, placement := localRestoreTarget(t, f)
	major := target.Scope.PostgresMajor
	for _, mode := range []string{"open_transaction", "read_only", "tool_major", "tool_missing", "preplacement_mutation", "diagnostic", "failed_process", "postplacement_mutation", "cancelled_process"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _ := stageRealArchive(t, f)
			tool, check, ctx := filepath.Join(filepath.Dir(f.pgDump), "pg_restore"), placement, t.Context()
			want, attempted := pgerrors.ErrConflict, false
			var tx pgx.Tx
			var err error
			switch mode {
			case "open_transaction":
				tx, err = f.target.Begin(ctx)
			case "read_only":
				_, err = f.target.Exec(ctx, "SET default_transaction_read_only=on")
			case "tool_major":
				tool, want = writeFakeRestore(t, major+1, "exit 0"), pgerrors.ErrUnsupported
			case "tool_missing":
				tool, want = filepath.Join(t.TempDir(), "missing"), pgerrors.ErrUnavailable
			case "preplacement_mutation":
				check = func(ctx context.Context, conn *pgx.Conn, target RestoreTarget) error {
					_, e := conn.Exec(ctx, "SET default_transaction_read_only=on")
					return e
				}
			case "diagnostic":
				tool, want, attempted = writeFakeRestore(t, major, "printf 'private-password-diagnostic' >&2"), pgerrors.ErrUnsupported, true
			case "failed_process":
				tool, want, attempted = writeFakeRestore(t, major, "printf 'private-password-diagnostic' >&2; exit 1"), pgerrors.ErrUnavailable, true
			case "postplacement_mutation":
				tool, attempted = writeFakeRestore(t, major, "exit 0"), true
				calls := 0
				check = func(ctx context.Context, conn *pgx.Conn, target RestoreTarget) error {
					calls++
					if calls == 2 {
						_, e := conn.Exec(ctx, "SET default_transaction_read_only=on")
						return e
					}
					return placement(ctx, conn, target)
				}
			case "cancelled_process":
				tool, want, attempted = writeFakeRestore(t, major, "while :; do :; done"), context.DeadlineExceeded, true
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 500*time.Millisecond)
				defer cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.Restore(ctx, f.target, target, tool, check)
			if tx != nil {
				if e := tx.Rollback(t.Context()); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := f.target.Exec(t.Context(), "SET default_transaction_read_only=off"); e != nil {
				t.Fatal(e)
			}
			if r != (RestoreExecution{}) || !errors.Is(err, want) || s.attempted != attempted || strings.Contains(fmt.Sprint(err), "private-password") {
				t.Fatalf("unqualified restore execution: %v (attempt=%v)", err, s.attempted)
			}
			assertNoRestoredSchema(t, f.target)
		})
	}
}

func TestRestoreConnectionKeepsLiteralIdentityPasswordIsolationAndWriteOptions(t *testing.T) {
	cfg, err := pgx.ParseConfig("postgresql://private_role@target.example:5432/unused?sslmode=verify-full")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database, cfg.Password = "postgresql://literal/ db?é#%", "private-password"
	dumpDSN, dumpEnv, err := dumpConnection(cfg, 16)
	if err != nil {
		t.Fatal(err)
	}
	dsn, env, err := restoreConnection(cfg, 16)
	if err != nil || dsn != dumpDSN || strings.Contains(dsn, cfg.Password) || !strings.Contains(strings.Join(env, "\n"), "PGOPTIONS=-c default_transaction_read_only=off -c search_path=pg_catalog") {
		t.Fatalf("restore connection dropped identity or explicit mode: %v", err)
	}
	for n := range dumpEnv {
		if strings.HasPrefix(dumpEnv[n], "PGOPTIONS=") || strings.HasPrefix(dumpEnv[n], "PGAPPNAME=") {
			continue
		}
		if !reflect.DeepEqual(env[n], dumpEnv[n]) {
			t.Fatal("restore connection changed credential/trust controls")
		}
	}
	// OIDs are local to a cluster. Equal numeric OIDs cannot reject an otherwise
	// independently authenticated project: provider placement is mandatory.
	target := RestoreTarget{Scope: archiveRequirement().Scope, OwnerID: uuid.NewString(), ProviderResourceID: "project-target", DataResourceID: "project-target/br-target", EndpointID: "endpoint-target",
		DatabaseName: cfg.Database, RoleName: cfg.User, DatabaseOID: 17, RoleOID: 11}
	target.ProviderCreatedAt, target.EndpointCreatedAt = target.Scope.CaptureCreatedAt.Add(time.Second), target.Scope.CaptureCreatedAt.Add(2*time.Second)
	if target.Validate() != nil {
		t.Fatal("valid local SQL identity rejected without provider proof")
	}
	fingerprint, err := target.Fingerprint()
	if err != nil || len(fingerprint) != 64 {
		t.Fatalf("target descriptor digest: %v", err)
	}
	zoned := target
	zoned.ProviderCreatedAt = zoned.ProviderCreatedAt.In(time.FixedZone("different", 3*60*60))
	zoned.Scope.CapturePoint = zoned.Scope.CapturePoint.In(time.FixedZone("different", 3*60*60))
	if got, err := zoned.Fingerprint(); err != nil || got != fingerprint {
		t.Fatalf("same instant changed immutable target digest: %v", err)
	}
	changed := target
	changed.DatabaseName += "x"
	if got, err := changed.Fingerprint(); err != nil || got == fingerprint {
		t.Fatalf("SQL name substitution retained digest: %v", err)
	}
}
