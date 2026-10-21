package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"testing"
)

func TestGithubSetupBranchAcceptsHyphensAndRejectsControls(t *testing.T) {
	for _, branch := range []string{"release/v1-2", "feature/my-api", "main"} {
		if !main.ValidGithubSetupBranch(branch) {
			t.Errorf("rejected valid branch %q", branch)
		}
	}
	for c := rune(0); c <= 0x20; c++ {
		branch := "release" + string(c) + "candidate"
		if main.ValidGithubSetupBranch(branch) {
			t.Errorf("accepted branch with control/space %q", branch)
		}
	}
	branches, err := main.ParseGithubSetupDeployBranches("release/v1-2=production,feature/my-api=staging")
	if err != nil || branches["release/v1-2"] != "production" || branches["feature/my-api"] != "staging" {
		t.Fatalf("deploy branch mappings=%v, err=%v", branches, err)
	}
	if _, err := main.ParseGithubSetupDeployBranches("staging=default,qa=staging"); err != nil {
		t.Fatalf("default and registered environments should be accepted: %v", err)
	}
	if _, err := main.ParseGithubSetupDeployBranches("staging=environment-name-that-is-too-long-abc"); err == nil {
		t.Fatal("accepted a scope that cannot be a registered project environment slug")
	}
}
