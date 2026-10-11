//go:build !managed_postgres_native || !metal

package main

import (
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/neon"
)

func managedPostgresFactories() map[string]managedpostgres.Factory {
	return map[string]managedpostgres.Factory{"neon": neon.New}
}
