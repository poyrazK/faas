// adr: 20261112223000

package main

// deployRequestCLISurfaces records how each JSON field of the customer deploy
// request (CreateDeploymentRequest, and its container overrides) is reached
// from the CLI or gregale.yaml, like updateAppRequestCLISurfaces does for app
// updates (ADR-20261112223000 §3). Coding agents only use the CLI, so a field
// with no path is a setting they cannot reach; KnownGap names it, and
// docs/cli-api-parity-exemptions.txt freezes the list so it can only shrink.
var deployRequestCLISurfaces = map[string]updateAppCLISurface{
	"expected_review":             {KnownGap: "set by the dashboard's reviewed deploy; no CLI setter"},
	"native_rollout":              {KnownGap: "no customer CLI setter; API-only today"},
	"image":                       {Path: "gregale deploy --image", AppFlags: []string{"image"}},
	"mcp_task_migration_override": {Path: "gregale deploy --mcp-task-migration-override", AppFlags: []string{"mcp-task-migration-override"}},
	"override_freeze":             {Path: "gregale deploy --override-freeze", AppFlags: []string{"override-freeze"}},
	"release_at":                  {Path: "gregale deploy --at", AppFlags: []string{"at"}},
	"release_notify":              {Path: "gregale deploy --release-notify", AppFlags: []string{"release-notify"}},
	"overrides":                   {Path: "gregale deploy flags and gregale.yaml (see deployOverridesCLISurfaces)"},
	"require_signed":              {Path: "gregale app <slug> security --require-signed (the app setting the deploy inherits)"},
	"companions":                  {Path: "gregale.yaml extension sidecars (loaded by gregale deploy)"},
	"sidecars":                    {KnownGap: "legacy sidecar list; gregale deploy sends companions instead"},
	"workflows":                   {Path: "gregale.yaml workflows (loaded by gregale deploy)"},
	"traffic_percent":             {Path: "gregale deploy --traffic-percent / --no-traffic", AppFlags: []string{"traffic-percent", "no-traffic"}},
	"scope":                       {Path: "gregale deploy --env (environment scope)"},
	"environment":                 {Path: "gregale deploy --environment", AppFlags: []string{"environment"}},
	"reason":                      {Path: "gregale deploy --reason", AppFlags: []string{"reason"}},
	"tag":                         {Path: "gregale deploy --tag", AppFlags: []string{"tag"}},
	"deployed_by":                 {Path: "gregale deploy --deployed-by", AppFlags: []string{"deployed-by"}},
	"pr_number":                   {Path: "gregale deploy --pr-number", AppFlags: []string{"pr-number"}},
	"rollback_on_5xx":             {Path: "gregale deploy --rollback-on-5xx", AppFlags: []string{"rollback-on-5xx"}},
	"disable_startup_cpu_boost":   {Path: "gregale deploy --disable-startup-cpu-boost", AppFlags: []string{"disable-startup-cpu-boost"}},
	"canary":                      {Path: "gregale deploy --canary-preset / --canary-stages", AppFlags: []string{"canary-preset", "canary-stages"}},
	"full_rootfs_allow_auto":      {KnownGap: "no customer CLI setter; API-only today"},
	"full_rootfs_override":        {KnownGap: "no customer CLI setter; API-only today"},
}

// deployOverridesCLISurfaces covers CreateDeploymentRequest.overrides.
var deployOverridesCLISurfaces = map[string]updateAppCLISurface{
	"entrypoint":      {Path: "gregale.yaml start / the image ENTRYPOINT"},
	"cmd":             {Path: "gregale.yaml start / the image CMD"},
	"env":             {Path: "gregale env set (app env, applied at deploy)"},
	"env_secrets":     {KnownGap: "no customer CLI setter; gregale env set seals values on the app instead"},
	"port":            {Path: "gregale.yaml port"},
	"main_depends_on": {Path: "gregale.yaml main_depends_on"},
	"healthcheck":     {Path: "gregale deploy --healthcheck-path / --healthcheck-grpc", AppFlags: []string{"healthcheck-path", "healthcheck-grpc"}},
	"readiness_probe": {Path: "gregale deploy --readiness-path / gregale.yaml hosting.readiness_probe", AppFlags: []string{"readiness-path"}},
	"liveness_probe":  {KnownGap: "API-only on purpose (#7221): strict liveness checks cause restart loops"},
	"scope":           {Path: "gregale deploy --env"},
}
