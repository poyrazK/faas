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
// CLI's choices while its region offered neither. Replica-only creation
// requires explicit support before reserving a database.
func addPostgresUnsupported(ctx context.Context, client addPostgresCapabilities, region, access, availability, serviceClass string, creating bool) error {
	replicaOnlyCreate := creating && access == "replica_only"
	if replicaOnlyCreate && serviceClass != "production" {
		return fmt.Errorf("--access replica_only requires --class production when creating a database")
	}
	region = strings.TrimSpace(region)
	if region == "" {
		return nil
	}
	capabilities, err := client.GetManagedPostgresCapabilities(ctx, region)
	if err != nil {
		if replicaOnlyCreate {
			return fmt.Errorf("could not verify --access replica_only in region %s: %w", region, err)
		}
		return nil //nolint:nilerr // Preserve the server-side validation fallback for existing modes.
	}
	if (len(capabilities.CredentialAccess) > 0 || replicaOnlyCreate) && !slices.Contains(capabilities.CredentialAccess, access) {
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
func printAddPostgresMigrationHint(w io.Writer, database, app, appType, scope, access string) {
	if access != "read_write" {
		return
	}
	// A migration credential reaches release tasks only, and a function app
	// cannot run one (no container image; the deploy refuses
	// release.command), so the app-route advice would lead to a dead end.
	if appType == "function" {
		_, _ = fmt.Fprintf(w, "Schema changes need a migration binding run from a release task, which function apps cannot run.\n"+
			"  Run migrations from an app (type: app) in the same project that declares release.command:\n"+
			"  gregale postgres attach %s <app> --scope %s --access migration --env MIGRATION_DATABASE_URL\n", database, scope)
		return
	}
	_, _ = fmt.Fprintf(w, "Schema changes need a migration binding run from a release task:\n  gregale postgres attach %s %s --scope %s --access migration --env MIGRATION_DATABASE_URL\n  then set release.command in gregale.yaml (for example: python migrate.py).\n", database, app, scope)
}
