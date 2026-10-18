package neon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func deadlineBodies(t *testing.T) []string {
	t.Helper()
	source, err := os.ReadFile("../../../cmd/gregale/templates/data-api/sql-deadline.mjs")
	if err != nil {
		t.Fatal(err)
	}
	bodies := []string{}
	for i, name := range []string{"enforceBody", "requestBody"} {
		match := regexp.MustCompile("const " + name + " = `([\\s\\S]*?)`").FindSubmatch(source)
		if len(match) != 2 {
			t.Fatal("missing runtime deadline contract", name)
		}
		body := strings.NewReplacer("${limits.queryMs}", strconv.Itoa(api.DataAPIQueryTimeoutMS), "${limits.lockMs}", strconv.Itoa(api.DataAPILockTimeoutMS)).Replace(string(match[1]))
		hash := sha256.Sum256([]byte(body))
		if hex.EncodeToString(hash[:]) != []string{deadlineEnforceSHA256, deadlineRequestSHA256}[i] {
			t.Fatal("provider deadline policy differs from the embedded runtime contract", name)
		}
		bodies = append(bodies, body)
	}
	return bodies
}

func TestSQLDeadlinePolicyMatchesRuntime(t *testing.T) { deadlineBodies(t) }

func TestSQLDeadlineBindingIdentityValidationAndForwarding(t *testing.T) {
	request := managedpostgres.CredentialRequest{ProviderResourceID: "project-123", IdentityKey: "generation", IdempotencyKey: "issue", Access: managedpostgres.CredentialDataAPI}
	p := &Provider{organizationID: "org", databaseName: "app"}
	legacy := p.credentialRole(request)
	for _, id := range []string{"invalid", uuid.Nil.String(), strings.ToUpper("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")} {
		request.BindingID = id
		if err := validateCredentialRequest(request); !errors.Is(err, managedpostgres.ErrInvalid) {
			t.Fatal("accepted invalid binding policy identity", id, err)
		}
	}
	request.BindingID = uuid.NewString()
	if err := validateCredentialRequest(request); err != nil {
		t.Fatal(err)
	}
	role := p.credentialRole(request)
	if role.bindingID != request.BindingID || role.name != legacy.name || role.marker != legacy.marker {
		t.Fatal("policy identity changed credential derivation or was lost")
	}
}

func installDeadlinePolicyFixture(t *testing.T, f *credentialFixture, role credentialRole) {
	t.Helper()
	bodies := deadlineBodies(t)
	executeSQL(t, f.admin, "SET ROLE "+roleIdentifier(role.schemaOwner))
	executeSQL(t, f.admin, `CREATE SCHEMA IF NOT EXISTS gregale_data_api;
 REVOKE ALL ON SCHEMA gregale_data_api FROM PUBLIC;
 CREATE OR REPLACE FUNCTION gregale_data_api.enforce_deadline() RETURNS void LANGUAGE plpgsql SECURITY INVOKER VOLATILE SET search_path=pg_catalog AS $body$`+bodies[0]+`$body$;
 CREATE OR REPLACE FUNCTION gregale_data_api.check_deadline_request() RETURNS void LANGUAGE plpgsql SECURITY INVOKER VOLATILE SET search_path=pg_catalog AS $body$`+bodies[1]+`$body$;
 REVOKE ALL ON FUNCTION gregale_data_api.enforce_deadline(),gregale_data_api.check_deadline_request() FROM PUBLIC;
 CREATE TABLE gregale_data_api.deadline_binding_policies(binding_id uuid NOT NULL,scope text NOT NULL,version integer NOT NULL,enforce_sha256 text NOT NULL,request_sha256 text NOT NULL,PRIMARY KEY(binding_id,scope));`)
	executeSQL(t, f.admin, `INSERT INTO gregale_data_api.deadline_binding_policies VALUES($1::uuid,$2,1,$3,$4)`, role.bindingID, role.scope, deadlineEnforceSHA256, deadlineRequestSHA256)
	executeSQL(t, f.admin, "RESET ROLE")
}

func TestSQLDeadlineRotationPreparesRestrictedLoginAndPreservesActiveRLS(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	first := f.dataAPIRole()
	first.bindingID = uuid.NewString()
	if err := f.manager.Ensure(ctx, f.material, first); err != nil {
		t.Fatal(err)
	}
	installDeadlinePolicyFixture(t, f, first)
	if err := f.manager.Ensure(ctx, f.material, first); err != nil {
		t.Fatal(err)
	}
	executeSQL(t, f.admin, "SET ROLE "+roleIdentifier(first.schemaOwner))
	executeSQL(t, f.admin, `CREATE TABLE api.rotation_notes(subject text NOT NULL);
 INSERT INTO api.rotation_notes VALUES('alice'),('bob'); ALTER TABLE api.rotation_notes ENABLE ROW LEVEL SECURITY;
 CREATE POLICY own_notes ON api.rotation_notes USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
 CREATE FUNCTION api.unapproved_rpc() RETURNS integer LANGUAGE SQL AS 'SELECT 1';
 REVOKE ALL ON FUNCTION api.unapproved_rpc() FROM PUBLIC;`)
	executeSQL(t, f.admin, "RESET ROLE")
	old := f.connect(t, first.name)
	claims := `{"sub":"alice","gregale_sql_deadline":{"version":1,"expires_at_ms":` + strconv.FormatInt(time.Now().Add(10*time.Second).UnixMilli(), 10) + `,"coordinated_refresh":false}}`
	executeSQL(t, old, "BEGIN")
	executeSQL(t, old, `SELECT set_config('request.jwt.claims',$1,true)`, claims)
	executeSQL(t, old, `SELECT gregale_data_api.check_deadline_request()`)
	// Keep an admitted, RLS-scoped request open while replacement preparation runs.
	active := make(chan error, 1)
	go func() { _, err := old.Exec(ctx, `SELECT pg_sleep(0.3) FROM api.rotation_notes`); active <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var running bool
		if err := f.admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE usename=$1 AND state='active')`, first.name).Scan(&running); err != nil {
			t.Fatal(err)
		}
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("serving request did not become active")
		}
		time.Sleep(time.Millisecond)
	}
	next := first
	next.name += "_next"
	f.extraRoles = append(f.extraRoles, next.name)
	if err := f.manager.Ensure(ctx, f.material, next); err != nil {
		t.Fatal(err)
	}
	if err := <-active; err != nil {
		t.Fatal("rotation interrupted the serving request", err)
	}
	var subject string
	if err := old.QueryRow(ctx, `SELECT subject FROM api.rotation_notes`).Scan(&subject); err != nil || subject != "alice" {
		t.Fatal("old login lost RLS scope", subject, err)
	}
	executeSQL(t, old, "COMMIT")
	replacement := f.connect(t, next.name)
	for range 2 {
		if err := f.manager.Ensure(ctx, f.material, next); err != nil {
			t.Fatal("idempotent preparation", err)
		}
	}
	executeSQL(t, replacement, "BEGIN")
	executeSQL(t, replacement, `SELECT set_config('request.jwt.claims',$1,true)`, strings.Replace(claims, "alice", "bob", 1))
	executeSQL(t, replacement, `SELECT gregale_data_api.check_deadline_request()`)
	if err := replacement.QueryRow(ctx, `SELECT subject FROM api.rotation_notes`).Scan(&subject); err != nil || subject != "bob" {
		t.Fatal("replacement lost RLS scope", subject, err)
	}
	executeSQL(t, replacement, "COMMIT")
	deniedSQL(t, replacement, `SELECT * FROM gregale_data_api.deadline_binding_policies`)
	deniedSQL(t, replacement, `SELECT api.unapproved_rpc()`)
	if err := f.manager.Revoke(ctx, f.material, first); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(ctx, "SELECT 1"); err == nil {
		t.Fatal("retired session survived")
	}
	executeSQL(t, replacement, "SELECT 1")
}

func TestSQLDeadlineRotationFailsClosedBeforeCandidateCommit(t *testing.T) {
	for _, drift := range []string{"body", "missing hook", "definer", "owner", "public execute", "search path", "missing search path", "schema public", "table public", "column grant", "table owner", "shape", "primary key", "RLS", "view", "version", "hash", "coordinated refresh"} {
		t.Run(drift, func(t *testing.T) {
			f := newCredentialFixture(t)
			ctx := context.Background()
			first := f.dataAPIRole()
			first.bindingID = uuid.NewString()
			if err := f.manager.Ensure(ctx, f.material, first); err != nil {
				t.Fatal(err)
			}
			installDeadlinePolicyFixture(t, f, first)
			if err := f.manager.Ensure(ctx, f.material, first); err != nil {
				t.Fatal(err)
			}
			serving := f.connect(t, first.name)
			var before, after string
			if err := f.admin.QueryRow(ctx, `SELECT rolpassword FROM pg_authid WHERE rolname=$1`, first.name).Scan(&before); err != nil {
				t.Fatal(err)
			}
			query := map[string]string{
				"body":                `CREATE OR REPLACE FUNCTION gregale_data_api.enforce_deadline() RETURNS void LANGUAGE plpgsql SET search_path=pg_catalog AS 'BEGIN NULL; END'`,
				"missing hook":        `DROP FUNCTION gregale_data_api.enforce_deadline()`,
				"definer":             `ALTER FUNCTION gregale_data_api.enforce_deadline() SECURITY DEFINER`,
				"owner":               `ALTER FUNCTION gregale_data_api.enforce_deadline() OWNER TO ` + roleIdentifier(f.config.User),
				"public execute":      `GRANT EXECUTE ON FUNCTION gregale_data_api.enforce_deadline() TO PUBLIC`,
				"search path":         `ALTER FUNCTION gregale_data_api.enforce_deadline() SET search_path=public`,
				"missing search path": `ALTER FUNCTION gregale_data_api.enforce_deadline() RESET search_path`,
				"schema public":       `GRANT USAGE ON SCHEMA gregale_data_api TO PUBLIC`,
				"table public":        `GRANT SELECT ON gregale_data_api.deadline_binding_policies TO PUBLIC`,
				"column grant":        `GRANT SELECT(version) ON gregale_data_api.deadline_binding_policies TO PUBLIC`,
				"table owner":         `ALTER TABLE gregale_data_api.deadline_binding_policies OWNER TO ` + roleIdentifier(f.config.User),
				"shape":               `ALTER TABLE gregale_data_api.deadline_binding_policies ADD COLUMN unexpected text`,
				"primary key":         `ALTER TABLE gregale_data_api.deadline_binding_policies DROP CONSTRAINT deadline_binding_policies_pkey`,
				"RLS":                 `ALTER TABLE gregale_data_api.deadline_binding_policies ENABLE ROW LEVEL SECURITY`,
				"view":                `ALTER TABLE gregale_data_api.deadline_binding_policies RENAME TO hidden_policy; CREATE VIEW gregale_data_api.deadline_binding_policies AS SELECT * FROM gregale_data_api.hidden_policy WHERE false`,
				"version":             `UPDATE gregale_data_api.deadline_binding_policies SET version=2`,
				"hash":                `UPDATE gregale_data_api.deadline_binding_policies SET enforce_sha256=repeat('0',64)`,
				"coordinated refresh": `CREATE TABLE gregale_data_api.refresh_gate(role_name text)`,
			}[drift]
			executeSQL(t, f.admin, query)
			next := first
			next.name += "_next"
			f.extraRoles = append(f.extraRoles, next.name)
			if err := f.manager.Ensure(ctx, f.material, next); !errors.Is(err, managedpostgres.ErrConflict) {
				t.Fatalf("accepted drift: %v", err)
			}
			var exists bool
			if err := f.admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, next.name).Scan(&exists); err != nil || exists {
				t.Fatal("failed preparation committed candidate", err)
			}
			if err := f.admin.QueryRow(ctx, `SELECT rolpassword FROM pg_authid WHERE rolname=$1`, first.name).Scan(&after); err != nil || before != after {
				t.Fatal("failed preparation changed serving credentials", err)
			}
			executeSQL(t, serving, "SELECT 1")
			// Explicit owner repair permits the same generation to resume.
			if drift == "version" {
				executeSQL(t, f.admin, `UPDATE gregale_data_api.deadline_binding_policies SET version=1`)
				if err := f.manager.Ensure(ctx, f.material, next); err != nil {
					t.Fatal("repaired preparation could not resume", err)
				}
			}
		})
	}
}

func TestSQLDeadlinePolicyDoesNotOptInOtherBindingsOrScopes(t *testing.T) {
	for _, kind := range []string{"other binding", "other scope", "standalone"} {
		t.Run(kind, func(t *testing.T) {
			f := newCredentialFixture(t)
			ctx := context.Background()
			role := f.dataAPIRole()
			role.bindingID = uuid.NewString()
			if err := f.manager.Ensure(ctx, f.material, role); err != nil {
				t.Fatal(err)
			}
			installDeadlinePolicyFixture(t, f, role)
			switch kind {
			case "other binding":
				role.bindingID = uuid.NewString()
			case "other scope":
				role.scope = strings.Repeat("0", 40)
			case "standalone":
				role.bindingID = ""
			}
			if err := f.manager.Ensure(ctx, f.material, role); err != nil {
				t.Fatal(err)
			}
			var granted bool
			if err := f.admin.QueryRow(ctx, `SELECT has_function_privilege($1,'gregale_data_api.enforce_deadline()','EXECUTE')`, role.name).Scan(&granted); err != nil || granted {
				t.Fatal("unapproved binding received hook execution", err)
			}
		})
	}
}

func TestSQLDeadlinePreparationUsesExplicitOwnerAuthority(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	role := f.dataAPIRole()
	role.bindingID = uuid.NewString()
	if err := f.manager.Ensure(ctx, f.material, role); err != nil {
		t.Fatal(err)
	}
	installDeadlinePolicyFixture(t, f, role)
	preparer := "deadline_preparer_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	f.extraRoles = append(f.extraRoles, preparer)
	executeSQL(t, f.admin, "CREATE ROLE "+roleIdentifier(preparer)+" NOLOGIN NOINHERIT; GRANT "+roleIdentifier(role.schemaOwner)+" TO "+roleIdentifier(preparer))
	tx, err := f.admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+roleIdentifier(preparer)); err != nil {
		t.Fatal(err)
	}
	var inherited bool
	if err := tx.QueryRow(ctx, `SELECT has_table_privilege(current_user,c.oid,'SELECT') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='gregale_data_api' AND c.relname='deadline_binding_policies'`).Scan(&inherited); err != nil || inherited {
		t.Fatal("fixture inherited private policy access", inherited, err)
	}
	if err := prepareSQLDeadlineCredential(ctx, tx, role); err != nil {
		t.Fatal("preparation relied on implicit owner inheritance", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
