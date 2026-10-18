package api

import "testing"

func TestGitLabEnvironmentSourceRequiresVerifiedProviderContract(t *testing.T) {
	base := EnvironmentGitSourceSpec{Provider: "gitlab", RepositoryID: 41, Repository: "group/subgroup/service",
		Ref: "refs/heads/main", ManifestPath: "environments/production.yaml", Mode: "report", ApprovalPolicy: "manual"}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	base.ApprovalPolicy, base.Mode, base.Prune = "protected_branch", "enforce", true
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*EnvironmentGitSourceSpec)
	}{
		{"unverified repository", func(s *EnvironmentGitSourceSpec) { s.RepositoryID = 0 }},
		{"GitHub installation", func(s *EnvironmentGitSourceSpec) { s.InstallationID = 7 }},
		{"unsafe path", func(s *EnvironmentGitSourceSpec) { s.Repository = "group/../service" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := base
			tc.change(&spec)
			if err := spec.Validate(); err == nil {
				t.Fatal("unsafe GitLab source accepted")
			}
		})
	}
}
