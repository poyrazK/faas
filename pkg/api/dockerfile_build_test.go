package api

import (
	"fmt"
	"strings"
	"testing"
)

func TestDockerfileBuildSecretsValidation(t *testing.T) {
	valid := &DockerfileBuildOptions{Secrets: []DockerfileBuildSecret{{ID: "npm", Secret: "NPM_TOKEN"}, {ID: "gh.token", Secret: "GH_TOKEN"}}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid secrets rejected: %v", err)
	}
	many := make([]DockerfileBuildSecret, DockerfileBuildSecretsMax+1)
	for i := range many {
		many[i] = DockerfileBuildSecret{ID: fmt.Sprintf("s%d", i), Secret: "TOKEN"}
	}
	for _, tc := range []struct {
		name    string
		secrets []DockerfileBuildSecret
		want    string
	}{
		{"id with a comma", []DockerfileBuildSecret{{ID: "a,src=/etc/shadow", Secret: "TOKEN"}}, "build secret id"},
		{"empty id", []DockerfileBuildSecret{{ID: "", Secret: "TOKEN"}}, "build secret id"},
		{"duplicate id", []DockerfileBuildSecret{{ID: "npm", Secret: "A"}, {ID: "npm", Secret: "B"}}, "declared twice"},
		{"invalid app secret", []DockerfileBuildSecret{{ID: "npm", Secret: "npm-token"}}, `names app secret "npm-token"`},
		{"too many", many, fmt.Sprintf("the limit is %d", DockerfileBuildSecretsMax)},
	} {
		options := &DockerfileBuildOptions{Secrets: tc.secrets}
		if err := options.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestDockerfileBuildSecretsAreOptions(t *testing.T) {
	options := &DockerfileBuildOptions{Secrets: []DockerfileBuildSecret{{ID: "npm", Secret: "NPM_TOKEN"}}}
	if options.IsZero() || options.SHA256() == "" {
		t.Fatal("secrets alone must count as build options")
	}
	clone := options.Clone()
	clone.Secrets[0].Secret = "OTHER"
	if options.Secrets[0].Secret != "NPM_TOKEN" {
		t.Fatal("clone aliases the secret list")
	}
	if clone.SHA256() == options.SHA256() {
		t.Fatal("a different secret name must change the digest")
	}
}
