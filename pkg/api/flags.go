// pkg/api/flags.go — env-driven feature flags that gate customer
// surface wiring. The TenantSurfaces flag is the dark-launch switch
// for issue #879 / ADR-100: the schema and the apid routes are
// wired in PR-C, but the routes 404 (or 503) until this flag is
// set, so a misconfigured rollout can be reverted by simply
// unsetting the env var (no migration to undo, no DNS to withdraw).
//
// Pattern mirrors cmd/apid/server.go:189-203 for FAAS_REKEY_ENABLED
// — direct os.Getenv with a stable "1" / "true" / "yes" accept
// set, and a default-off shape. No global mutable state outside
// the accessor function so tests can override with t.Setenv.
package api

import (
	"os"
	"strings"
)

// TenantSurfacesEnabled reports whether the customer surface
// HTTP API is live. Reads FAAS_TENANT_SURFACES_ENABLED at every
// call (not cached at boot) so an operator can flip the env var
// and SIGHUP-restart-free roll out / roll back the surface routes
// without bouncing every daemon. Default off; the cert engine +
// state surface are in place but the HTTP routes + CLI are
// gated until PR-C ships.
func TenantSurfacesEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("FAAS_TENANT_SURFACES_ENABLED")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// JobsEnabled reports whether the ADR-099 jobs cluster is live.
// Reads FAAS_JOBS_ENABLED at every call (not cached at boot) so an
// operator can flip the env var on schedd + apid independently and
// roll out / roll back without a daemon bounce. Default off; the
// PR-A schema, PR-B JobStore, and PR-C schedd dispatch are in place
// but the /v1/jobs* routes 404 with CodeJobsNotAllowed until this
// flag is set, so a misconfigured rollout can be reverted by simply
// unsetting the env var (no migration to undo, no jobs to migrate).
//
// Companion env var on schedd: FAAS_JOBS_DISABLED=1 forces the
// dispatch tick OFF even when this flag is on — used as a kill
// switch during the dark-launch ramp without needing to flip the
// apid surface flag too.
func JobsEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("FAAS_JOBS_ENABLED")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// JobsDisabled reports whether the operator has force-killed the
// jobs dispatch path via FAAS_JOBS_DISABLED=1. Inverse of
// JobsEnabled for the schedd side: schedd keeps reading
// JobsEnabled() for the apid route gate but consults JobsDisabled
// as a separate kill switch on the dispatch tick so an emergency
// rollback can stop in-flight wakes without bouncing apid. Default
// off (no kill switch active). Reads the env var on every call so
// the operator can flip mid-rollout.
func JobsDisabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("FAAS_JOBS_DISABLED")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
