// adr: 20261116213000 — custom Postgres group roles.
package managedpostgres

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresCustomRoleStoreDeclaresSettlesAndFences(t *testing.T) {
	s, pool, ctx, account := postgresStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	database := postgresReadyDatabase(t, s, account, "windmill", now.Add(-time.Hour))
	user := CustomRole{AccountID: account, DatabaseID: database.ID, Name: "windmill_user", GrantedTo: []CredentialAccess{CredentialMigration, CredentialReadWrite}}
	saved, err := s.SaveCustomRole(ctx, user, CustomRolesMax, now)
	if err != nil || saved.State != CustomRolePending || saved.Revision != 1 || len(saved.GrantedTo) != 2 {
		t.Fatalf("save user: %+v %v", saved, err)
	}
	admin := CustomRole{AccountID: account, DatabaseID: database.ID, Name: "windmill_admin", BypassRLS: true, MemberOf: []string{"windmill_user"}}
	if _, err := s.SaveCustomRole(ctx, admin, CustomRolesMax, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveCustomRole(ctx, CustomRole{AccountID: account, DatabaseID: database.ID, Name: "orphan", MemberOf: []string{"missing"}}, CustomRolesMax, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing parent: %v", err)
	}
	if _, err := s.SaveCustomRole(ctx, CustomRole{AccountID: account, DatabaseID: database.ID, Name: "windmill_user", MemberOf: []string{"windmill_admin"}}, CustomRolesMax, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cycle: %v", err)
	}
	if _, err := s.SaveCustomRole(ctx, CustomRole{AccountID: account, DatabaseID: database.ID, Name: "third"}, 2, now); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("limit: %v", err)
	}
	if bypass, err := s.HasBypassRLSCustomRole(ctx, account, database.ID); err != nil || !bypass {
		t.Fatalf("bypass: %v %v", bypass, err)
	}
	due, err := s.DueCustomRoleDatabases(ctx, 10, now)
	if err != nil || len(due) != 1 || due[0].DatabaseID != database.ID || due[0].AccountID != account {
		t.Fatalf("due: %+v %v", due, err)
	}
	if err := s.FailCustomRoleApply(ctx, account, database.ID, "unavailable", now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if due, err := s.DueCustomRoleDatabases(ctx, 10, now); err != nil || len(due) != 0 {
		t.Fatalf("due after failure: %+v %v", due, err)
	}

	// Only the revisions an apply read are settled.
	if _, err := s.SaveCustomRole(ctx, user, CustomRolesMax, now); err != nil { // revision 2
		t.Fatal(err)
	}
	if err := s.FinishCustomRoleApply(ctx, account, database.ID, map[string]int64{"windmill_user": 1, "windmill_admin": 1}, now); err != nil {
		t.Fatal(err)
	}
	roles, err := s.ListCustomRoles(ctx, account, database.ID)
	if err != nil || len(roles) != 2 {
		t.Fatalf("list: %+v %v", roles, err)
	}
	for _, role := range roles {
		switch role.Name {
		case "windmill_admin":
			if role.State != CustomRoleReady || role.LastError != "" || !role.BypassRLS || len(role.MemberOf) != 1 {
				t.Fatalf("admin not settled: %+v", role)
			}
		case "windmill_user":
			if role.State != CustomRolePending || role.Revision != 2 {
				t.Fatalf("newer user revision settled by an older apply: %+v", role)
			}
		}
	}

	// Data API reservations and BYPASSRLS roles exclude each other.
	app, err := state.NewPgStore(pool).CreateApp(ctx, state.App{AccountID: account, Slug: "roles-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	dataAPI := testBinding(account, database.ID, app.ID, uuid.NewString(), now)
	dataAPI.Access, dataAPI.EnvironmentKey = CredentialDataAPI, "DATA_API_URL"
	if _, _, err := s.ReserveBinding(ctx, dataAPI); !errors.Is(err, ErrBypassRLSConflict) {
		t.Fatalf("data api reservation with a BYPASSRLS role: %v", err)
	}
	if _, err := s.DeleteCustomRole(ctx, account, database.ID, "windmill_user", now); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete parent: %v", err)
	}
	deleting, err := s.DeleteCustomRole(ctx, account, database.ID, "windmill_admin", now)
	if err != nil || deleting.State != CustomRoleDeleting {
		t.Fatalf("delete admin: %+v %v", deleting, err)
	}
	if err := s.FinishCustomRoleApply(ctx, account, database.ID, map[string]int64{"windmill_admin": deleting.Revision}, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReserveBinding(ctx, dataAPI); err != nil {
		t.Fatalf("data api reservation without BYPASSRLS roles: %v", err)
	}
	if _, err := s.SaveCustomRole(ctx, admin, CustomRolesMax, now); !errors.Is(err, ErrBypassRLSConflict) {
		t.Fatalf("BYPASSRLS role with a Data API binding: %v", err)
	}
	// Another account sees nothing.
	if _, err := s.ListCustomRoles(ctx, uuid.NewString(), database.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other account: %v", err)
	}
}
