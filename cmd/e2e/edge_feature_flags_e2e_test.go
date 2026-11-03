//go:build !no_pg

package e2e_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/platformflags"
)

// releaseEdgeFeatures turns the edge feature platform flags
// (ADR-20261023113721) on for every account the way an operator does, and
// waits until apid and gatewayd-internal have applied them: challenge rules,
// WAF warn and block, and penalties are off until launch.
func releaseEdgeFeatures(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	releasePlatformFlags(t, pool, platformflags.EdgeFeatures, "edge feature e2e")
}
