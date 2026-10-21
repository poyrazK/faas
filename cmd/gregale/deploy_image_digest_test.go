package main_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestImageDigestRequiredProblem pins H10-118: a tag-only --image failed
// with the title "Image required" and no way to find the digest.
func TestImageDigestRequiredProblem(t *testing.T) {
	problem := main.ImageDigestRequiredProblem("mirror.gcr.io/traefik/whoami:v1.10.3")
	if problem.Code != api.CodeImageRequired || problem.Title != "Image digest required" {
		t.Fatalf("problem = %q %q", problem.Code, problem.Title)
	}
	for _, want := range []string{`"mirror.gcr.io/traefik/whoami:v1.10.3" is not digest-pinned`, "repository@sha256:<digest>", "crane digest mirror.gcr.io/traefik/whoami:v1.10.3"} {
		if !strings.Contains(problem.Detail, want) {
			t.Errorf("detail %q lacks %q", problem.Detail, want)
		}
	}
}
