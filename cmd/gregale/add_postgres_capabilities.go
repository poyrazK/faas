package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// addPostgresCapabilities is the subset of the API client add postgres reads.
type addPostgresCapabilities interface {
	GetManagedPostgresCapabilities(context.Context, string) (api.ManagedPostgresCapabilities, error)
}

// addPostgresUnsupported checks the requested access mode (and, when creating,
// availability) against what the region actually offers, before a database
// is created. Production advertised data_api and high_availability in the
// CLI's choices while its region offered neither. An unreadable capability
// response never blocks: the server still validates, as before.
func addPostgresUnsupported(ctx context.Context, client addPostgresCapabilities, region, access, availability string, creating bool) error {
	region = strings.TrimSpace(region)
	if region == "" {
		return nil
	}
	capabilities, err := client.GetManagedPostgresCapabilities(ctx, region)
	if err != nil {
		return nil //nolint:nilerr // unreadable capabilities never block; the server still refuses unsupported modes
	}
	if len(capabilities.CredentialAccess) > 0 && !slices.Contains(capabilities.CredentialAccess, access) {
		return fmt.Errorf("--access %s is not available in region %s; available: %s", access, region, strings.Join(capabilities.CredentialAccess, ", "))
	}
	if creating && len(capabilities.Availability) > 0 && !slices.Contains(capabilities.Availability, availability) {
		return fmt.Errorf("--availability %s is not available in region %s; available: %s", availability, region, strings.Join(capabilities.Availability, ", "))
	}
	return nil
}

// printAddPostgresMigrationHint tells a developer who just bound a runtime
// credential how schema changes are applied: the runtime role has no DDL, and
// serving instances never receive migration credentials.
func printAddPostgresMigrationHint(w io.Writer, database, app, scope, access string) {
	if access != "read_write" {
		return
	}
	_, _ = fmt.Fprintf(w, "Schema changes need a migration binding run from a release task:\n  gregale postgres attach %s %s --scope %s --access migration --env MIGRATION_DATABASE_URL\n  then set release.command in gregale.yaml (for example: python migrate.py).\n", database, app, scope)
}
