//go:build managed_postgres_native && metal

package main

import (
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/neon"
)

// Only the isolated acceptance binary can speak to the disposable SQL fixture.
// Production apid binaries contain no sqlfixture driver or configuration path.
func managedPostgresFactories() map[string]managedpostgres.Factory {
	return map[string]managedpostgres.Factory{"neon": neon.New, "sqlfixture": postgresfixture.NewRemote}
}
