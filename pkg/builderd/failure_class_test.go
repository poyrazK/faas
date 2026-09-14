package builderd

// Tests for build-failure classification (issue #2577).
//
// Untagged on purpose, matching failure_class.go: the logic has no metal
// dependency, and leaving it behind the metal tag is why a build that never
// ran could be blamed on the customer for as long as it was.

import "testing"

// A build that never produced an exit status is an infrastructure failure, not
// the customer's fault. Classifying it as user_error sent operators reading
// build logs for a platform bug — which is what #2577 did, stamping
// failure_class=user_error on "vm exit -1" after the spawn failed on a host
// path builderd had resolved incorrectly.
func TestClassifyBuildFailure_NoExitStatusIsInfraNotUserError(t *testing.T) {
	tests := []struct {
		name     string
		exitCode int
		want     string
	}{
		{name: "vm never ran", exitCode: -1, want: "FailureInfra"},
		{name: "oom", exitCode: 137, want: "FailureOOM"},
		{name: "timeout", exitCode: 124, want: "FailureTimeout"},
		{name: "genuine build failure", exitCode: 1, want: "FailureUserError"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, _ := classifyBuildFailure(tc.exitCode, t.TempDir())
			if got != tc.want {
				t.Errorf("classifyBuildFailure(%d) = %q, want %q", tc.exitCode, got, tc.want)
			}
		})
	}
}
