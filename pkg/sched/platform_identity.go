package sched

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// appendPlatformIdentity appends the immutable deployment and placement
// identity to the existing API-env channel. The vmmd wire stays additive-free:
// platform entries are appended after customer entries, and guest-init gives
// the reserved FAAS_* keys platform precedence over manifest env and secrets.
// Optional provenance fields are still emitted as empty values: a customer
// must not be able to spoof an unavailable commit/tag/region by setting the
// reserved key themselves.
func appendPlatformIdentity(env []fcvm.APIEnvEntry, app state.App, dep state.Deployment, acct state.Account, nodeID, instanceID, region string) []fcvm.APIEnvEntry {
	entries := make([]fcvm.APIEnvEntry, 0, len(env)+9)
	entries = append(entries, env...)
	add := func(key, value string) {
		entries = append(entries, fcvm.APIEnvEntry{Key: key, Value: value})
	}
	add(api.PlatformAppIDEnv, app.ID)
	add(api.PlatformDeploymentIDEnv, dep.ID)
	add(api.PlatformTenantIDEnv, acct.ID)
	add(api.PlatformInstanceIDEnv, instanceID)
	add(api.PlatformNodeIDEnv, nodeID)
	add(api.PlatformRegionEnv, region)
	add(api.PlatformCommitSHAEnv, dep.CommitSHA)
	add(api.PlatformDeploymentTagEnv, dep.Tag)
	createdAt := ""
	if !dep.CreatedAt.IsZero() {
		createdAt = dep.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	add(api.PlatformDeploymentCreatedEnv, createdAt)
	return entries
}
