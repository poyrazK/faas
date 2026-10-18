package neon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func refreshRotationBodies(t *testing.T) ([]string, string) {
	t.Helper()
	source, err := os.ReadFile("../../../cmd/gregale/templates/data-api/refresh-rotation.mjs")
	if err != nil {
		t.Fatal(err)
	}
	bodies := []string{}
	for i, name := range []string{"rotationCheckBody", "rotationStatusBody", "rotationWitnessBody", "rotationPrepareBody"} {
		match := regexp.MustCompile("const " + name + " = `([\\s\\S]*?)`").FindSubmatch(source)
		if len(match) != 2 {
			t.Fatal("missing rotation hook contract", name)
		}
		hash := sha256.Sum256(match[1])
		if hex.EncodeToString(hash[:]) != refreshRotationHashes()[i] {
			t.Fatal("provider and runtime rotation hook differ", name)
		}
		bodies = append(bodies, string(match[1]))
	}
	ddl := regexp.MustCompile("export const rotationTablesDDL = `([\\s\\S]*?)`").FindSubmatch(source)
	if len(ddl) != 2 {
		t.Fatal("missing rotation tables contract")
	}
	legacy, err := os.ReadFile("../../../cmd/gregale/templates/data-api/refresh.mjs")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range regexp.MustCompile(`AS \$function\$([\s\S]*?)\$function\$`).FindAllSubmatch(legacy, -1) {
		hash := sha256.Sum256(body[1])
		if !strings.Contains(string(source), hex.EncodeToString(hash[:])) {
			t.Fatal("rotation upgrade does not pin the current legacy hook")
		}
	}
	return bodies, string(ddl[1])
}

func TestRefreshRotationContractMatchesRuntime(t *testing.T) { refreshRotationBodies(t) }

func installRefreshRotationFixture(t *testing.T, f *credentialFixture, role credentialRole, deadline bool) {
	t.Helper()
	bodies, ddl := refreshRotationBodies(t)
	executeSQL(t, f.admin, "SET ROLE "+roleIdentifier(role.schemaOwner))
	executeSQL(t, f.admin, `CREATE SCHEMA gregale_data_api;REVOKE ALL ON SCHEMA gregale_data_api FROM PUBLIC;
 CREATE TABLE gregale_data_api.refresh_gate(role_name text PRIMARY KEY,paused boolean NOT NULL,token_hash text,fingerprint text NOT NULL);
 CREATE TABLE api.rotation_notes(subject text NOT NULL);INSERT INTO api.rotation_notes VALUES('alice'),('bob');
 ALTER TABLE api.rotation_notes ENABLE ROW LEVEL SECURITY;
 CREATE POLICY own_notes ON api.rotation_notes USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
 CREATE FUNCTION api.rotation_count() RETURNS bigint LANGUAGE SQL SECURITY INVOKER AS 'SELECT count(*) FROM api.rotation_notes';
 COMMENT ON FUNCTION api.rotation_count() IS '@gregale:rpc';REVOKE ALL ON FUNCTION api.rotation_count() FROM PUBLIC;
 CREATE FUNCTION api.rotation_unapproved() RETURNS integer LANGUAGE SQL AS 'SELECT 1';REVOKE ALL ON FUNCTION api.rotation_unapproved() FROM PUBLIC;`)
	executeSQL(t, f.admin, ddl)
	definitions := []struct{ name, args, returns, language, security string }{
		{"check_request", "", "void", "plpgsql", "DEFINER"},
		{"refresh_status", "candidate_hash text", "TABLE(paused boolean,fingerprint text,owns_pause boolean)", "sql", "DEFINER"},
		{"refresh_catalog_witness", "subject name", "text", "sql", "INVOKER"},
		{"prepare_refresh_member", "binding uuid,resource_scope text,candidate name", "void", "plpgsql", "INVOKER"},
	}
	for i, fn := range definitions {
		executeSQL(t, f.admin, "CREATE FUNCTION gregale_data_api."+fn.name+"("+fn.args+") RETURNS "+fn.returns+" LANGUAGE "+fn.language+" SECURITY "+fn.security+" VOLATILE SET search_path=pg_catalog AS $body$"+bodies[i]+"$body$; REVOKE ALL ON FUNCTION gregale_data_api."+fn.name+"("+fn.args+") FROM PUBLIC")
	}
	executeSQL(t, f.admin, "GRANT USAGE ON SCHEMA gregale_data_api TO "+roleIdentifier(role.name)+";GRANT EXECUTE ON FUNCTION gregale_data_api.check_request(),gregale_data_api.refresh_status(text),api.rotation_count() TO "+roleIdentifier(role.name))
	if deadline {
		for i, name := range []string{"enforce_deadline", "check_deadline_request"} {
			executeSQL(t, f.admin, "CREATE FUNCTION gregale_data_api."+name+"() RETURNS void LANGUAGE plpgsql SECURITY INVOKER VOLATILE SET search_path=pg_catalog AS $body$"+deadlineBodies(t)[i]+"$body$;REVOKE ALL ON FUNCTION gregale_data_api."+name+"() FROM PUBLIC; GRANT EXECUTE ON FUNCTION gregale_data_api."+name+"() TO "+roleIdentifier(role.name))
		}
	}
	executeSQL(t, f.admin, `INSERT INTO gregale_data_api.refresh_rotation_bindings VALUES($1::uuid,$2,1,$3,$4,false,NULL)`, role.bindingID, role.scope, role.name, deadline)
	executeSQL(t, f.admin, `INSERT INTO gregale_data_api.refresh_rotation_members SELECT $1::text,r.oid,$2::uuid,$3,repeat('a',64),gregale_data_api.refresh_catalog_witness(($1::text)::name),ARRAY['api.rotation_count()'::regprocedure::oid] FROM pg_roles r WHERE r.rolname=$1::text`, role.name, role.bindingID, role.scope)
	executeSQL(t, f.admin, "RESET ROLE")
}

func TestSQLRefreshRotationPreparesAndRetiresOnlyOldGeneration(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "refresh", true: "refresh with deadline"}[deadline], func(t *testing.T) {
			f := newCredentialFixture(t)
			ctx := context.Background()
			first := f.dataAPIRole()
			first.bindingID = uuid.NewString()
			if err := f.manager.Ensure(ctx, f.material, first); err != nil {
				t.Fatal(err)
			}
			installRefreshRotationFixture(t, f, first, deadline)
			next := first
			next.name += "_next"
			f.extraRoles = append(f.extraRoles, next.name)
			old := f.connect(t, first.name)
			for range 2 {
				if err := f.manager.Ensure(ctx, f.material, next); err != nil {
					t.Fatal("prepare replacement", err)
				}
			}
			current := f.connect(t, next.name)
			for _, session := range []struct {
				name    string
				subject string
			}{{first.name, "alice"}, {next.name, "bob"}} {
				conn := old
				if session.name == next.name {
					conn = current
				}
				executeSQL(t, conn, "BEGIN")
				executeSQL(t, conn, `SELECT set_config('request.jwt.claims',jsonb_build_object('sub',$1::text,'gregale_serving_fingerprint',repeat('a',64))::text,true)`, session.subject)
				executeSQL(t, conn, `SELECT gregale_data_api.check_request()`)
				var subject string
				if err := conn.QueryRow(ctx, `SELECT subject FROM api.rotation_notes`).Scan(&subject); err != nil || subject != session.subject {
					t.Fatal("RLS lost during rotation", subject, err)
				}
				var count int
				if err := conn.QueryRow(ctx, `SELECT api.rotation_count()`).Scan(&count); err != nil || count != 1 {
					t.Fatal("approved RPC lost during rotation", count, err)
				}
				executeSQL(t, conn, "COMMIT")
			}
			deniedSQL(t, current, `SELECT * FROM gregale_data_api.refresh_rotation_bindings`)
			deniedSQL(t, current, `SELECT gregale_data_api.refresh_catalog_witness(current_user::name)`)
			deniedSQL(t, current, `SELECT api.rotation_unapproved()`)
			executeSQL(t, f.admin, `UPDATE gregale_data_api.refresh_rotation_bindings SET paused=true,token_hash=repeat('b',64)`)
			if err := f.manager.Revoke(ctx, f.material, first); err != nil {
				t.Fatal("retire old generation", err)
			}
			if _, err := old.Exec(ctx, "SELECT 1"); err == nil {
				t.Fatal("old serving session survived retirement")
			}
			var paused bool
			var members int
			var source string
			if err := f.admin.QueryRow(ctx, `SELECT paused,source_role,(SELECT count(*) FROM gregale_data_api.refresh_rotation_members) FROM gregale_data_api.refresh_rotation_bindings`).Scan(&paused, &source, &members); err != nil || !paused || members != 1 || source != next.name {
				t.Fatal("retirement changed the replacement gate", paused, source, members, err)
			}
			executeSQL(t, current, `SELECT 1`)
			if err := f.manager.Revoke(ctx, f.material, first); err != nil {
				t.Fatal("retirement retry", err)
			}
			if err := f.manager.Revoke(ctx, f.material, next); err != nil {
				t.Fatal("last generation retirement", err)
			}
			if err := f.admin.QueryRow(ctx, `SELECT count(*) FROM gregale_data_api.refresh_rotation_bindings`).Scan(&members); err != nil || members != 0 {
				t.Fatal("last retirement retained binding state", members, err)
			}
		})
	}
}

func TestSQLRefreshRotationFailsClosedAndResumesAfterOwnerRepair(t *testing.T) {
	for _, drift := range []string{"hook body", "missing search path", "definer", "public execute", "private helper grant", "table RLS", "table public", "column public", "table shape", "missing table", "witness", "role OID", "source marker", "source attribute", "RPC definition", "RPC privilege", "policy role"} {
		t.Run(drift, func(t *testing.T) {
			f := newCredentialFixture(t)
			ctx := context.Background()
			first := f.dataAPIRole()
			first.bindingID = uuid.NewString()
			if err := f.manager.Ensure(ctx, f.material, first); err != nil {
				t.Fatal(err)
			}
			installRefreshRotationFixture(t, f, first, false)
			next := first
			next.name += "_next"
			f.extraRoles = append(f.extraRoles, next.name)
			query := map[string]string{
				"hook body":            `CREATE OR REPLACE FUNCTION gregale_data_api.check_request() RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS 'BEGIN NULL; END'`,
				"missing search path":  `ALTER FUNCTION gregale_data_api.check_request() RESET search_path`,
				"definer":              `ALTER FUNCTION gregale_data_api.check_request() SECURITY INVOKER`,
				"public execute":       `GRANT EXECUTE ON FUNCTION gregale_data_api.check_request() TO PUBLIC`,
				"private helper grant": `GRANT EXECUTE ON FUNCTION gregale_data_api.prepare_refresh_member(uuid,text,name) TO ` + roleIdentifier(first.name),
				"table RLS":            `ALTER TABLE gregale_data_api.refresh_rotation_members ENABLE ROW LEVEL SECURITY`,
				"table public":         `GRANT SELECT ON gregale_data_api.refresh_rotation_bindings TO PUBLIC`,
				"column public":        `GRANT SELECT(fingerprint) ON gregale_data_api.refresh_rotation_members TO PUBLIC`,
				"table shape":          `ALTER TABLE gregale_data_api.refresh_rotation_members ADD COLUMN unexpected text`,
				"missing table":        `ALTER TABLE gregale_data_api.refresh_rotation_members RENAME TO hidden_members`,
				"witness":              `UPDATE gregale_data_api.refresh_rotation_members SET catalog_witness=repeat('0',64)`,
				"role OID":             `UPDATE gregale_data_api.refresh_rotation_members SET role_oid='10'::oid`,
				"source marker":        `COMMENT ON ROLE ` + roleIdentifier(first.name) + ` IS 'unrelated'`,
				"source attribute":     `ALTER ROLE ` + roleIdentifier(first.name) + ` CREATEROLE`,
				"RPC definition":       `CREATE OR REPLACE FUNCTION api.rotation_count() RETURNS bigint LANGUAGE SQL AS 'SELECT 999::bigint'`,
				"RPC privilege":        `REVOKE EXECUTE ON FUNCTION api.rotation_count() FROM ` + roleIdentifier(first.name),
				"policy role":          `ALTER POLICY own_notes ON api.rotation_notes TO ` + roleIdentifier(first.name),
			}[drift]
			executeSQL(t, f.admin, query)
			if err := f.manager.Ensure(ctx, f.material, next); !errors.Is(err, managedpostgres.ErrConflict) {
				t.Fatalf("drift did not block preparation: %v", err)
			}
			var exists bool
			if err := f.admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, next.name).Scan(&exists); err != nil || exists {
				t.Fatal("failed preparation committed candidate", err)
			}
			var source string
			if err := f.admin.QueryRow(ctx, `SELECT source_role FROM gregale_data_api.refresh_rotation_bindings`).Scan(&source); err != nil || source != first.name {
				t.Fatal("failed preparation advanced source", source, err)
			}
			if drift == "witness" {
				executeSQL(t, f.admin, "SET ROLE "+roleIdentifier(first.schemaOwner))
				executeSQL(t, f.admin, `UPDATE gregale_data_api.refresh_rotation_members SET catalog_witness=gregale_data_api.refresh_catalog_witness(role_name::name)`)
				executeSQL(t, f.admin, "RESET ROLE")
				if err := f.manager.Ensure(ctx, f.material, next); err != nil {
					t.Fatal("owner repair could not resume generation", err)
				}
			}
		})
	}
}

func TestSQLRefreshRotationRejectsLoginSpecificRLSApproval(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	first := f.dataAPIRole()
	first.bindingID = uuid.NewString()
	if err := f.manager.Ensure(ctx, f.material, first); err != nil {
		t.Fatal(err)
	}
	installRefreshRotationFixture(t, f, first, false)
	// The source remains approved and unchanged. Only effective applicability of
	// this policy differs for a candidate; raw policy metadata is identical.
	executeSQL(t, f.admin, "ALTER POLICY own_notes ON api.rotation_notes TO "+roleIdentifier(first.name))
	executeSQL(t, f.admin, "SET ROLE "+roleIdentifier(first.schemaOwner))
	executeSQL(t, f.admin, `UPDATE gregale_data_api.refresh_rotation_members SET catalog_witness=gregale_data_api.refresh_catalog_witness(role_name::name)`)
	executeSQL(t, f.admin, "RESET ROLE")
	next := first
	next.name += "_next"
	f.extraRoles = append(f.extraRoles, next.name)
	if err := f.manager.Ensure(ctx, f.material, next); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatal("candidate adopted source-only RLS policy", err)
	}
	var exists bool
	if err := f.admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, next.name).Scan(&exists); err != nil || exists {
		t.Fatal("failed RLS preparation committed candidate", err)
	}
}
