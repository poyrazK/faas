//go:build linux

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildDependenciesInvalidateWarmRailpackCompileInputs(t *testing.T) {
	plan := []byte(`{"steps":[{"name":"install","variables":{"KEEP":"value"}},{"name":"build"}],"deploy":{"variables":{}}}`)
	first, err := stampRailpackBuildInputs(plan, nil, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	second, err := stampRailpackBuildInputs(plan, nil, strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) == string(second) {
		t.Fatal("changed upstream deployment kept compile input")
	}
	var parsed struct {
		Steps  []struct{ Variables map[string]string }
		Deploy struct{ Variables map[string]string }
	}
	if err := json.Unmarshal(second, &parsed); err != nil {
		t.Fatal(err)
	}
	for _, step := range parsed.Steps {
		if step.Variables["GREGALE_BUILD_DEPENDENCY_DIGEST"] != strings.Repeat("b", 64) {
			t.Fatal("an install/build step kept previous dependency inputs")
		}
	}
	if _, ok := parsed.Deploy.Variables["GREGALE_BUILD_DEPENDENCY_DIGEST"]; ok {
		t.Fatal("internal cache partition became runtime configuration")
	}
	if _, err := stampRailpackBuildInputs(plan, nil, "$(unsafe)"); err == nil {
		t.Fatal("invalid digest accepted")
	}
}

func TestBuildDependencyArgvForDockerAndRailpack(t *testing.T) {
	for _, framework := range []api.BuildFramework{api.FrameworkDockerfile, api.FrameworkRailpackNode} {
		m := api.BuildManifest{Framework: framework, Workdir: "/build/src", OutDir: "/build/out", BuildDependencyDigest: strings.Repeat("a", 64)}
		argv := strings.Join(buildArgv(m), " ")
		if framework == api.FrameworkDockerfile && !strings.Contains(argv, "--no-cache") {
			t.Fatal("Docker retained compile layers")
		}
		if framework == api.FrameworkRailpackNode && !strings.Contains(argv, "public-build-urls /proc/1/exe") {
			t.Fatal("Railpack omitted digest plan injection")
		}
		m.BuildDependencyDigest = ""
		legacy := strings.Join(buildArgv(m), " ")
		if strings.Contains(legacy, "--no-cache") || strings.Contains(legacy, "public-build-urls") {
			t.Fatal("ordinary build changed caching behavior")
		}
	}
}
