package main

import "testing"

func TestGithubSetupBranchAcceptsHyphensAndRejectsControls(t *testing.T) {
	for _, branch := range []string{"release/v1-2", "feature/my-api", "main"} {
		if !validGithubSetupBranch(branch) {
			t.Errorf("rejected valid branch %q", branch)
		}
	}
	for c := rune(0); c <= 0x20; c++ {
		branch := "release" + string(c) + "candidate"
		if validGithubSetupBranch(branch) {
			t.Errorf("accepted branch with control/space %q", branch)
		}
	}
	branches, err := parseGithubSetupDeployBranches("release/v1-2=production,feature/my-api=staging")
	if err != nil || branches["release/v1-2"] != "production" || branches["feature/my-api"] != "staging" {
		t.Fatalf("deploy branch mappings=%v, err=%v", branches, err)
	}
}
