package firecracker

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// bindingTimeoutSQL records the revision in the role's database-specific
// settings so a delayed guest request cannot overwrite a newer policy.
func bindingTimeoutSQL(database, role string, policy managedpostgres.BindingTimeoutPolicy) (string, error) {
	if !identifierPattern.MatchString(database) || !rolePattern.MatchString(role) ||
		(!strings.HasPrefix(role, "gregale_rt_") && !strings.HasPrefix(role, "gregale_ro_")) ||
		policy.Revision < 1 || policy.Validate() != nil {
		return "", managedpostgres.ErrInvalid
	}
	return fmt.Sprintf(`BEGIN;
SET LOCAL synchronous_commit = 'remote_apply';
SELECT pg_advisory_xact_lock(hashtextextended('gregale-binding-timeouts:%[2]s', 0));
DO $gregale$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_db_role_setting s
    JOIN pg_roles r ON r.oid = s.setrole
    JOIN pg_database d ON d.oid = s.setdatabase
    CROSS JOIN LATERAL unnest(s.setconfig) AS config(value)
    WHERE r.rolname = '%[2]s' AND d.datname = '%[1]s'
      AND substring(config.value from '^gregale[.]binding_timeout_revision=([0-9]{1,19})$')::numeric > %[3]d
  ) THEN
    ALTER ROLE %[2]s IN DATABASE %[1]s SET statement_timeout TO '%[4]dms';
    ALTER ROLE %[2]s IN DATABASE %[1]s SET lock_timeout TO '%[5]dms';
    ALTER ROLE %[2]s IN DATABASE %[1]s SET idle_in_transaction_session_timeout TO '%[6]dms';
    ALTER ROLE %[2]s IN DATABASE %[1]s SET gregale.binding_timeout_revision TO '%[3]d';
  END IF;
END $gregale$;
COMMIT;
`, database, role, policy.Revision, policy.StatementMS, policy.LockMS, policy.IdleInTransactionMS), nil
}

func (p *Provider) ApplyBindingTimeouts(ctx context.Context, resourceID, role string, policy managedpostgres.BindingTimeoutPolicy) error {
	batch, err := bindingTimeoutSQL(p.cfg.Database, role, policy)
	if err != nil {
		return err
	}
	return p.admin(ctx, resourceID, batch)
}
