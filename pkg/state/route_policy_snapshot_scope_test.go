package state

import "testing"

// A deployment in a one- or two-character environment (an environment branch
// named "b1") persists its route policy snapshot when it goes live.
func TestDeploymentRoutePolicySnapshotAcceptsShortScopes(t *testing.T) {
	const dep, app = "8d15aa62-1650-4d68-8c57-a2e1d8a24af8", "34eb61c5-0c6f-4d21-9eff-c640baecfe73"
	for _, scope := range []string{"", "a", "b1", "pr-7", "production"} {
		snap, err := marshalDeploymentRoutePolicySnapshot(dep, app, scope, nil)
		if err != nil {
			t.Fatalf("scope %q: marshal: %v", scope, err)
		}
		if err := validateDeploymentRoutePolicySnapshot(snap); err != nil {
			t.Errorf("scope %q: %v", scope, err)
		}
	}
	for _, scope := range []string{"-b", "b-", "B1", "a_b"} {
		snap, err := marshalDeploymentRoutePolicySnapshot(dep, app, scope, nil)
		if err != nil {
			t.Fatalf("scope %q: marshal: %v", scope, err)
		}
		if err := validateDeploymentRoutePolicySnapshot(snap); err == nil {
			t.Errorf("scope %q accepted", scope)
		}
	}
}
