package gregalemanifest

import (
	"strings"
	"testing"
)

func TestBuildSection(t *testing.T) {
	m, err := ParseBytes([]byte("build:\n  dockerfile: ./docker/Dockerfile.prod\n  target: runtime\n  args: {NODE_ENV: production}\n  secrets:\n    - {id: npmrc, secret: NPM_TOKEN}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := m.Build.DockerfilePath(); got != "docker/Dockerfile.prod" {
		t.Fatalf("DockerfilePath = %q", got)
	}
	opts := m.Build.Options()
	if opts.Target != "runtime" || opts.Args["NODE_ENV"] != "production" || opts.Secrets[0].ID != "npmrc" {
		t.Fatalf("Options = %+v", opts)
	}
	if (&BuildConfig{}).DockerfilePath() != "Dockerfile" || (&BuildConfig{}).Options() != nil {
		t.Fatal("an empty build: section must select the root Dockerfile and no options")
	}
}

func TestBuildSectionValidation(t *testing.T) {
	for _, tc := range []struct{ yaml, want string }{
		{"build:\n  dockerfile: /etc/Dockerfile\n", "relative path"},
		{"build:\n  dockerfile: ../Dockerfile\n", "relative path"},
		{"build:\n  target: \"9bad\"\n", "not a valid stage name"},
		{"build:\n  args: {\"bad-name\": x}\n", "build.args name"},
		{"build:\n  secrets:\n    - {id: a, secret: bad-key}\n", "app secret"},
		{"build: {}\nfunction:\n  runtime: node22\n  handler: handler.handler\n", "applies to app deploys"},
	} {
		m, err := ParseBytes([]byte(tc.yaml))
		if err == nil {
			err = m.Validate()
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v, want %q", tc.yaml, err, tc.want)
		}
	}
}
