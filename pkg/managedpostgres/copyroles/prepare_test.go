// adr:531
package copyroles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	rolesql "github.com/onebox-faas/faas/pkg/managedpostgres/copyroles/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

type fixture struct {
	sourceRoot, targetRoot, source, target *pgx.Conn
	sourcePlan                             copyinventory.ExportPlan
	targetInventory                        copyinventory.Inventory
	pins                                   copyarchive.RestoreTarget
	dispositions                           []Disposition
	owner, member, group                   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	sourceURL, targetURL := os.Getenv("DATABASE_URL"), os.Getenv("FAAS_COPY_ROLES_TARGET_DATABASE_URL")
	if sourceURL == "" || targetURL == "" {
		t.Skip("two independent local PostgreSQL clusters required for role seeding contracts")
	}
	f := &fixture{}
	var err error
	f.sourceRoot, err = pgx.Connect(t.Context(), sourceURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.sourceRoot.Close(context.Background()) })
	f.targetRoot, err = pgx.Connect(t.Context(), targetURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.targetRoot.Close(context.Background()) })
	for _, c := range []*pgx.Conn{f.sourceRoot, f.targetRoot} {
		if !strings.HasPrefix(c.Config().Host, "/") {
			t.Fatal("role contracts require private Unix sockets")
		}
	}
	var sourceSystem, targetSystem string
	if err = f.sourceRoot.QueryRow(t.Context(), "SELECT system_identifier::text FROM pg_control_system()").Scan(&sourceSystem); err != nil {
		t.Fatal(err)
	}
	if err = f.targetRoot.QueryRow(t.Context(), "SELECT system_identifier::text FROM pg_control_system()").Scan(&targetSystem); err != nil {
		t.Fatal(err)
	}
	if sourceSystem == targetSystem {
		t.Fatal("source and target role catalogues must belong to independent clusters")
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	f.owner = "grg_role_owner_" + suffix
	f.member = "role/ ?é \" ' ; --" + suffix
	f.group = "grg_role_group_" + suffix
	db := "grg_role_catalog_" + suffix
	createdRoles := map[*pgx.Conn][]string{}
	createdDBs := map[*pgx.Conn][]string{}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, c := range []*pgx.Conn{f.source, f.target} {
			if c != nil {
				_ = c.Close(ctx)
			}
		}
		for _, root := range []*pgx.Conn{f.sourceRoot, f.targetRoot} {
			if root == f.targetRoot {
				_, _ = root.Exec(ctx, "REVOKE SET ON PARAMETER \"app.api_key\" FROM "+pgx.Identifier{f.owner}.Sanitize())
			}
			for _, name := range createdDBs[root] {
				if _, err := root.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
					t.Error(err)
				}
			}
			// A failed test might have committed seed roles before a reply was lost.
			for _, name := range []string{f.member, f.group} {
				if root == f.targetRoot {
					_, _ = root.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{name}.Sanitize())
				}
			}
			for n := len(createdRoles[root]) - 1; n >= 0; n-- {
				if _, err := root.Exec(ctx, "DROP ROLE "+pgx.Identifier{createdRoles[root][n]}.Sanitize()); err != nil {
					t.Error(err)
				}
			}
		}
	})
	for _, root := range []*pgx.Conn{f.sourceRoot, f.targetRoot} {
		if _, err = root.Exec(t.Context(), "CREATE ROLE "+pgx.Identifier{f.owner}.Sanitize()+" LOGIN CREATEROLE CREATEDB PASSWORD 'bootstrap-only-password'"); err != nil {
			t.Fatal(err)
		}
		createdRoles[root] = append(createdRoles[root], f.owner)
		if _, err = root.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{db}.Sanitize()+" OWNER "+pgx.Identifier{f.owner}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		createdDBs[root] = append(createdDBs[root], db)
	}
	for _, r := range []struct{ name, attrs string }{
		{f.member, "LOGIN NOINHERIT CREATEDB CONNECTION LIMIT 7 PASSWORD 'source-password-never-copied' VALID UNTIL '2040-02-03 04:05:06+03'"},
		{f.group, "NOLOGIN INHERIT CREATEROLE"},
	} {
		if _, err = f.sourceRoot.Exec(t.Context(), "CREATE ROLE "+pgx.Identifier{r.name}.Sanitize()+" "+r.attrs); err != nil {
			t.Fatal(err)
		}
		createdRoles[f.sourceRoot] = append(createdRoles[f.sourceRoot], r.name)
	}
	// PG16 requires explicit SET authority for undeclared custom placeholders.
	// The worker must preserve this precondition, not omit the captured setting.
	if _, err = f.targetRoot.Exec(t.Context(), "GRANT SET ON PARAMETER \"app.api_key\" TO "+pgx.Identifier{f.owner}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"ALTER ROLE " + pgx.Identifier{f.member}.Sanitize() + " SET app.api_key TO 'private-value=''semi; -- é'",
		"ALTER ROLE " + pgx.Identifier{f.member}.Sanitize() + " SET search_path TO 'weird, schema', public",
		"ALTER ROLE " + pgx.Identifier{f.member}.Sanitize() + " SET timezone TO 'Europe/Istanbul'",
		"ALTER ROLE " + pgx.Identifier{f.group}.Sanitize() + " SET work_mem TO '8192kB'",
		"ALTER ROLE " + pgx.Identifier{f.group}.Sanitize() + " SET search_path TO 'Schema \"quoted\"; , é', '" + strings.Repeat("long", 20) + "', ''",
	} {
		if _, err = f.sourceRoot.Exec(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range []struct {
		root *pgx.Conn
		out  **pgx.Conn
	}{{f.sourceRoot, &f.source}, {f.targetRoot, &f.target}} {
		cfg := pair.root.Config().Copy()
		cfg.User, cfg.Password, cfg.Database = f.owner, "bootstrap-only-password", db
		cfg.RuntimeParams = map[string]string{"search_path": "pg_catalog", "default_transaction_read_only": "off"}
		*pair.out, err = pgx.ConnectConfig(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	scope := copyinventory.Scope{PostgresMajor: 16, OperationID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString(), SourceDatabaseID: uuid.NewString(), CaptureDatabaseID: uuid.NewString(),
		SourceVersion: strings.Repeat("a", 64), BackendID: "neon", BackendFingerprint: strings.Repeat("b", 64), SourceProviderResourceID: "source-project", SourceDataResourceID: "source-project/source-branch", ProviderSnapshotID: "snapshot", CaptureProviderResourceID: "native-capture", CapturePoint: at, SnapshotCreatedAt: at.Add(time.Second), CaptureCreatedAt: at.Add(2 * time.Second)}
	f.pins = copyarchive.RestoreTarget{Scope: scope, OwnerID: uuid.NewString(), ProviderResourceID: "independent-target", DataResourceID: "independent-target/root", EndpointID: "ep-independent", ProviderCreatedAt: at.Add(3 * time.Second), EndpointCreatedAt: at.Add(4 * time.Second), DatabaseName: db, RoleName: f.owner}
	f.refreshSource(t)
	i, cfg := readCatalogue(t, f.target)
	f.targetInventory = i
	f.pins.DatabaseOID, f.pins.RoleOID = cfg.DatabaseOID, cfg.RoleOID
	src, _ := f.sourcePlan.RoleCatalogueForWorker()
	base, _ := i.RoleCatalogueForWorker()
	ids := map[string]uint32{}
	for _, r := range base.Roles {
		ids[r.Name] = r.OID
	}
	for _, r := range src.Roles {
		f.dispositions = append(f.dispositions, Disposition{SourceOID: r.OID, ExistingTargetOID: ids[r.Name]})
	}
	return f
}
func readCatalogue(t *testing.T, c *pgx.Conn) (copyinventory.Inventory, copyinventory.Config) {
	t.Helper()
	cfg := copyinventory.Config{DatabaseName: c.Config().Database, RoleName: c.Config().User}
	cfg.FingerprintKey[0] = 37
	if err := c.QueryRow(t.Context(), "SELECT current_setting('server_version_num')::int/10000,d.oid,r.oid FROM pg_database d,pg_roles r WHERE d.datname=current_database() AND r.rolname=current_user").Scan(&cfg.PostgresMajor, &cfg.DatabaseOID, &cfg.RoleOID); err != nil {
		t.Fatal(err)
	}
	i, err := copyinventory.Read(t.Context(), c, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return i, cfg
}
func (f *fixture) refreshSource(t *testing.T) {
	t.Helper()
	i, _ := readCatalogue(t, f.source)
	var err error
	f.sourcePlan, err = i.PlanExports(f.pins.Scope, nil)
	if err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) plan(t *testing.T) Plan {
	t.Helper()
	p, err := NewPlan(f.sourcePlan, f.targetInventory, f.pins, f.dispositions)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func allow(context.Context, copyarchive.RestoreTarget) error { return nil }

func TestRoleSeedsPreserveAttributesResetPasswordsAndRecoverExactPrivatePlan(t *testing.T) {
	f := newFixture(t)
	p := f.plan(t)
	if p.Summary().Created != 2 || p.Summary().DeferredLogins != 1 {
		t.Fatal("incomplete role dispositions")
	}
	key, _ := age.GenerateX25519Identity()
	other, _ := age.GenerateX25519Identity()
	sealed, err := Seal(key.Recipient(), p)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := Open([]*age.X25519Identity{other, key}, f.sourcePlan, f.pins, sealed)
	if err != nil || !reflect.DeepEqual(recovered, p) {
		t.Fatalf("original encrypted plan recovery: %v", err)
	}
	for _, value := range []any{p, sealed, recovered} {
		out, _ := json.Marshal(value)
		for _, secret := range []string{f.owner, f.member, "private-value", "bootstrap-only-password", "source-password-never-copied"} {
			if bytes.Contains(out, []byte(secret)) || strings.Contains(fmt.Sprintf("%+v %#v", value, value), secret) || bytes.Contains(sealed.Ciphertext, []byte(secret)) {
				t.Fatal("role plan exposed private metadata")
			}
		}
	}
	r, err := Prepare(t.Context(), f.target, recovered, allow)
	if err != nil {
		t.Fatal(err)
	}
	ids := r.IdentitiesForWorker()
	if len(ids) != p.Summary().Roles || r.SeededAt().IsZero() {
		t.Fatal("role identities missing")
	}
	var valid bool
	if err = f.targetRoot.QueryRow(t.Context(), `SELECT NOT a.rolcanlogin AND a.rolpassword IS NULL AND NOT a.rolinherit AND a.rolcreatedb AND NOT a.rolcreaterole AND a.rolconnlimit=7 AND a.rolvaliduntil='2040-02-03 01:05:06+00'::timestamptz AND r.rolconfig=ARRAY['app.api_key=private-value=''semi; -- é','search_path="weird, schema", public','TimeZone=Europe/Istanbul'] FROM pg_authid a JOIN pg_roles r ON r.oid=a.oid WHERE a.rolname=$1`, f.member).Scan(&valid); err != nil || !valid {
		t.Fatalf("target role attributes/password reset: %v", err)
	}
	if err = f.sourceRoot.QueryRow(t.Context(), "SELECT rolcanlogin AND rolpassword IS NOT NULL FROM pg_authid WHERE rolname=$1", f.member).Scan(&valid); err != nil || !valid {
		t.Fatalf("target seeding changed source role: %v", err)
	}
	if err = f.targetRoot.QueryRow(t.Context(), `SELECT admin_option AND NOT inherit_option AND NOT set_option FROM pg_auth_members WHERE roleid=(SELECT oid FROM pg_roles WHERE rolname=$1) AND member=(SELECT oid FROM pg_roles WHERE rolname=$2)`, f.member, f.owner).Scan(&valid); err != nil || !valid {
		t.Fatalf("unexpected automatic creator authority: %v", err)
	}
	replay, err := Prepare(t.Context(), f.target, recovered, allow)
	if err != nil || !reflect.DeepEqual(replay.IdentitiesForWorker(), ids) || !replay.SeededAt().Equal(r.SeededAt()) {
		t.Fatalf("replay replaced SQL identity/time: %v", err)
	}
	ids[0].TargetOID++
	if reflect.DeepEqual(ids, r.IdentitiesForWorker()) {
		t.Fatal("mutable receipt aliases private identities")
	}
}

func TestRoleSeedConcurrentWorkersShareOneCommitAndRecheckAuthorityAfterWaiting(t *testing.T) {
	t.Run("concurrent_first_commit", func(t *testing.T) {
		f := newFixture(t)
		p := f.plan(t)
		other, err := pgx.ConnectConfig(t.Context(), f.target.Config().Copy())
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close(context.Background())
		type result struct {
			receipt Receipt
			err     error
		}
		results := make(chan result, 2)
		for _, c := range []*pgx.Conn{f.target, other} {
			go func(c *pgx.Conn) { r, e := Prepare(t.Context(), c, p, allow); results <- result{r, e} }(c)
		}
		first, second := <-results, <-results
		if first.err != nil || second.err != nil || !first.receipt.SeededAt().Equal(second.receipt.SeededAt()) || !reflect.DeepEqual(first.receipt.IdentitiesForWorker(), second.receipt.IdentitiesForWorker()) {
			t.Fatalf("concurrent owners replaced SQL pins: %v / %v", first.err, second.err)
		}
	})
	t.Run("stale_waiter", func(t *testing.T) {
		f := newFixture(t)
		p := f.plan(t)
		blocker, err := pgx.ConnectConfig(t.Context(), f.target.Config().Copy())
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Close(context.Background())
		tx, err := blocker.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if err = rolesql.New().LockRoleSeed(t.Context(), tx); err != nil {
			t.Fatal(err)
		}
		var owner atomic.Bool
		owner.Store(true)
		authorized := make(chan struct{}, 1)
		done := make(chan error, 1)
		go func() {
			_, err := Prepare(t.Context(), f.target, p, func(context.Context, copyarchive.RestoreTarget) error {
				if !owner.Load() {
					return pgerrors.ErrConflict
				}
				select {
				case authorized <- struct{}{}:
				default:
				}
				return nil
			})
			done <- err
		}()
		<-authorized
		owner.Store(false)
		if err = tx.Rollback(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err = <-done; !errors.Is(err, pgerrors.ErrConflict) {
			t.Fatalf("stale waiter retained dispatch authority: %v", err)
		}
		var absent bool
		if err = f.target.QueryRow(t.Context(), "SELECT NOT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='gregale_copy_roles')").Scan(&absent); err != nil || !absent {
			t.Fatalf("stale worker installed metadata: %v", err)
		}
	})
}

func TestRoleSeedCommittedLostReplyRecoversWithoutRecreatingRoles(t *testing.T) {
	f := newFixture(t)
	p := f.plan(t)
	key, _ := age.GenerateX25519Identity()
	sealed, err := Seal(key.Recipient(), p)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	r, err := Prepare(t.Context(), f.target, p, func(context.Context, copyarchive.RestoreTarget) error {
		calls++
		if calls == 4 {
			return errors.New("private post-commit owner failure")
		}
		return nil
	})
	if !errors.Is(err, pgerrors.ErrUnavailable) || len(r.IdentitiesForWorker()) != 0 || calls != 4 {
		t.Fatalf("lost post-commit reply returned success: %v", err)
	}
	var ids []byte
	var at time.Time
	if err = f.target.QueryRow(t.Context(), "SELECT created_roles,seeded_at FROM gregale_copy_roles.receipt").Scan(&ids, &at); err != nil {
		t.Fatal(err)
	}
	_ = f.target.Close(t.Context())
	cfg := f.targetRoot.Config().Copy()
	cfg.User, cfg.Password, cfg.Database = f.owner, "bootstrap-only-password", f.pins.DatabaseName
	f.target, err = pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := Open([]*age.X25519Identity{key}, f.sourcePlan, f.pins, sealed)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := Prepare(t.Context(), f.target, recovered, allow)
	if err != nil || !replay.SeededAt().Equal(at) {
		t.Fatalf("fresh-session recovery: %v", err)
	}
	raw, _ := json.Marshal(replay.body.Created)
	var before, after any
	_ = json.Unmarshal(ids, &before)
	_ = json.Unmarshal(raw, &after)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("committed lost reply rediscovered/recreated SQL identities")
	}
	// Equivalent new ciphertext cannot rebase the pre-write target baseline.
	today, _ := readCatalogue(t, f.target)
	if _, err = NewPlan(f.sourcePlan, today, f.pins, f.dispositions); !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatalf("live target silently rebuilt ownership: %v", err)
	}
}

func TestRoleSeedFaultsRollBackAllNewRolesAndReceipt(t *testing.T) {
	for _, tc := range []struct {
		name         string
		edit         func(*testing.T, *fixture)
		beforeCommit bool
		want         error
	}{
		{"role_authority", func(t *testing.T, f *fixture) {
			_, err := f.targetRoot.Exec(t.Context(), "ALTER ROLE "+pgx.Identifier{f.owner}.Sanitize()+" NOCREATEROLE")
			if err != nil {
				t.Fatal(err)
			}
		}, false, pgerrors.ErrUnsupported},
		{"source_bypass_rls", func(t *testing.T, f *fixture) {
			_, err := f.sourceRoot.Exec(t.Context(), "ALTER ROLE "+pgx.Identifier{f.group}.Sanitize()+" BYPASSRLS")
			if err != nil {
				t.Fatal(err)
			}
			f.refreshSource(t)
		}, false, pgerrors.ErrUnsupported},
		{"parameter_authority", func(t *testing.T, f *fixture) {
			_, err := f.targetRoot.Exec(t.Context(), "REVOKE SET ON PARAMETER \"app.api_key\" FROM "+pgx.Identifier{f.owner}.Sanitize())
			if err != nil {
				t.Fatal(err)
			}
		}, false, pgerrors.ErrUnsupported},
		{"precommit_lease_loss", nil, true, pgerrors.ErrConflict},
		{"baseline_attributes", func(t *testing.T, f *fixture) {
			_, err := f.targetRoot.Exec(t.Context(), "ALTER ROLE "+pgx.Identifier{f.owner}.Sanitize()+" CONNECTION LIMIT 3")
			if err != nil {
				t.Fatal(err)
			}
		}, false, pgerrors.ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.edit != nil {
				tc.edit(t, f)
			}
			p := f.plan(t)
			calls := 0
			r, err := Prepare(t.Context(), f.target, p, func(context.Context, copyarchive.RestoreTarget) error {
				calls++
				if tc.beforeCommit && calls == 3 {
					return pgerrors.ErrConflict
				}
				return nil
			})
			if !errors.Is(err, tc.want) || len(r.IdentitiesForWorker()) != 0 || strings.Contains(fmt.Sprint(err), "private-value") {
				t.Fatalf("fault returned receipt/private error: %v", err)
			}
			var absent bool
			if err = f.targetRoot.QueryRow(t.Context(), "SELECT NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=ANY($1))", []string{f.member, f.group}).Scan(&absent); err != nil || !absent {
				t.Fatalf("failure committed partial roles: %v", err)
			}
			if err = f.target.QueryRow(t.Context(), "SELECT NOT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='gregale_copy_roles')").Scan(&absent); err != nil || !absent {
				t.Fatalf("failure committed role ledger/schema: %v", err)
			}
		})
	}
}

func TestRoleSeedRejectsCatalogueDriftReceiptSubstitutionAndSharedLedger(t *testing.T) {
	f := newFixture(t)
	p := f.plan(t)
	r, err := Prepare(t.Context(), f.target, p, allow)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, sql string }{
		{"role_attributes", "ALTER ROLE " + pgx.Identifier{f.member}.Sanitize() + " CONNECTION LIMIT 8"},
		{"shared_schema", "GRANT USAGE ON SCHEMA gregale_copy_roles TO PUBLIC"},
		{"corrupt_receipt", "UPDATE gregale_copy_roles.receipt SET created_roles='[]'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Restore each deliberate edit after the rejected replay.
			tx, err := f.target.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(t.Context(), tc.sql); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			_, err = Prepare(t.Context(), f.target, p, allow)
			if !errors.Is(err, pgerrors.ErrConflict) {
				t.Fatalf("drift/substitution accepted: %v", err)
			}
			switch tc.name {
			case "role_attributes":
				_, err = f.target.Exec(t.Context(), "ALTER ROLE "+pgx.Identifier{f.member}.Sanitize()+" CONNECTION LIMIT 7")
			case "shared_schema":
				_, err = f.target.Exec(t.Context(), "REVOKE USAGE ON SCHEMA gregale_copy_roles FROM PUBLIC")
			case "corrupt_receipt":
				raw, _ := json.Marshal(r.body.Created)
				_, err = f.target.Exec(t.Context(), "UPDATE gregale_copy_roles.receipt SET created_roles=$1::jsonb", raw)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
	other := p
	other.body.InventoryFingerprint = strings.Repeat("d", 64)
	if _, err = Prepare(t.Context(), f.target, other, allow); !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatalf("different original plan adopted receipt: %v", err)
	}
}
