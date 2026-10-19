package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPublicURLsReachAllRailpackStepsAndReplaceOldSourceDefaults(t *testing.T) {
	data, err := os.ReadFile("../../pkg/devpatch/testdata/railpack-0.38.0-node-build.json")
	if err != nil {
		t.Fatal(err)
	}
	urls := map[string]string{"VITE_API_URL": "https://new-api.example.com/v1"}
	stamped, err := stampRailpackPublicBuildURLs(data, urls)
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Steps []struct {
			Variables map[string]string `json:"variables"`
		} `json:"steps"`
		Deploy struct {
			Variables map[string]string `json:"variables"`
		} `json:"deploy"`
	}
	if json.Unmarshal(stamped, &plan) != nil || len(plan.Steps) < 3 {
		t.Fatal("invalid stamped plan")
	}
	for _, step := range plan.Steps {
		if step.Variables["VITE_API_URL"] != urls["VITE_API_URL"] {
			t.Fatal("a compile/install step missed the public URL")
		}
	}
	if plan.Deploy.Variables["VITE_API_URL"] != urls["VITE_API_URL"] || plan.Deploy.Variables["NODE_ENV"] != "production" {
		t.Fatal("runtime defaults lost")
	}
	for _, invalid := range []string{`null`, `{"steps":[],"deploy":{}}`, `{"steps":[null],"deploy":{}}`, `{"steps":[{"variables":[]}],"deploy":{}}`} {
		if _, err := stampRailpackPublicBuildURLs([]byte(invalid), urls); err == nil {
			t.Fatal("invalid plan accepted")
		}
	}
	if _, err := stampRailpackPublicBuildURLs(data, map[string]string{"DATABASE_URL": "https://db.example.com"}); err == nil {
		t.Fatal("secret key reached build plan")
	}
}

func TestPublicBuildURLsUseDockerArgumentsAndRailpackHelper(t *testing.T) {
	urls := map[string]string{"VITE_API_URL": "https://api.example.com/v1", "NEXT_PUBLIC_AUTH_URL": "https://auth.example.com"}
	docker := buildArgv(api.BuildManifest{Framework: api.FrameworkDockerfile, Workdir: "/build/src", OutDir: "/build/out", PublicBuildURLs: urls})
	joined := strings.Join(docker, "\n")
	if !strings.Contains(joined, "build-arg:VITE_API_URL=https://api.example.com/v1") || !strings.Contains(joined, "build-arg:NEXT_PUBLIC_AUTH_URL=https://auth.example.com") {
		t.Fatal("Dockerfile build args missing")
	}
	railpack := buildArgv(api.BuildManifest{Framework: api.FrameworkRailpackNode, Workdir: "/build/src", OutDir: "/build/out", PublicBuildURLs: urls, DependencyCache: true})
	script := railpack[2]
	helper := strings.Index(script, "GREGALE_INIT_HELPER=public-build-urls /proc/1/exe")
	cache := strings.Index(script, "GREGALE_INIT_HELPER=dependency-cache-inputs")
	if helper < 0 || cache < helper || strings.Contains(script, urls["VITE_API_URL"]) {
		t.Fatal("public plan configuration must precede dependency cache decisions without shell interpolation")
	}
}
