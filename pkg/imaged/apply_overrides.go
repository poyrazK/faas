// Package imaged applies deploy-time overrides (issue #460 / ADR-053) onto
// the OCI-derived AppManifest. PR-A persisted the six override columns on
// `deployments`; this file is the seam where imaged layers them onto the
// manifest that gets stamped into /etc/faas/app.json. PR-C is the follow-up
// that propagates port/healthcheck through the host-side wake path; for now
// applyOverrides writes them into the manifest anyway so the source of truth
// is consistent across all six fields.
//
// The helper is a pure function (no I/O, no DB, no app/dep lookups) so it
// pins via table-driven unit tests in apply_overrides_test.go without a test
// harness. The caller (handleDeployment → buildImageLayer / buildFunctionLayer)
// is responsible for transitioning the deployment row to FAILED via
// markDeployFailed on a non-nil error here, mirroring the dep.Handler path
// that called manifest.Validate() directly.
package imaged

import (
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// applyOverrides layers dep's six issue #460 / ADR-053 override columns onto
// the OCI-derived manifest in place, returning the mutated copy. Errors come
// only from json.Unmarshal on the jsonb columns — the row was already
// validated at apid-create time (CreateDeploymentOverrides.Validate at
// pkg/api/dto.go), so a json.Unmarshal failure here means a column was
// tampered with at the DB layer (or the migration is replaying old shapes).
// imaged treats that as a deploy failure.
//
// Precedence (highest wins, mirroring ADR-053 §Decision 1):
//
//   - entrypoint: OverrideEntrypoint replaces OCI cmd-derived entrypoint; if
//     only OverrideCmd is set, it is appended to the existing OCI entrypoint
//     (argv = entrypoint + cmd, mirrors OCI runtime contract).
//   - env:        OverrideEnv wins on key collision with OCI env; non-colliding
//     OCI keys pass through.
//   - env_secrets: ref strings carry through onto the manifest's EnvSecrets
//     side-channel; sealed VALUES never appear here.
//   - port:       OverridePort replaces DefaultAppPort (PR-C makes it runtime;
//     dormant until host plumbing lands).
//   - healthcheck: OverrideHealthcheck.Path replaces the OCI/missing Healthz
//     (same — dormant until PR-C).
func applyOverrides(manifest api.AppManifest, dep state.Deployment) (api.AppManifest, error) {
	// --- entrypoint + cmd (combined: argv = entrypoint + cmd) ---
	//
	// Three cases:
	//   1. both nil: no change (preserve OCI argv)
	//   2. only entrypoint set: entrypoint replaces; cmd is appended if also set
	//   3. only cmd set: cmd appends to existing OCI argv
	//   4. both set: entrypoint replaces; cmd appends (mirrors case 2)
	if len(dep.OverrideEntrypoint) > 0 || len(dep.OverrideCmd) > 0 {
		argv := append([]string(nil), dep.OverrideEntrypoint...)
		if len(argv) == 0 {
			argv = append(argv, manifest.Entrypoint...)
		}
		manifest.Entrypoint = append(argv, dep.OverrideCmd...)
	}

	// --- env (merge, override wins on key collision) ---
	if len(dep.OverrideEnv) > 0 {
		var overrideEnv map[string]string
		if err := json.Unmarshal(dep.OverrideEnv, &overrideEnv); err != nil {
			return api.AppManifest{}, fmt.Errorf("imaged: decode override_env: %w", err)
		}
		// Defensive copy: a caller that retries the helper on the same base
		// (imaged transient error, snapshot re-pull) must not see the merged
		// state from the first run. Mirrors manifestFromImageConfig's
		// cloneEnvMap helper — pre-PR-B code already deep-copied OCI env for
		// this reason. PR-B extends that invariant to the override merge.
		merged := make(map[string]string, len(manifest.Env)+len(overrideEnv))
		for k, v := range manifest.Env {
			merged[k] = v
		}
		for k, v := range overrideEnv {
			merged[k] = v // override wins (last write wins on collision)
		}
		manifest.Env = merged
	}

	// --- env_secrets (refs carry through; pkg/sched resolves at wake) ---
	if len(dep.OverrideEnvSecrets) > 0 {
		var overrideEnvSecrets map[string]string
		if err := json.Unmarshal(dep.OverrideEnvSecrets, &overrideEnvSecrets); err != nil {
			return api.AppManifest{}, fmt.Errorf("imaged: decode override_env_secrets: %w", err)
		}
		merged := make(map[string]string, len(manifest.EnvSecrets)+len(overrideEnvSecrets))
		for k, v := range manifest.EnvSecrets {
			merged[k] = v
		}
		for k, v := range overrideEnvSecrets {
			merged[k] = v
		}
		manifest.EnvSecrets = merged
	}

	// --- port (dormant until PR-C; written to manifest for source-of-truth) ---
	if dep.OverridePort != 0 {
		manifest.Port = dep.OverridePort
	}

	// --- healthcheck (dormant until PR-C; only Path is surfaced today) ---
	if len(dep.OverrideHealthcheck) > 0 {
		var hc api.DeploymentHealthcheck
		if err := json.Unmarshal(dep.OverrideHealthcheck, &hc); err != nil {
			return api.AppManifest{}, fmt.Errorf("imaged: decode override_healthcheck: %w", err)
		}
		if hc.Path != "" {
			manifest.Healthz = hc.Path
		}
		// M-1 (ADR-136) surfaces Test + StartPeriodS onto AppManifest.Healthcheck
		// when the override declares them, so the OCI HEALTHCHECK shape flows
		// through to the per-VM manifest alongside the Path projection above.
		// Runtime polling lands in M-2 (ADR-X5); the field stays dormant on
		// guest-init until then, but the wire shape is canonical from
		// commit 6 onward.
		if len(hc.Test) > 0 || hc.StartPeriodS > 0 {
			mh := manifest.Healthcheck
			if mh == nil {
				mh = &api.AppManifestHealthcheck{}
			}
			if len(hc.Test) > 0 {
				mh.Test = append([]string(nil), hc.Test...)
			}
			if hc.StartPeriodS > 0 {
				mh.StartPeriodS = hc.StartPeriodS
			}
			manifest.Healthcheck = mh
		}
		// IntervalS/TimeoutS/Retries belong in pkg/fcvm/vmm.go::waitReady
		// after PR-C; today's waitReady is a bare TCP accept and has no
		// field for them. The manifest schema does not carry these yet
		// (would require a v2 contract). PR-B intentionally drops them.
	}

	return manifest, nil
}

// applyAppLifecycle overlays customer-owned lifecycle settings after image and
// deployment overrides. The app row is the source of truth for these fields;
// image config must not be able to change whether a workload is request,
// service, worker, or job mode.
func applyAppLifecycle(manifest api.AppManifest, app state.App) api.AppManifest {
	manifest.ExecutionMode = app.Manifest.ExecutionMode
	manifest.RestartPolicy = app.Manifest.RestartPolicy
	manifest.StartupDeadlineS = app.Manifest.StartupDeadlineS
	manifest.MaxRetries = app.Manifest.MaxRetries
	if app.Manifest.ServiceReplicas == nil {
		manifest.ServiceReplicas = nil
	} else {
		manifest.ServiceReplicas = &api.ServiceReplicas{
			Min:     app.Manifest.ServiceReplicas.Min,
			Max:     app.Manifest.ServiceReplicas.Max,
			Desired: app.Manifest.ServiceReplicas.Desired,
		}
	}
	return manifest
}
