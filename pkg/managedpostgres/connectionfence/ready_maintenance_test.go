// adr:531
package connectionfence

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence/sqlc"
)

func readyMaintenanceConfig(t *testing.T, f bootstrapFixture) BootstrapConfig {
	t.Helper()
	row, err := sqlc.New().MaintenanceBootstrapIdentity(t.Context(), f.root)
	if err != nil {
		t.Fatal(err)
	}
	return BootstrapConfig{SourceDatabase: f.request.DatabaseNames[0], SourceRole: f.admin, SourcePostgresMajor: int(row.ServerVersion / 10000)}
}

func TestReadyMaintenanceRecoversWhileSourceAdmissionIsClosed(t *testing.T) {
	f := newBootstrapFixture(t)
	ctx := t.Context()
	ready, err := f.bootstrap(t).Activate(ctx, f.created(t))
	if err != nil {
		t.Fatal(err)
	}
	config := readyMaintenanceConfig(t, f)
	conn, err := f.connect(t.Context(), t, MaintenanceDatabase, f.admin)
	if err != nil {
		t.Fatal(err)
	}
	if actual, err := AuthenticateReadyMaintenance(ctx, conn, config, ready); err != nil || actual != ready {
		t.Fatalf("ready authentication: %+v %v", actual, err)
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
		t.Fatal(err)
	}
	if err := c.Install(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Close(ctx, f.request); err != nil {
		t.Fatal(err)
	}
	if _, err := f.connect(t.Context(), t, config.SourceDatabase, f.admin); err == nil {
		t.Fatal("source admitted a new connection after closure")
	}
	// Recovery opens only the private database after losing the source path.
	fresh, err := f.connect(t.Context(), t, MaintenanceDatabase, f.admin)
	if err != nil {
		t.Fatal(err)
	}
	if actual, err := AuthenticateReadyMaintenance(ctx, fresh, config, ready); err != nil || actual != ready {
		t.Fatalf("closed-source recovery: %+v %v", actual, err)
	}
	for _, action := range []string{"reserve", "activate", "retire"} {
		_, err := sqlc.New().MaintenanceBootstrapAction(ctx, fresh, sqlc.MaintenanceBootstrapActionParams{
			OwnerToken: tokenUUID(ready.OwnerToken), SourceResourceID: ready.SourceResourceID, Action: action,
			ExpectedOwner: pgtype.Uint32{Uint32: ready.OwnerOID, Valid: true}, ExpectedDatabase: pgtype.Uint32{Uint32: ready.DatabaseOID, Valid: true}})
		if !errors.Is(classifyError(err), managedpostgres.ErrInvalid) {
			t.Fatalf("private observer admitted %s mutation: %v", action, classifyError(err))
		}
	}
	if _, err := c.Abandon(ctx, f.request.Identity); err != nil {
		t.Fatal(err)
	}
	terminal, err := c.Observe(ctx, f.request.Identity)
	if err != nil || terminal.State != "released" || terminal.ReleasedAt.IsZero() {
		t.Fatalf("independent terminal observation: %+v %v", terminal, err)
	}
	if _, err := f.connect(t.Context(), t, config.SourceDatabase, f.admin); err != nil {
		t.Fatalf("original source admission was not restored: %v", err)
	}
}

func TestReadyMaintenanceRejectsReceiptAndSessionSubstitution(t *testing.T) {
	f := newBootstrapFixture(t)
	ctx := t.Context()
	ready, err := f.bootstrap(t).Activate(ctx, f.created(t))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := f.connect(t.Context(), t, MaintenanceDatabase, f.admin)
	if err != nil {
		t.Fatal(err)
	}
	config := readyMaintenanceConfig(t, f)
	for _, fault := range []string{"token", "source", "owner_oid", "database_oid", "state", "missing_oid", "role", "major", "source_session", "session_role"} {
		t.Run(fault, func(t *testing.T) {
			r, cfg, session := ready, config, conn
			switch fault {
			case "token":
				r.OwnerToken = uuid.NewString()
				r.OwnerRole = maintenanceOwnerRole(r.Identity)
			case "source":
				r.SourceResourceID += "-other"
			case "owner_oid":
				r.OwnerOID++
			case "database_oid":
				r.DatabaseOID++
			case "state":
				r.State = "reserved"
			case "missing_oid":
				r.DatabaseOID = 0
			case "role":
				cfg.SourceRole = f.tenant
			case "major":
				cfg.SourcePostgresMajor++
			case "source_session":
				session, err = f.connect(ctx, t, cfg.SourceDatabase, f.admin)
				if err != nil {
					t.Fatal(err)
				}
			case "session_role":
				if _, err := sqlc.New().SetMaintenanceOwnerRole(ctx, conn, ready.OwnerRole); err != nil {
					t.Fatal(err)
				}
				cfg.SourceRole = ready.OwnerRole
			}
			if actual, err := AuthenticateReadyMaintenance(ctx, session, cfg, r); err == nil || actual != (Maintenance{}) {
				t.Fatalf("accepted %s substitution: %+v %v", fault, actual, err)
			}
			if fault == "session_role" {
				if err := sqlc.New().ResetMaintenanceOwnerRole(ctx, conn); err != nil {
					t.Fatal(err)
				}
			}
			if actual, err := AuthenticateReadyMaintenance(ctx, conn, config, ready); err != nil || actual != ready {
				t.Fatalf("rejected observation changed ownership: %+v %v", actual, err)
			}
		})
	}
}
