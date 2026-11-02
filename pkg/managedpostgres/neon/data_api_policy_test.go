// adr: 1664 — role reconciliation must upgrade only the exact legacy policy.
package neon

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func (f *credentialFixture) dataAPIRole() credentialRole {
	role := f.runtime
	role.name = strings.Replace(role.name, "gregale_rt_", "gregale_api_", 1)
	role.marker = strings.TrimSuffix(role.marker, ":read_write") + ":data_api"
	role.access = managedpostgres.CredentialDataAPI
	f.extraRoles = append(f.extraRoles, role.name)
	return role
}

func TestSQLDataAPILockPolicyUpgradePreservesCredentialsAndSessions(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	role := f.dataAPIRole()
	if err := f.manager.Ensure(ctx, f.material, role); err != nil {
		t.Fatal(err)
	}
	current := f.connect(t, role.name)
	var statementMS, lockMS int
	settingsSQL := `SELECT (extract(epoch FROM current_setting('statement_timeout')::interval)*1000)::int,
 (extract(epoch FROM current_setting('lock_timeout')::interval)*1000)::int`
	if err := current.QueryRow(ctx, settingsSQL).Scan(&statementMS, &lockMS); err != nil || statementMS != api.DataAPIQueryTimeoutMS || lockMS != api.DataAPILockTimeoutMS {
		t.Fatalf("new session policy: statement=%d lock=%d err=%v", statementMS, lockMS, err)
	}
	executeSQL(t, f.admin, "ALTER ROLE "+roleIdentifier(role.name)+" RESET lock_timeout")
	legacy := f.connect(t, role.name)
	var passwordBefore, passwordAfter string
	if err := f.admin.QueryRow(ctx, "SELECT rolpassword FROM pg_authid WHERE rolname=$1", role.name).Scan(&passwordBefore); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := f.manager.Ensure(ctx, f.material, role); err != nil {
			t.Fatalf("compatible replay: %v", err)
		}
	}
	if err := f.admin.QueryRow(ctx, "SELECT rolpassword FROM pg_authid WHERE rolname=$1", role.name).Scan(&passwordAfter); err != nil || passwordBefore == "" || passwordBefore != passwordAfter {
		t.Fatal("policy upgrade changed credentials", err)
	}
	if err := legacy.QueryRow(ctx, settingsSQL).Scan(&statementMS, &lockMS); err != nil || lockMS != 0 || statementMS != api.DataAPIQueryTimeoutMS {
		t.Fatalf("existing session was altered: statement=%d lock=%d err=%v", statementMS, lockMS, err)
	}
	fresh := f.connect(t, role.name)
	if err := fresh.QueryRow(ctx, settingsSQL).Scan(&statementMS, &lockMS); err != nil || lockMS != api.DataAPILockTimeoutMS || statementMS != api.DataAPIQueryTimeoutMS {
		t.Fatalf("reconnected session policy: statement=%d lock=%d err=%v", statementMS, lockMS, err)
	}
	// Qualification checks effective settings, including more-specific overrides.
	executeSQL(t, fresh, "SET lock_timeout=0")
	if err := managedpostgres.VerifyDataAPICredentialProbe(ctx, fresh, f.admin, role.schemaOwner, "must_not_create"); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("qualification accepted an ineffective lock policy: %v", err)
	}
}

func TestSQLDataAPILockPolicyRejectsDriftWithoutRepair(t *testing.T) {
	for _, drift := range []string{"explicit zero", "different lock", "different statement", "extra setting", "membership", "marker", "inherit", "retired", "outside privilege"} {
		t.Run(drift, func(t *testing.T) {
			f := newCredentialFixture(t)
			ctx := context.Background()
			role := f.dataAPIRole()
			if err := f.manager.Ensure(ctx, f.material, role); err != nil {
				t.Fatal(err)
			}
			name := roleIdentifier(role.name)
			executeSQL(t, f.admin, "ALTER ROLE "+name+" RESET lock_timeout")
			switch drift {
			case "explicit zero":
				executeSQL(t, f.admin, "ALTER ROLE "+name+" SET lock_timeout=0")
			case "different lock":
				executeSQL(t, f.admin, "ALTER ROLE "+name+" SET lock_timeout=6000")
			case "different statement":
				executeSQL(t, f.admin, "ALTER ROLE "+name+" SET statement_timeout=1")
			case "extra setting":
				executeSQL(t, f.admin, "ALTER ROLE "+name+" SET search_path=api")
			case "membership":
				executeSQL(t, f.admin, "GRANT "+roleIdentifier(role.schemaOwner)+" TO "+name)
			case "marker":
				executeSQL(t, f.admin, "COMMENT ON ROLE "+name+" IS 'unrelated'")
			case "inherit":
				executeSQL(t, f.admin, "ALTER ROLE "+name+" INHERIT")
			case "retired":
				executeSQL(t, f.admin, "ALTER ROLE "+name+" NOLOGIN")
			case "outside privilege":
				executeSQL(t, f.admin, "CREATE TABLE public.private_data(id integer); GRANT SELECT ON public.private_data TO "+name)
			}
			var before, after []string
			query := "SELECT rolconfig FROM pg_roles WHERE rolname=$1"
			if err := f.admin.QueryRow(ctx, query, role.name).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if err := f.manager.Ensure(ctx, f.material, role); !errors.Is(err, managedpostgres.ErrConflict) {
				t.Fatalf("expected conflict: %v", err)
			}
			if err := f.admin.QueryRow(ctx, query, role.name).Scan(&after); err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("conflicting policy was changed: %v -> %v (%v)", before, after, err)
			}
		})
	}
}
