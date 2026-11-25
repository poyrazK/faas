package frameworkprofile

import "testing"

// Prod hunt r47: the deploy summary showed Go's local `go run .` as the
// deployed start command; the build runs the compiled ./out binary.
func TestDeployedStartCommand(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile Profile
		want    string
	}{
		{"go source", Profile{StartCommand: "go run ."}, "./out"},
		{"go with a configured start", Profile{StartCommand: "go run .", StartFromConfig: true}, "go run ."},
		{"node", Profile{StartCommand: "npm run start"}, "npm run start"},
		{"none", Profile{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeployedStartCommand(tc.profile); got != tc.want {
				t.Fatalf("DeployedStartCommand = %q, want %q", got, tc.want)
			}
		})
	}
}
