package releaseinstall

import "testing"

func TestValidateDeploymentRecord(t *testing.T) {
	valid := DeploymentRecord{
		Daemon: "apid", Version: "sha256:abc", CommitSHA: "0123456789abcdef0123456789abcdef01234567",
		DeployedBy: "operator", DeployKind: DeploymentInstall,
	}
	if err := ValidateDeploymentRecord(valid); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}
	for name, mutate := range map[string]func(*DeploymentRecord){
		"missing daemon":  func(r *DeploymentRecord) { r.Daemon = " " },
		"missing version": func(r *DeploymentRecord) { r.Version = "" },
		"bad commit":      func(r *DeploymentRecord) { r.CommitSHA = "ABC" },
		"missing actor":   func(r *DeploymentRecord) { r.DeployedBy = "" },
		"bad kind":        func(r *DeploymentRecord) { r.DeployKind = "manual" },
		"bad sbom digest": func(r *DeploymentRecord) { r.SBOMSHA256 = "sha256:not-a-digest" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := valid
			mutate(&copy)
			if err := ValidateDeploymentRecord(copy); err == nil {
				t.Fatal("record unexpectedly accepted")
			}
		})
	}
}

func TestNormalizeDeploymentHistoryLimit(t *testing.T) {
	for input, want := range map[int]int{0: 50, -1: 50, 1: 1, 50: 50, 500: 500, 501: 500} {
		if got := NormalizeDeploymentHistoryLimit(input); got != want {
			t.Errorf("NormalizeDeploymentHistoryLimit(%d) = %d, want %d", input, got, want)
		}
	}
}
