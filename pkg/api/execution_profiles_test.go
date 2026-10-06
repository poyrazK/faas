package api

import "testing"

func TestExecutionProfilesAdmissionAndSnapshotShape(t *testing.T) {
	for _, tc := range []struct {
		profile ExecutionProfile
		runtime ExecutionRuntime
		valid   bool
	}{
		{"", ExecutionRuntimeNode24, true}, {ExecutionProfileStandard, ExecutionRuntimePython313, true},
		{ExecutionProfilePythonDataV1, ExecutionRuntimePython313, true}, {ExecutionProfilePythonDataV1, ExecutionRuntimeNode24, false},
		{ExecutionProfilePythonDataV1, ExecutionRuntimePython312, false}, {"custom-packages", ExecutionRuntimePython313, false},
	} {
		request := CreateExecutionRequest{Runtime: tc.runtime, Profile: tc.profile, Source: "def main(input, context): return input"}
		resolved, problem := request.Resolve(PlanPro)
		if (problem == nil) != tc.valid {
			t.Fatalf("profile=%s/runtime=%s: %v", tc.profile, tc.runtime, problem)
		}
		if !tc.valid {
			continue
		}
		if resolved.Profile != tc.profile.Normalized() {
			t.Fatal("profile not resolved")
		}
		if tc.profile.Normalized() == ExecutionProfilePythonDataV1 && resolved.SnapshotShape().Profile != ExecutionProfilePythonDataV1 {
			t.Fatal("profile omitted from snapshot shape")
		}
	}
}
