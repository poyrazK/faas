package api

import (
	"strings"
	"testing"
)

// adr: 678
func TestValidProjectImage(t *testing.T) {
	for _, tc := range []struct {
		ref  string
		want bool
	}{
		{"docker.io/library/nginx:1.27", true},
		{"registry.example.com:5000/team/app:release-1", true},
		{"ghcr.io/team/app@sha256:" + strings.Repeat("a", 64), true},
		{"nginx:latest", false},
		{"https://example.com/app:v1", false},
		{"example.com/app:bad tag", false},
		{"example.com/app@sha256:short", false},
		{"example.com/a/../app:v1", false},
		{"example.com//app:v1", false},
		{"example.com/app:v1\n", false},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			if got := ValidProjectImage(tc.ref); got != tc.want {
				t.Fatalf("ValidProjectImage = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestValidFunctionRuntime(t *testing.T) {
	for _, runtime := range FunctionRuntimes {
		if !ValidFunctionRuntime(runtime) {
			t.Errorf("ValidFunctionRuntime(%q) = false", runtime)
		}
	}
	for _, runtime := range []string{"", "node", "Node22", "python311", "go124_alpine"} {
		if ValidFunctionRuntime(runtime) {
			t.Errorf("ValidFunctionRuntime(%q) = true", runtime)
		}
	}
}

func TestValidDeploymentImage(t *testing.T) {
	valid := "registry.example.com/team/app@sha256:1111111111111111111111111111111111111111111111111111111111111111"
	if !ValidDeploymentImage(valid) {
		t.Fatalf("ValidDeploymentImage(%q) = false", valid)
	}
	if digest, ok := DeploymentImageDigest(valid); !ok || digest != "@sha256:1111111111111111111111111111111111111111111111111111111111111111" {
		t.Fatalf("DeploymentImageDigest = (%q, %t)", digest, ok)
	}
	for _, ref := range []string{
		"registry.example.com/team/app:latest",
		"app@sha256:1111111111111111111111111111111111111111111111111111111111111111",
		"registry.example.com/team/app@sha256:1111",
		"registry.example.com/team/app@sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		" registry.example.com/team/app@sha256:1111111111111111111111111111111111111111111111111111111111111111",
		"registry.example.com/team/app@sha256:1111111111111111111111111111111111111111111111111111111111111111\n",
	} {
		if ValidDeploymentImage(ref) {
			t.Errorf("ValidDeploymentImage(%q) = true", ref)
		}
	}
}

// adr: 083
func TestValidateExistingAppShape(t *testing.T) {
	if _, problem := ValidateExistingAppShape("app", "", "app", ""); problem != nil {
		t.Fatalf("unchanged app shape rejected: %+v", problem)
	}
	if field, problem := ValidateExistingAppShape("app", "", "function", "node22"); problem == nil || field != "app.class" || problem.Code != CodeValidation {
		t.Fatalf("class mismatch = (%q, %+v)", field, problem)
	}
	if field, problem := ValidateExistingAppShape("function", "node22", "function", "python312"); problem == nil || field != "app.runtime" || problem.Code != CodeValidation {
		t.Fatalf("runtime mismatch = (%q, %+v)", field, problem)
	}
}
