package api

import "testing"

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
