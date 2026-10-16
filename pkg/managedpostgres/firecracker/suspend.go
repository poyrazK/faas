package firecracker

import (
	"context"
	"fmt"
	"strings"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Source suspension for a cutover (ADR-1370 §4; the native
// CredentialSuspender is slice 14). Suspending a binding identity stops
// every client that holds its credential, inside the catalog or not:
// its role is set NOLOGIN and its backends are terminated. pgproxyd
// authenticates a client against the role's verifier and then logs in as
// the role, so a suspended role cannot open a session even through a
// pooled server connection. Unlike revocation, suspension keeps the
// verifier and the password, so an activation aborted before publish
// resumes the same credentials unchanged.
//
// Resume re-enables exactly the roles it is given. Revocation sets NOLOGIN
// too, so callers pass only identities they suspended and did not revoke;
// an activation's fence keeps its members' bindings from being revoked
// meanwhile.

// sqlstateSessionsRemain is what the idle check raises while a suspended
// role still has a session or a prepared transaction (object_in_use).
const sqlstateSessionsRemain = "55006"

// suspendRoles sets every role NOLOGIN and terminates its backends. Safe to
// repeat; a role that does not exist is skipped.
func (p *Provider) suspendRoles(ctx context.Context, resourceID string, roles []string) error {
	batch, err := suspendSQL(roles)
	if err != nil {
		return err
	}
	return p.admin(ctx, resourceID, batch)
}

// resumeRoles lets every role log in again. Safe to repeat.
func (p *Provider) resumeRoles(ctx context.Context, resourceID string, roles []string) error {
	batch, err := resumeSQL(roles)
	if err != nil {
		return err
	}
	return p.admin(ctx, resourceID, batch)
}

// rolesIdle reports whether no session and no prepared transaction belongs
// to any of the roles. A terminated backend takes a moment to exit, so a
// suspension observes until this is true.
func (p *Provider) rolesIdle(ctx context.Context, resourceID string, roles []string) (bool, error) {
	batch, err := idleSQL(roles)
	if err != nil {
		return false, err
	}
	resp, err := p.scheduler.DatabaseAdmin(ctx, &scheddpb.DatabaseAdminRequest{DatabaseId: resourceID, Sql: batch})
	if err != nil {
		return false, providerError(err)
	}
	switch {
	case resp.GetErrorCode() == "":
		return true, nil
	case resp.GetSqlstate() == sqlstateSessionsRemain:
		return false, nil
	default:
		return false, fmt.Errorf("%w: idle check failed (sqlstate %s)", managedpostgres.ErrUnavailable, resp.GetSqlstate())
	}
}

// roleList quotes roles as a SQL array literal after checking each is a
// driver-issued role name.
func roleList(roles []string) (string, error) {
	if len(roles) == 0 {
		return "", fmt.Errorf("%w: no roles", managedpostgres.ErrInvalid)
	}
	quoted := make([]string, len(roles))
	for i, role := range roles {
		if !rolePattern.MatchString(role) {
			return "", fmt.Errorf("%w: invalid role %q", managedpostgres.ErrInvalid, role)
		}
		quoted[i] = "'" + role + "'"
	}
	return "ARRAY[" + strings.Join(quoted, ", ") + "]::name[]", nil
}

func suspendSQL(roles []string) (string, error) {
	list, err := roleList(roles)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`DO $gregale$
DECLARE r name;
BEGIN
  FOR r IN SELECT rolname FROM pg_roles WHERE rolname = ANY(%[1]s) LOOP
    EXECUTE format('ALTER ROLE %%I NOLOGIN', r);
  END LOOP;
END $gregale$;
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = ANY(%[1]s);
`, list), nil
}

func resumeSQL(roles []string) (string, error) {
	list, err := roleList(roles)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`DO $gregale$
DECLARE r name;
BEGIN
  FOR r IN SELECT rolname FROM pg_roles WHERE rolname = ANY(%[1]s) LOOP
    EXECUTE format('ALTER ROLE %%I LOGIN', r);
  END LOOP;
END $gregale$;
`, list), nil
}

func idleSQL(roles []string) (string, error) {
	list, err := roleList(roles)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`DO $gregale$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_stat_activity WHERE usename = ANY(%[1]s))
     OR EXISTS (SELECT 1 FROM pg_prepared_xacts WHERE owner = ANY(%[1]s)) THEN
    RAISE EXCEPTION 'gregale: suspended roles still have sessions' USING ERRCODE = '%[2]s';
  END IF;
END $gregale$;
`, list, sqlstateSessionsRemain), nil
}
