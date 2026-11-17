package main

import (
	"fmt"
	"strings"
)

// projectDeployFlagAlternatives names the supported path for a single-app
// deploy flag that a project deploy refuses. The refusal used to say only
// "project deploy policy is not yet supported", which read as "impossible"
// for settings the project path does support another way (retail-store
// GREG-02). Flags without an entry have no project equivalent today.
var projectDeployFlagAlternatives = map[string]string{
	"startup-deadline-s": "set it in gregale.yaml next to compose.yaml:\n      lifecycle:\n        startup_deadline_s: 120",
	"max-retries":        "set lifecycle.max_retries in gregale.yaml",
	"restart-policy":     "set lifecycle.restart_policy in gregale.yaml",
	"execution-mode":     "set lifecycle.execution_mode in gregale.yaml",
	"no-require-authn":   "after the deploy, open one app with: gregale app update <app> --no-require-authn (anyone on the internet can then call it)",
	"require-authn":      "public URLs require a Gregale API key by default; to change one app later: gregale app update <app> --require-authn",
	"size":               "set deploy.resources.limits.memory per service in compose.yaml, or after the deploy: gregale app <app> --size SIZE",
	"vcpu":               "set deploy.resources.limits.cpus per service in compose.yaml",
	"profile":            "set deploy.resources.limits.memory per service in compose.yaml, or after the deploy: gregale app <app> --size SIZE",
	"dockerfile":         "set build.dockerfile per service in compose.yaml",
	"app":                "select services with --only <service>[,<service>...]",
	"function":           "select services with --only <service>[,<service>...]",
}

// projectDeployFlagRefusal renders the refused flags, one per line, each
// with its supported alternative when there is one.
func projectDeployFlagRefusal(flags []string) error {
	var b strings.Builder
	if len(flags) == 1 {
		fmt.Fprintf(&b, "%s applies to a single app and cannot be combined with --project, --only or --project-slug", flags[0])
	} else {
		fmt.Fprintf(&b, "%s apply to a single app and cannot be combined with --project, --only or --project-slug", strings.Join(flags, ", "))
	}
	for _, flag := range flags {
		if alternative, ok := projectDeployFlagAlternatives[strings.TrimPrefix(flag, "--")]; ok {
			fmt.Fprintf(&b, "\n  %s: %s", flag, alternative)
		}
	}
	return fmt.Errorf("%s", b.String())
}
