package state

import "testing"

func TestGitHubActionsRepositoryFromSubject(t *testing.T) {
	tests := []struct {
		name    string
		issuer  string
		subject string
		want    string
		wantOK  bool
	}{
		{
			name:    "legacy subject",
			issuer:  githubActionsOIDCIssuer,
			subject: "repo:octocat/hello:environment:production",
			want:    "octocat/hello",
			wantOK:  true,
		},
		{
			name:    "immutable subject",
			issuer:  githubActionsOIDCIssuer,
			subject: "repo:octocat@123456/hello@789012:environment:production",
			want:    "octocat/hello",
			wantOK:  true,
		},
		{
			name:    "immutable subject requires ids on both components",
			issuer:  githubActionsOIDCIssuer,
			subject: "repo:octocat@123456/hello:environment:production",
		},
		{
			name:    "malformed owner id",
			issuer:  githubActionsOIDCIssuer,
			subject: "repo:octocat@owner/hello@789012:environment:production",
		},
		{
			name:    "malformed repo id",
			issuer:  githubActionsOIDCIssuer,
			subject: "repo:octocat@123456/hello@repo:environment:production",
		},
		{
			name:    "wrong issuer",
			issuer:  "https://issuer.example.com",
			subject: "repo:octocat@123456/hello@789012:environment:production",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := githubActionsRepositoryFromSubject(tt.issuer, tt.subject)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("githubActionsRepositoryFromSubject() = (%q, %t), want (%q, %t)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestGitHubActionsRepositoryIdentityFromSubject(t *testing.T) {
	identity, ok := githubActionsRepositoryIdentityFromSubject(
		githubActionsOIDCIssuer,
		"repo:octocat@123456/hello@789012:environment:production",
	)
	if !ok {
		t.Fatal("immutable subject did not parse")
	}
	if identity.FullName != "octocat/hello" || identity.OwnerID != 123456 || identity.RepoID != 789012 {
		t.Fatalf("identity = %+v, want octocat/hello with IDs 123456/789012", identity)
	}
	for _, subject := range []string{
		"repo:octocat@0/hello@789012:ref:refs/heads/main",
		"repo:octocat@9223372036854775808/hello@789012:ref:refs/heads/main",
	} {
		if _, ok := githubActionsRepositoryIdentityFromSubject(githubActionsOIDCIssuer, subject); ok {
			t.Errorf("accepted invalid immutable IDs in %q", subject)
		}
	}
}
