package firecracker

import (
	"context"
	"fmt"
	"strings"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Ending a cutover source's sessions (ADR-1370 §4; the native
// SessionTerminator is slice 14). The activation first revokes every
// binding identity on the source through RevokeCredentials, which sets
// each role NOLOGIN, so no new session can open: pgproxyd logs in as the
// role after authenticating its client, pooled or not. What is left is any
// session that was already open, which terminateRoleSessions ends, and the
// zero observation the activation waits for before it records the source
// as suspended.
//
// Admin batches return no rows, only an error code and SQLSTATE, so the
// observation reports presence: a check raises its own SQLSTATE while
// something remains.

const (
	// sqlstateSessionsRemain: a revoked role still has a session.
	sqlstateSessionsRemain = "GRS01"
	// sqlstatePreparedRemain: a revoked role still owns a prepared
	// transaction, which terminating backends does not end.
	sqlstatePreparedRemain = "GRP01"
)

// terminateRoleSessions terminates every backend of the roles. Safe to
// repeat; a role without sessions, or that does not exist, is skipped.
func (p *Provider) terminateRoleSessions(ctx context.Context, resourceID string, roles []string) error {
	list, err := roleList(roles)
	if err != nil {
		return err
	}
	return p.admin(ctx, resourceID, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = ANY("+list+");\n")
}

// roleSessionsRemain reports whether the roles still have sessions and
// whether they still own prepared transactions.
func (p *Provider) roleSessionsRemain(ctx context.Context, resourceID string, roles []string) (sessions, prepared bool, err error) {
	list, err := roleList(roles)
	if err != nil {
		return false, false, err
	}
	if sessions, err = p.assertNone(ctx, resourceID, "SELECT 1 FROM pg_stat_activity WHERE usename = ANY("+list+")", sqlstateSessionsRemain); err != nil {
		return false, false, err
	}
	if prepared, err = p.assertNone(ctx, resourceID, "SELECT 1 FROM pg_prepared_xacts WHERE owner = ANY("+list+")", sqlstatePreparedRemain); err != nil {
		return false, false, err
	}
	return sessions, prepared, nil
}

// assertNone runs a batch that raises state when query returns a row; it
// reports whether it did.
func (p *Provider) assertNone(ctx context.Context, resourceID, query, state string) (bool, error) {
	batch := fmt.Sprintf(`DO $gregale$
BEGIN
  IF EXISTS (%s) THEN
    RAISE EXCEPTION 'gregale: revoked roles still hold this' USING ERRCODE = '%s';
  END IF;
END $gregale$;
`, query, state)
	resp, err := p.scheduler.DatabaseAdmin(ctx, &scheddpb.DatabaseAdminRequest{DatabaseId: resourceID, Sql: batch})
	if err != nil {
		return false, providerError(err)
	}
	switch {
	case resp.GetErrorCode() == "":
		return false, nil
	case resp.GetSqlstate() == state:
		return true, nil
	default:
		return false, fmt.Errorf("%w: session check failed (sqlstate %s)", managedpostgres.ErrUnavailable, resp.GetSqlstate())
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
