//go:build !managed_postgres_native || !metal

package apidserver

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/firecracker"
	"github.com/onebox-faas/faas/pkg/managedpostgres/neon"
)

// managedPostgresFactories lists the backend drivers. The native
// firecracker backend (ADR-1027) needs apid's pool for role verifiers.
func managedPostgresFactories(pool *pgxpool.Pool) map[string]managedpostgres.Factory {
	return map[string]managedpostgres.Factory{"neon": neon.New, firecracker.Driver: firecracker.Factory(pool)}
}
