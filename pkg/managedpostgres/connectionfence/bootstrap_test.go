// adr:567
package connectionfence

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence/sqlc"
)

type bootstrapFixture struct {
	fixture
	identity Identity
}

func newBootstrapFixture(t *testing.T) bootstrapFixture {
	t.Helper()
	f := bootstrapFixture{fixture: newFixture(t), identity: Identity{OwnerToken: uuid.NewString(), SourceResourceID: "project/private-source-" + uuid.NewString()}}
	var exists bool
	if err := f.root.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)", MaintenanceDatabase).Scan(&exists); err != nil || exists {
		t.Fatalf("bootstrap contract requires an unoccupied maintenance name: %v exists=%v", err, exists)
	}
	if err := f.root.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname=$1)", maintenanceOwnerRole(f.identity)).Scan(&exists); err != nil || exists {
		t.Fatalf("bootstrap contract requires a fresh owner identity: %v exists=%v", err, exists)
	}
	// Model the API-created Neon application role's CREATEROLE/CREATEDB.
	// All roles/databases used here belong to this fresh, isolated fixture.
	for _, role := range []string{f.admin, f.tenant} {
		if _, err := f.root.Exec(t.Context(), "ALTER ROLE "+pgx.Identifier{role}.Sanitize()+" CREATEDB CREATEROLE"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Even on a concurrently used test server, never remove a database
		// owned by an unrelated role which acquired this fixed name.
		var owner string
		err := f.root.QueryRow(ctx, "SELECT r.rolname FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname=$1", MaintenanceDatabase).Scan(&owner)
		if err == nil {
			if owner != f.admin && owner != f.tenant && owner != maintenanceOwnerRole(f.identity) {
				t.Errorf("leaving unrelated maintenance database owned by %q", owner)
			} else if _, err := f.root.Exec(ctx, "DROP DATABASE gregale_checkpoint WITH (FORCE)"); err != nil {
				t.Error(err)
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			t.Error(err)
		}
		if _, err := f.root.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{maintenanceOwnerRole(f.identity)}.Sanitize()); err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f bootstrapFixture) bootstrap(t *testing.T) *Bootstrap {
	t.Helper()
	conn, err := f.connect(t.Context(), t, f.request.DatabaseNames[0], f.admin)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewBootstrap(conn, BootstrapConfig{SourceDatabase: f.request.DatabaseNames[0], SourceRole: f.admin})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f bootstrapFixture) reserved(t *testing.T) Maintenance {
	t.Helper()
	receipt, err := f.bootstrap(t).Reserve(t.Context(), f.identity)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func (f bootstrapFixture) created(t *testing.T) Maintenance {
	t.Helper()
	receipt, err := f.bootstrap(t).Create(t.Context(), f.reserved(t))
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestMaintenanceBootstrapRecoversOwnershipAndActivatesPrivately(t *testing.T) {
	f := newBootstrapFixture(t)
	ctx := t.Context()
	reserved := f.reserved(t)
	if reserved.State != "reserved" || reserved.OwnerOID == 0 || reserved.DatabaseOID != 0 {
		t.Fatalf("reservation: %+v", reserved)
	}
	replayed := f.reserved(t)
	if replayed != reserved {
		t.Fatalf("reservation recovery changed ownership: %+v %+v", reserved, replayed)
	}
	client, err := f.connect(t.Context(), t, f.request.DatabaseNames[0], f.tenant)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"ALTER ROLE " + pgx.Identifier{reserved.OwnerRole}.Sanitize() + " LOGIN",
		"GRANT " + pgx.Identifier{reserved.OwnerRole}.Sanitize() + " TO " + pgx.Identifier{f.tenant}.Sanitize(),
		"COMMENT ON ROLE " + pgx.Identifier{reserved.OwnerRole}.Sanitize() + " IS 'forged'",
		"SET ROLE " + pgx.Identifier{reserved.OwnerRole}.Sanitize(),
	} {
		if _, err := client.Exec(ctx, statement); err == nil {
			t.Fatalf("application administrator acquired private maintenance authority: %s", statement)
		}
	}
	created, err := f.bootstrap(t).Create(ctx, reserved)
	if err != nil || created.State != "reserved" || created.DatabaseOID == 0 || created.OwnerOID != reserved.OwnerOID {
		t.Fatalf("create: %+v %v", created, err)
	}
	var allows bool
	if err := f.root.QueryRow(ctx, "SELECT datallowconn FROM pg_database WHERE oid=$1", created.DatabaseOID).Scan(&allows); err != nil || allows {
		t.Fatalf("database was not created closed: %v %v", allows, err)
	}
	if _, err := f.connect(t.Context(), t, MaintenanceDatabase, f.tenant); err == nil {
		t.Fatal("application connected before activation")
	}
	// Simulate losing the response after CREATE: recovery has only the durable
	// owner OID. Both observe and create reuse the exact existing database.
	for _, recover := range []func(context.Context, Maintenance) (Maintenance, error){f.bootstrap(t).Observe, f.bootstrap(t).Create} {
		observed, err := recover(ctx, reserved)
		if err != nil || observed != created {
			t.Fatalf("create response recovery: %+v %v", observed, err)
		}
	}
	ready, err := f.bootstrap(t).Activate(ctx, created)
	if err != nil || ready.State != "ready" || ready.DatabaseOID != created.DatabaseOID || ready.OwnerOID != created.OwnerOID {
		t.Fatalf("activation: %+v %v", ready, err)
	}
	for range 2 {
		observed, err := f.bootstrap(t).Activate(ctx, created)
		if err != nil || observed != ready {
			t.Fatalf("activation recovery: %+v %v", observed, err)
		}
	}
	if _, err := f.connect(t.Context(), t, MaintenanceDatabase, f.tenant); err == nil {
		t.Fatal("application connected after private activation")
	}
	poolConfig := f.fixture.bootstrap.Copy()
	poolConfig.ConnConfig.Database, poolConfig.ConnConfig.User = MaintenanceDatabase, f.admin
poolConfig.ConnConfig.Password = f.password
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	c, err := New(ctx, pool, Config{MaintenanceDatabase: MaintenanceDatabase, MaintenanceRole: f.admin,
		MaintenanceDatabaseOID: ready.DatabaseOID, MaintenanceOwnerOID: ready.OwnerOID})
	if err != nil {
		t.Fatalf("inherited private database ownership: %v", err)
	}
	for _, pins := range [][2]uint32{{ready.DatabaseOID + 1, ready.OwnerOID}, {ready.DatabaseOID, ready.OwnerOID + 1}} {
		if _, err := New(ctx, pool, Config{MaintenanceDatabase: MaintenanceDatabase, MaintenanceRole: f.admin,
			MaintenanceDatabaseOID: pins[0], MaintenanceOwnerOID: pins[1]}); !errors.Is(err, managedpostgres.ErrUnsupported) {
			t.Fatalf("controller ignored bootstrap OID pins: %v", err)
		}
	}
	if err := c.Install(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Close(ctx, f.request); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Release(ctx, f.request.Identity); err != nil {
		t.Fatal(err)
	}
	if _, err := f.bootstrap(t).RetireReserved(ctx, ready); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("reserved cleanup retired an installed maintenance database: %v", err)
	}
	if _, err := c.Observe(ctx, f.request.Identity); err != nil {
		t.Fatalf("rejected cleanup changed installed maintenance: %v", err)
	}
	var permanent bool
	if err := f.bootstrap(t).conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname='gregale_checkpoint')").Scan(&permanent); err != nil || permanent {
		t.Fatalf("bootstrap polluted shared source database schema: %v %v", permanent, err)
	}
}

func TestMaintenanceBootstrapRejectsSubstitution(t *testing.T) {
	for _, fault := range []string{"unknown_database", "unknown_role", "forged_creator", "source", "owner_pin", "database_pin", "database_owner", "database_opened", "foreign_grant", "owner_login", "foreign_member", "ready_public_grant", "ready_database_replaced"} {
		t.Run(fault, func(t *testing.T) {
			f := newBootstrapFixture(t)
			ctx := t.Context()
			role := maintenanceOwnerRole(f.identity)
			if fault == "unknown_database" {
				if _, err := f.root.Exec(ctx, "CREATE DATABASE gregale_checkpoint OWNER "+pgx.Identifier{f.tenant}.Sanitize()+" TEMPLATE template0"); err != nil {
					t.Fatal(err)
				}
				if _, err := f.bootstrap(t).Reserve(ctx, f.identity); !errors.Is(err, managedpostgres.ErrConflict) {
					t.Fatalf("adopted unrelated database: %v", err)
				}
				return
			}
			if fault == "unknown_role" || fault == "forged_creator" {
				creator, err := f.connect(t.Context(), t, f.request.DatabaseNames[0], f.tenant)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := creator.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN CREATEDB"); err != nil {
					t.Fatal(err)
				}
				if fault == "forged_creator" {
					var actorOID, ownerOID uint32
					if err := f.root.QueryRow(ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", f.admin).Scan(&actorOID); err != nil {
						t.Fatal(err)
					}
					if err := f.root.QueryRow(ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", role).Scan(&ownerOID); err != nil {
						t.Fatal(err)
					}
					marker, _ := json.Marshal(map[string]any{"protocol": "gregale-maintenance-v1", "owner_token": f.identity.OwnerToken,
						"source_resource_id": f.identity.SourceResourceID, "actor_oid": actorOID, "owner_oid": ownerOID, "database_oid": 0, "state": "reserved"})
					if _, err := creator.Exec(ctx, "GRANT "+pgx.Identifier{role}.Sanitize()+" TO "+pgx.Identifier{f.admin}.Sanitize()+" WITH ADMIN TRUE, INHERIT TRUE, SET TRUE"); err != nil {
						t.Fatal(err)
					}
					// COMMENT cannot parameterize its value; quote the JSON literal.
					if _, err := creator.Exec(ctx, "COMMENT ON ROLE "+pgx.Identifier{role}.Sanitize()+" IS '"+strings.ReplaceAll(string(marker), "'", "''")+"'"); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := f.bootstrap(t).Reserve(ctx, f.identity); !errors.Is(err, managedpostgres.ErrConflict) {
					t.Fatalf("adopted %s: %v", fault, err)
				}
				return
			}
			receipt := f.created(t)
			if strings.HasPrefix(fault, "ready_") {
				var err error
				receipt, err = f.bootstrap(t).Activate(ctx, receipt)
				if err != nil {
					t.Fatal(err)
				}
			}
			switch fault {
			case "source":
				receipt.SourceResourceID += "-other"
			case "owner_pin":
				receipt.OwnerOID++
			case "database_pin":
				receipt.DatabaseOID++
			case "database_owner":
				_, err := f.root.Exec(ctx, "ALTER DATABASE gregale_checkpoint OWNER TO "+pgx.Identifier{f.tenant}.Sanitize())
				if err != nil {
					t.Fatal(err)
				}
			case "database_opened":
				if _, err := f.root.Exec(ctx, "ALTER DATABASE gregale_checkpoint ALLOW_CONNECTIONS true"); err != nil {
					t.Fatal(err)
				}
			case "foreign_grant":
				if _, err := f.root.Exec(ctx, "GRANT CONNECT ON DATABASE gregale_checkpoint TO "+pgx.Identifier{f.tenant}.Sanitize()); err != nil {
					t.Fatal(err)
				}
			case "owner_login":
				if _, err := f.root.Exec(ctx, "ALTER ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN"); err != nil {
					t.Fatal(err)
				}
			case "foreign_member":
				if _, err := f.root.Exec(ctx, "GRANT "+pgx.Identifier{role}.Sanitize()+" TO "+pgx.Identifier{f.tenant}.Sanitize()); err != nil {
					t.Fatal(err)
				}
			case "ready_public_grant":
				if _, err := f.root.Exec(ctx, "GRANT CONNECT ON DATABASE gregale_checkpoint TO PUBLIC"); err != nil {
					t.Fatal(err)
				}
			case "ready_database_replaced":
				if _, err := f.root.Exec(ctx, "DROP DATABASE gregale_checkpoint"); err != nil {
					t.Fatal(err)
				}
				if _, err := f.root.Exec(ctx, "CREATE DATABASE gregale_checkpoint OWNER "+pgx.Identifier{role}.Sanitize()+" TEMPLATE template0"); err != nil {
					t.Fatal(err)
				}
				if _, err := f.root.Exec(ctx, "REVOKE ALL ON DATABASE gregale_checkpoint FROM PUBLIC"); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "foreign_grant" {
				if _, err := f.bootstrap(t).Activate(ctx, receipt); !errors.Is(err, managedpostgres.ErrConflict) {
					t.Fatalf("activated shared maintenance: %v", err)
				}
			} else if _, err := f.bootstrap(t).Observe(ctx, receipt); !errors.Is(err, managedpostgres.ErrConflict) {
				t.Fatalf("accepted %s: %v", fault, err)
			}
		})
	}
}

func TestMaintenanceBootstrapRetirementFencesDelayedCreation(t *testing.T) {
	for _, point := range []string{"before_reserve", "after_reserve", "after_create"} {
		t.Run(point, func(t *testing.T) {
			f := newBootstrapFixture(t)
			ctx := t.Context()
			receipt := Maintenance{Identity: f.identity}
			if point == "after_reserve" {
				receipt = f.reserved(t)
			} else if point == "after_create" {
				receipt = f.created(t)
			}
			// Select the owner before retirement so the following SQL CREATE
			// exercises a previously admitted session's refreshed authority.
			lateConn, err := f.connect(t.Context(), t, f.request.DatabaseNames[0], f.admin)
			if err != nil {
				t.Fatal(err)
			}
			q := sqlc.New()
			if point != "before_reserve" {
				if _, err := q.SetMaintenanceOwnerRole(ctx, lateConn, receipt.OwnerRole); err != nil {
					t.Fatal(err)
				}
			}
			terminal, err := f.bootstrap(t).RetireReserved(ctx, receipt)
			if err != nil || terminal.State != "retired" || terminal.OwnerOID == 0 || terminal.DatabaseOID != receipt.DatabaseOID {
				t.Fatalf("retirement: %+v %v", terminal, err)
			}
			for range 2 {
				observed, err := f.bootstrap(t).RetireReserved(ctx, terminal)
				if err != nil || observed != terminal {
					t.Fatalf("terminal recovery changed: %+v %v", observed, err)
				}
			}
			if _, err := f.bootstrap(t).Reserve(ctx, f.identity); !errors.Is(err, managedpostgres.ErrConflict) {
				t.Fatalf("late reserve revived retired owner: %v", err)
			}
			late := terminal
			late.State = "reserved"
			if _, err := f.bootstrap(t).Create(ctx, late); !errors.Is(err, managedpostgres.ErrConflict) {
				t.Fatalf("late create revived retired owner: %v", err)
			}
			// Even a connection that had already selected the owner cannot
			// create after retirement has removed its CREATEDB attribute.
			if point == "before_reserve" {
				if _, err := q.SetMaintenanceOwnerRole(ctx, lateConn, terminal.OwnerRole); err != nil {
					t.Fatal(err)
				}
			}
			if err := q.CreateMaintenanceDatabase(ctx, lateConn); err == nil {
				t.Fatal("retired owner retained database creation authority")
			}
			if err := q.ResetMaintenanceOwnerRole(ctx, lateConn); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMaintenanceBootstrapCancellationKeepsRecovery(t *testing.T) {
	f := newBootstrapFixture(t)
	ctx := t.Context()
	receipt := f.reserved(t)
	blocker, err := f.connect(t.Context(), t, f.request.DatabaseNames[0], f.admin)
	if err != nil {
		t.Fatal(err)
	}
	q := sqlc.New()
	if err := q.LockMaintenanceBootstrap(ctx, blocker); err != nil {
		t.Fatal(err)
	}
	b := f.bootstrap(t)
	pending, cancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { _, err := b.Create(pending, receipt); result <- err }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var waiting bool
		if err := f.root.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event='advisory')", b.conn.PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("bootstrap did not reach the independently observed lock wait")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled dispatch: %v", err)
	}
	if !b.conn.IsClosed() {
		t.Fatal("cancelled bootstrap session was reused")
	}
	if unlocked, err := q.UnlockMaintenanceBootstrap(ctx, blocker); err != nil || !unlocked {
		t.Fatalf("release test blocker: %v %v", unlocked, err)
	}
	terminal, err := f.bootstrap(t).RetireReserved(ctx, receipt)
	if err != nil || terminal.State != "retired" {
		t.Fatalf("replacement recovery: %+v %v", terminal, err)
	}
	if _, err := f.bootstrap(t).Create(ctx, receipt); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("late request escaped terminal owner: %v", err)
	}
}

func TestMaintenanceBootstrapRequiresAuthenticatedConnection(t *testing.T) {
	for _, fault := range []string{"database", "role", "postgres_major", "shared_source_role"} {
		t.Run(fault, func(t *testing.T) {
			f := newBootstrapFixture(t)
			ctx := t.Context()
			b := f.bootstrap(t)
			switch fault {
			case "database":
				b.config.SourceDatabase = f.request.DatabaseNames[1]
			case "role":
				b.config.SourceRole = f.tenant
			case "postgres_major":
				row, err := sqlc.New().MaintenanceBootstrapIdentity(ctx, b.conn)
				if err != nil {
					t.Fatal(err)
				}
				b.config.SourcePostgresMajor = int(row.ServerVersion/10000) + 1
			case "shared_source_role":
				if _, err := f.root.Exec(ctx, "GRANT "+pgx.Identifier{f.admin}.Sanitize()+" TO "+pgx.Identifier{f.tenant}.Sanitize()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := b.Reserve(ctx, f.identity); !errors.Is(err, managedpostgres.ErrUnsupported) {
				t.Fatalf("%s source was not authenticated: %v", fault, err)
			}
			if !b.conn.IsClosed() {
				t.Fatal("rejected connection was reused")
			}
			var exists bool
			if err := f.root.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname=$1)", maintenanceOwnerRole(f.identity)).Scan(&exists); err != nil || exists {
				t.Fatalf("unqualified connection created an owner: %v %v", exists, err)
			}
		})
	}
}
