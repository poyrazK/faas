// adr: 20261104120000

package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// A deploy stages its manifest's HTTP crons into its own environment, unless
// it is dark or runs with --no-triggers; production names stage into the
// production scope.
func TestDeployStagesManifestCronsIntoItsEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name        string
		noTriggers  bool
		traffic     int
		environment string
		stages      bool
		scope       string
	}{
		{name: "default scope", traffic: 100, stages: true},
		{name: "named production", traffic: 100, environment: "production", stages: true},
		{name: "default by name", traffic: 100, environment: "default", stages: true},
		{name: "stage", traffic: 100, environment: "staging", stages: true, scope: "staging"},
		{name: "preview environment", traffic: 100, environment: "pr-42", stages: true, scope: "pr-42"},
		{name: "no triggers", noTriggers: true, traffic: 100, environment: "staging", scope: "staging"},
		{name: "dark deploy", traffic: 0},
		{name: "canary", traffic: 10, stages: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := main.DeployStagesManifestCrons(tc.noTriggers, tc.traffic); got != tc.stages {
				t.Fatalf("stages manifest crons = %v, want %v", got, tc.stages)
			}
			if got := main.CronStagingEnvironment(tc.environment); got != tc.scope {
				t.Fatalf("cron staging environment = %q, want %q", got, tc.scope)
			}
		})
	}
}
