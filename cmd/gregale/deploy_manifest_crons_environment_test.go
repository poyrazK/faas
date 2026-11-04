// adr: 20261104120000

package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Crons fire only into production, and the staging request names no
// environment, so only a production deploy may replace the app's manifest
// crons; a stage deploy must leave production's crons alone.
func TestDeployStagesManifestCronsOnlyForProduction(t *testing.T) {
	for _, tc := range []struct {
		name        string
		noTriggers  bool
		traffic     int
		environment string
		want        bool
	}{
		{name: "default scope", traffic: 100, want: true},
		{name: "named production", traffic: 100, environment: "production", want: true},
		{name: "stage", traffic: 100, environment: "staging"},
		{name: "preview environment", traffic: 100, environment: "pr-42"},
		{name: "no triggers", noTriggers: true, traffic: 100},
		{name: "dark deploy", traffic: 0},
		{name: "canary", traffic: 10, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := main.DeployStagesManifestCrons(tc.noTriggers, tc.traffic, tc.environment); got != tc.want {
				t.Fatalf("stages manifest crons = %v, want %v", got, tc.want)
			}
		})
	}
}
