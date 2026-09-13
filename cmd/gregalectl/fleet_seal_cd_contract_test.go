package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFleetSealCDContract(t *testing.T) {
	t.Parallel()
	repoRoot := filepath.Join("..", "..")
	tests := []struct {
		workflow string
		tokens   []string
	}{
		{
			workflow: ".github/workflows/cd-controlplane.yml",
			tokens: []string{
				"FLEET_AGE_KEY: ${{ secrets.FLEET_AGE_KEY }}",
				"FLEET_AGE_RECIPIENT: ${{ secrets.FLEET_AGE_RECIPIENT }}",
				"Stage the shared fleet seal identity",
				`printf '%s' "$FLEET_AGE_KEY" > "$stage/fleet.age"`,
				`printf '%s' "$FLEET_AGE_RECIPIENT" > "$stage/fleet.age.pub"`,
				"gregalectl fleet-seal migrate",
				"gregalectl fleet-seal verify",
			},
		},
		{
			workflow: ".github/workflows/cd-compute.yml",
			tokens: []string{
				"FLEET_AGE_KEY: ${{ secrets.FLEET_AGE_KEY }}",
				"FLEET_AGE_RECIPIENT: ${{ secrets.FLEET_AGE_RECIPIENT }}",
				`printf '%s' "$FLEET_AGE_KEY" > "$ARTIFACT_DIR/fleet.age"`,
				`printf '%s' "$FLEET_AGE_RECIPIENT" > "$ARTIFACT_DIR/fleet.age.pub"`,
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(filepath.Base(test.workflow), func(t *testing.T) {
			t.Parallel()
			body, err := os.ReadFile(filepath.Join(repoRoot, test.workflow))
			if err != nil {
				t.Fatal(err)
			}
			for _, token := range test.tokens {
				if !strings.Contains(string(body), token) {
					t.Errorf("%s is missing fleet-seal CD contract %q", test.workflow, token)
				}
			}
		})
	}
}
