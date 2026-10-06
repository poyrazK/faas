// adr: 590
package copyinventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

type inventoryFixture struct {
	root, conn            *pgx.Conn
	cfg                   Config
	closed, owner, member string
}

func newInventoryFixture(t *testing.T) inventoryFixture {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL required for copy inventory contracts")
	}
	root, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close(context.Background()) })
	id := strings.ReplaceAll(uuid.NewString(), "-", "")
	f := inventoryFixture{root: root, closed: "grg_copy_closed_" + id, owner: "grg_copy_owner_" + id, member: "grg_copy_member_" + id}
	f.cfg.DatabaseName = "grg_copy_catalog_" + id
	f.cfg.RoleName = f.owner
	f.cfg.FingerprintKey[0] = 17
	roles, dbs := []string{}, []string{}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if f.conn != nil {
			_ = f.conn.Close(ctx)
		}
		for _, db := range dbs {
			if _, err := root.Exec(ctx, "DROP DATABASE "+pgx.Identifier{db}.Sanitize()); err != nil {
				t.Error(err)
			}
		}
		for _, role := range roles {
			if _, err := root.Exec(ctx, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Error(err)
			}
		}
	})
	for _, role := range []string{f.owner, f.member} {
		if _, err := root.Exec(t.Context(), "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOINHERIT PASSWORD 'password-never-in-inventory'"); err != nil {
			t.Fatal(err)
		}
		roles = append(roles, role)
	}
	for _, db := range []string{f.cfg.DatabaseName, f.closed} {
		if _, err := root.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{db}.Sanitize()+" OWNER "+pgx.Identifier{f.owner}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		dbs = append(dbs, db)
	}
	statements := []string{
		"ALTER DATABASE " + pgx.Identifier{f.closed}.Sanitize() + " ALLOW_CONNECTIONS false",
		"ALTER DATABASE " + pgx.Identifier{f.closed}.Sanitize() + " CONNECTION LIMIT 7",
		"REVOKE ALL ON DATABASE " + pgx.Identifier{f.closed}.Sanitize() + " FROM PUBLIC",
		"GRANT CONNECT ON DATABASE " + pgx.Identifier{f.closed}.Sanitize() + " TO " + pgx.Identifier{f.member}.Sanitize(),
		"ALTER DATABASE " + pgx.Identifier{f.closed}.Sanitize() + " SET timezone TO 'Europe/Istanbul'",
		"ALTER ROLE " + pgx.Identifier{f.member}.Sanitize() + " SET work_mem TO '8192kB'",
		"ALTER ROLE " + pgx.Identifier{f.member}.Sanitize() + " IN DATABASE " + pgx.Identifier{f.closed}.Sanitize() + " SET grg_copy.secret_key TO 'private-setting-must-be-sealed'",
		"GRANT " + pgx.Identifier{f.owner}.Sanitize() + " TO " + pgx.Identifier{f.member}.Sanitize() + " WITH ADMIN OPTION",
	}
	for _, sql := range statements {
		if _, err := root.Exec(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	c := root.Config().Copy()
	c.Database = f.cfg.DatabaseName
	c.User = f.owner
	c.Password = "password-never-in-inventory"
	f.conn, err = pgx.ConnectConfig(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.conn.QueryRow(t.Context(), "select current_setting('server_version_num')::int/10000,d.oid,r.oid from pg_database d,pg_roles r where d.datname=current_database() and r.rolname=current_user").Scan(&f.cfg.PostgresMajor, &f.cfg.DatabaseOID, &f.cfg.RoleOID); err != nil {
		t.Fatal(err)
	}
	if f.cfg.PostgresMajor >= 16 {
		if _, err := root.Exec(t.Context(), "GRANT "+pgx.Identifier{f.owner}.Sanitize()+" TO "+pgx.Identifier{f.member}.Sanitize()+" WITH INHERIT FALSE, SET FALSE"); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// assertStableFingerprint requires two reads that saw the same catalogues
// to produce the same fingerprint. The inventory is cluster-wide, and in CI
// the other packages of the shard create and drop databases and roles on the
// same server, so two reads can legitimately differ. A pair whose contents
// differ raced that churn and is retried; equal contents must fingerprint
// equally.
func assertStableFingerprint(t *testing.T, f inventoryFixture) {
	t.Helper()
	ctx := t.Context()
	for attempt := 0; attempt < 50; attempt++ {
		first, err := Read(ctx, f.conn, f.cfg)
		if err != nil {
			t.Fatal(err)
		}
		second, err := Read(ctx, f.conn, f.cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first.body, second.body) {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if first.fingerprint != second.fingerprint {
			t.Fatal("stable source inventory changed")
		}
		return
	}
	t.Fatal("every read pair saw different catalogues; cannot check fingerprint stability")
}

func TestCopyInventoryReadsClosedDatabasesGlobalsAndPrivateSettings(t *testing.T) {
	f := newInventoryFixture(t)
	ctx := t.Context()
	i, err := Read(ctx, f.conn, f.cfg)
	if err != nil {
		q := sqlc.New()
		identity, identityErr := q.CopyClusterIdentity(ctx, f.conn)
		if identityErr == nil {
			t.Logf("identity pins: database=%v role=%v version=%v session=%v", identity.DatabaseOid.Uint32 == f.cfg.DatabaseOID, identity.RoleOid.Uint32 == f.cfg.RoleOID, int(identity.ServerVersion/10000) == f.cfg.PostgresMajor, identity.SessionRole == f.cfg.RoleName)
		}
		b := payload{DatabaseOID: f.cfg.DatabaseOID, RoleOID: f.cfg.RoleOID}
		outs := []any{&b.Databases, &b.Roles, &b.Memberships, &b.Settings, &b.Tablespaces, &b.PreparedTransactions}
		for n, query := range []func(context.Context, sqlc.DBTX) ([]byte, error){q.CopyClusterDatabases, q.CopyClusterRoles, q.CopyClusterMemberships, q.CopyClusterSettings, q.CopyClusterTablespaces, q.CopyClusterPreparedTransactions} {
			raw, queryErr := query(ctx, f.conn)
			if queryErr != nil {
				var pgErr *pgconn.PgError
				if errors.As(queryErr, &pgErr) {
					t.Errorf("catalogue query %d: SQLSTATE %s", n, pgErr.Code)
				}
			} else if decodeErr := json.Unmarshal(raw, outs[n]); decodeErr != nil {
				t.Errorf("catalogue query %d decode: %T", n, decodeErr)
			}
		}
		t.Logf("catalogue validation: %s", payloadProblem(b))
		t.Fatal(err)
	}
	var closed *Database
	for n := range i.body.Databases {
		if i.body.Databases[n].Name == f.closed {
			closed = &i.body.Databases[n]
		}
	}
	if closed == nil || closed.AllowConnections || closed.ConnectionLimit != 7 || closed.Owner != f.owner || len(closed.ACL) == 0 {
		t.Fatalf("closed database metadata missing: %v", closed != nil)
	}
	for _, name := range []string{"template0", "template1", f.cfg.DatabaseName, f.closed} {
		if !slices.ContainsFunc(i.body.Databases, func(d Database) bool { return d.Name == name }) {
			t.Fatalf("database omitted: %s", name)
		}
	}
	if f.cfg.PostgresMajor >= 15 && closed.LocaleProvider == nil {
		t.Fatal("locale provider was lost during decode")
	}
	var membership *Membership
	for n := range i.body.Memberships {
		m := &i.body.Memberships[n]
		if m.Role == f.owner && m.Member == f.member {
			membership = m
		}
	}
	if membership == nil || !membership.Admin || membership.Inherit || f.cfg.PostgresMajor >= 16 && membership.Set || f.cfg.PostgresMajor < 16 && !membership.Set {
		t.Fatal("membership authority or portable semantics omitted")
	}
	if !slices.ContainsFunc(i.body.Settings, func(s Setting) bool {
		return s.Database == f.closed && s.Role == "" && slices.Contains(s.Config, "TimeZone=Europe/Istanbul")
	}) ||
		!slices.ContainsFunc(i.body.Settings, func(s Setting) bool {
			return s.Database == "" && s.Role == f.member && slices.Contains(s.Config, "work_mem=8192kB")
		}) ||
		!slices.ContainsFunc(i.body.Settings, func(s Setting) bool {
			return s.Database == f.closed && s.Role == f.member && slices.Contains(s.Config, "grg_copy.secret_key=private-setting-must-be-sealed")
		}) {
		t.Fatal("database/role/role-in-database configuration missing")
	}
	raw, err := i.PayloadForSealing()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "private-setting-must-be-sealed") || strings.Contains(string(raw), "password-never-in-inventory") || strings.Contains(string(raw), "rolpassword") {
		t.Fatal("sealed payload lost config or copied password material")
	}
	encoded, err := json.Marshal(i)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{string(encoded), fmt.Sprintf("%+v", i), fmt.Sprintf("%#v", i)} {
		if strings.Contains(out, "private-setting") || strings.Contains(out, f.member) || strings.Contains(out, f.closed) {
			t.Fatal("ordinary output exposed sensitive inventory")
		}
	}
	assertStableFingerprint(t, f)
	otherKey := f.cfg
	otherKey.FingerprintKey[1] = 23
	if other, err := Read(ctx, f.conn, otherKey); err != nil || other.fingerprint == i.fingerprint {
		t.Fatalf("fingerprint was unkeyed: %v", err)
	}
	if _, err := f.root.Exec(ctx, "ALTER ROLE "+pgx.Identifier{f.member}.Sanitize()+" IN DATABASE "+pgx.Identifier{f.closed}.Sanitize()+" SET grg_copy.secret_key TO 'changed-private-setting'"); err != nil {
		t.Fatal(err)
	}
	if changed, err := Read(ctx, f.conn, f.cfg); err != nil || changed.fingerprint == i.fingerprint {
		t.Fatalf("closed database scoped edit was ignored: %v", err)
	}
	if f.conn.PgConn().TxStatus() != 'I' {
		t.Fatal("read left a transaction open")
	}
}

func TestCopyInventoryPinsSQLIdentityAndIsolatesBorrowedSession(t *testing.T) {
	f := newInventoryFixture(t)
	ctx := t.Context()
	if _, err := f.conn.Exec(ctx, "CREATE SCHEMA forged; CREATE TABLE forged.pg_roles (oid oid,rolname name); SET search_path=forged,pg_catalog"); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(ctx, f.conn, f.cfg); err != nil {
		t.Fatalf("caller search path contaminated catalogue read: %v", err)
	}
	var path string
	if err := f.conn.QueryRow(ctx, "show search_path").Scan(&path); err != nil || path != "forged, pg_catalog" {
		t.Fatalf("read changed caller session: %q %v", path, err)
	}
	for _, mode := range []string{"major", "database_oid", "database_name", "role_oid", "role_name", "missing_key"} {
		bad := f.cfg
		want := pgerrors.ErrConflict
		switch mode {
		case "major":
			bad.PostgresMajor++
		case "database_oid":
			bad.DatabaseOID++
		case "database_name":
			bad.DatabaseName = f.closed
		case "role_oid":
			bad.RoleOID++
		case "role_name":
			bad.RoleName = f.member
		case "missing_key":
			bad.FingerprintKey = [32]byte{}
			want = pgerrors.ErrInvalid
		}
		if i, err := Read(ctx, f.conn, bad); !errors.Is(err, want) || i.fingerprint != "" || f.conn.PgConn().TxStatus() != 'I' {
			t.Fatalf("accepted %s pin or leaked transaction: %v", mode, err)
		}
	}
	tx, err := f.conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Read(ctx, f.conn, f.cfg); !errors.Is(err, pgerrors.ErrConflict) || f.conn.PgConn().TxStatus() != 'T' {
		t.Fatalf("borrowed caller transaction: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.Exec(ctx, "SET ROLE "+pgx.Identifier{f.member}.Sanitize()); err == nil {
		t.Fatal("fixture owner should not be a member of child role")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Read(cancelled, f.conn, f.cfg); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
}

func TestCopyInventoryRecoversPrivatePayloadWithoutRereadingSource(t *testing.T) {
	f := newInventoryFixture(t)
	i, err := Read(t.Context(), f.conn, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := i.PayloadForSealing()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.conn.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	recovered, err := RecoverPrivatePayload(raw, f.cfg, i.fingerprint)
	if err != nil || recovered.Summary() != i.Summary() {
		t.Fatalf("original private inventory was not recovered: %v", err)
	}
	for _, mode := range []string{"setting", "identity", "key", "fingerprint", "unknown_field", "trailing_json", "version"} {
		bad := append([]byte{}, raw...)
		cfg := f.cfg
		fingerprint := i.fingerprint
		switch mode {
		case "setting":
			bad = bytes.ReplaceAll(bad, []byte("private-setting-must-be-sealed"), []byte("changed-private-setting"))
		case "identity":
			cfg.RoleName = f.member
		case "key":
			cfg.FingerprintKey[1] = 31
		case "fingerprint":
			fingerprint = strings.Repeat("0", 64)
		case "unknown_field":
			bad = append([]byte(`{"unknown":true,`), bad[1:]...)
		case "trailing_json":
			bad = append(bad, []byte(`{}`)...)
		case "version":
			bad = bytes.Replace(bad, []byte(`"version":1`), []byte(`"version":2`), 1)
		}
		if recovered, err := RecoverPrivatePayload(bad, cfg, fingerprint); !errors.Is(err, pgerrors.ErrConflict) || recovered.fingerprint != "" {
			t.Fatalf("accepted %s persisted substitution: %v", mode, err)
		}
	}
}
