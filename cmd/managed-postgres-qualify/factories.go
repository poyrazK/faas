package main

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/firecracker"
	"github.com/onebox-faas/faas/pkg/managedpostgres/neon"
)

// nativeDatabaseURLEnv is the control-plane database whose role verifiers
// pgproxyd reads. Qualifying the native backend (ADR-1027) publishes the
// probes' credentials there, so live runs against it need it.
const nativeDatabaseURLEnv = "FAAS_MANAGED_POSTGRES_QUALIFY_NATIVE_DATABASE_URL"

// describeFactories build every driver from its configuration alone, for
// the modes that read only a backend's capabilities and fingerprint
// (--check-config, --verify). The native one reaches nothing.
func describeFactories() map[string]managedpostgres.Factory {
	return map[string]managedpostgres.Factory{"neon": neon.New, firecracker.Driver: firecracker.DescribeFactory()}
}

// liveFactories build drivers that reach their backends. Without
// nativeDatabaseURLEnv the native driver is only described, so a Neon
// backend can still be qualified from a configuration that lists both;
// qualifying the native backend then fails up front (see liveNativeReady).
// The returned func closes what was opened.
func liveFactories(ctx context.Context, getenv func(string) string) (map[string]managedpostgres.Factory, func(), error) {
	factories := describeFactories()
	pool, closePool, err := nativeVerifierPool(ctx, getenv)
	if err != nil || pool == nil {
		return factories, closePool, err
	}
	factories[firecracker.Driver] = firecracker.Factory(pool)
	return factories, closePool, nil
}

// liveNativeReady reports whether a live run can qualify backend: a native
// backend needs the verifier database.
func liveNativeReady(getenv func(string) string, backend managedpostgres.Backend) bool {
	return backend.Driver != firecracker.Driver || strings.TrimSpace(getenv(nativeDatabaseURLEnv)) != ""
}

// nativeVerifierPool opens the native driver's verifier database when one
// is configured; nil (and a no-op close) when not.
func nativeVerifierPool(ctx context.Context, getenv func(string) string) (*pgxpool.Pool, func(), error) {
	dsn := strings.TrimSpace(getenv(nativeDatabaseURLEnv))
	if dsn == "" {
		return nil, func() {}, nil
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, nil, err
	}
	return pool, pool.Close, nil
}
