// adr: 585
package neon

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestSnapshotCopyReaderSQLRejectsMetadataBeforeConnectingAndRedactsConnectionErrors(t *testing.T) {
	for _, fault := range []string{"unpinned", "disabled", "credential_host", "connect_error"} {
		t.Run(fault, func(t *testing.T) {
			f := newSnapshotReaderFixture(t)
			f.rows = []endpoint{f.owned()}
			f.request.ExpectedEndpointID, f.request.ExpectedCreatedAt = "ep-owned", f.request.CaptureCreatedAt.Add(time.Minute)
			switch fault {
			case "unpinned":
				f.request.ExpectedEndpointID = ""
			case "disabled":
				yes := true
				f.rows[0].Disabled = &yes
			case "credential_host":
				f.uri = "postgres://gregale_owner:reader-secret@ep-source.neon.tech/gregale_checkpoint"
			}
			connections := 0
			err := f.base.p.withSnapshotCopyReaderSQL(t.Context(), f.base.definition, f.request,
				func(context.Context, *pgx.Conn, managedpostgres.SnapshotCopyReaderSQLIdentity) error {
					t.Fatal("unqualified SQL callback ran")
					return nil
				},
				func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
					connections++
					return nil, errors.New("failed with reader-secret")
				})
			if err == nil || strings.Contains(err.Error(), "reader-secret") || fault != "connect_error" && connections != 0 || f.posts != 0 {
				t.Fatalf("metadata/connection boundary failed: connections=%d err=%v", connections, err)
			}
		})
	}
}

func localReaderSQLConnection(t *testing.T) (*pgx.Conn, *pgx.ConnConfig, int) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL required for private reader SQL contracts")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams = map[string]string{"default_transaction_read_only": "on", "search_path": "pg_catalog"}
	conn, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	var major int
	if err := conn.QueryRow(t.Context(), "select current_setting('server_version_num')::int/10000").Scan(&major); err != nil {
		t.Fatal(err)
	}
	return conn, cfg, major
}

func TestSnapshotCopyReaderSQLAuthenticatesLocalReadOnlyIdentityAndOIDPins(t *testing.T) {
	conn, cfg, major := localReaderSQLConnection(t)
	identity, err := authenticateSnapshotCopyReaderSQL(t.Context(), conn, cfg, major)
	if err != nil || identity.DatabaseOID == 0 || identity.RoleOID == 0 || identity.PostgresMajor != major || identity.DatabaseName != cfg.Database || identity.RoleName != cfg.User {
		t.Fatalf("SQL identity qualification: %+v %v", identity, err)
	}
	// SQL qualification must remain safe with an application-controlled path.
	if _, err := conn.Exec(t.Context(), "set search_path=public"); err != nil {
		t.Fatal(err)
	}
	again, err := authenticateSnapshotCopyReaderSQL(t.Context(), conn, cfg, major)
	if err != nil || again != identity {
		t.Fatalf("search path changed SQL authority: %+v %v", again, err)
	}
	for _, fault := range []string{"database", "role", "major", "transaction", "read_write", "closed"} {
		t.Run(fault, func(t *testing.T) {
			conn, cfg, major := localReaderSQLConnection(t)
			switch fault {
			case "database":
				cfg.Database = "wrong_database"
			case "role":
				cfg.User = "wrong_role"
			case "major":
				major++
			case "transaction":
				if _, err := conn.Begin(t.Context()); err != nil {
					t.Fatal(err)
				}
			case "read_write":
				if _, err := conn.Exec(t.Context(), "set default_transaction_read_only=off"); err != nil {
					t.Fatal(err)
				}
			case "closed":
				_ = conn.Close(t.Context())
			}
			if _, err := authenticateSnapshotCopyReaderSQL(t.Context(), conn, cfg, major); !errors.Is(err, managedpostgres.ErrConflict) {
				t.Fatalf("unqualified SQL identity accepted: %v", err)
			}
		})
	}
}

func TestSnapshotCopyReaderSQLClosesUnqualifiedConnectionBeforeCallback(t *testing.T) {
	f := newSnapshotReaderFixture(t)
	f.rows = []endpoint{f.owned()}
	f.request.ExpectedEndpointID, f.request.ExpectedCreatedAt = "ep-owned", f.request.CaptureCreatedAt.Add(time.Minute)
	conn, _, _ := localReaderSQLConnection(t)
	err := f.base.p.withSnapshotCopyReaderSQL(t.Context(), f.base.definition, f.request,
		func(context.Context, *pgx.Conn, managedpostgres.SnapshotCopyReaderSQLIdentity) error {
			t.Fatal("unqualified connection reached callback")
			return nil
		},
		func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) { return conn, nil })
	if !errors.Is(err, managedpostgres.ErrConflict) || !conn.IsClosed() || f.posts != 0 {
		t.Fatalf("unqualified connection leaked or ran callback: %v", err)
	}
	conn, _, _ = localReaderSQLConnection(t)
	err = f.base.p.withSnapshotCopyReaderSQL(t.Context(), f.base.definition, f.request,
		func(context.Context, *pgx.Conn, managedpostgres.SnapshotCopyReaderSQLIdentity) error {
			t.Fatal("failed connector reached callback")
			return nil
		},
		func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error) {
			return conn, errors.New("partial connect with reader-secret")
		})
	if !errors.Is(err, managedpostgres.ErrUnavailable) || !conn.IsClosed() || strings.Contains(err.Error(), "reader-secret") {
		t.Fatalf("partially established connection leaked credentials or socket: %v", err)
	}
}
