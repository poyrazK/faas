// adr: 1664 — bounded Data API lock waits and compatible login defaults.
package neon

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func dataAPILoginSettings() []string {
	return []string{"statement_timeout=" + strconv.Itoa(api.DataAPIQueryTimeoutMS), "lock_timeout=" + strconv.Itoa(api.DataAPILockTimeoutMS)}
}

func configureDataAPILogin(ctx context.Context, tx pgx.Tx, role credentialRole) error {
	for _, setting := range dataAPILoginSettings() {
		if _, err := tx.Exec(ctx, "ALTER ROLE "+roleIdentifier(role.name)+" SET "+setting); err != nil {
			return err
		}
	}
	return nil
}

// Only the exact previous policy is eligible for an in-place upgrade. Reuse
// the current identity/membership checks without accepting arbitrary settings.
// The containing transaction rolls back the upgrade if later ACL checks fail.
func (i sqlRoleInfo) matchesLegacyDataAPI(role credentialRole) bool {
	if role.access != managedpostgres.CredentialDataAPI || len(i.configuration) != 1 || i.configuration[0] != "statement_timeout="+strconv.Itoa(api.DataAPIQueryTimeoutMS) {
		return false
	}
	i.configuration = dataAPILoginSettings()
	return i.matches(role)
}
