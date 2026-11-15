package gregalemanifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A workflow function's adapter runs its handler under the protocol core of
// the SDK in the function's dependencies (ADR-20261113120824 §1), so a deploy
// without the SDK is refused before the build rather than failing every run.

var pythonWorkflowSDKRE = regexp.MustCompile(`(?im)^\s*["']?(gregale-sdk|faas-sdk|faas_sdk)\b`)

// CheckWorkflowFunctionSDK reports an error when a Node or Python workflow
// function declares no SDK dependency in its root or the project directory.
// Other runtimes serve the protocol from their own code.
func CheckWorkflowFunctionSDK(projectDir string, plan FunctionPlan) error {
	if !plan.Spec.IsWorkflow() {
		return nil
	}
	dirs := []string{filepath.Join(projectDir, filepath.FromSlash(plan.Root))}
	if plan.Root != "." && plan.Root != "" {
		dirs = append(dirs, projectDir)
	}
	switch {
	case strings.HasPrefix(plan.Runtime, "node"):
		for _, dir := range dirs {
			if nodeDeclaresSDK(filepath.Join(dir, "package.json")) {
				return nil
			}
		}
		return fmt.Errorf("function %q is a workflow, so it needs @gregale/sdk-node in its package.json dependencies", plan.Spec.Name)
	case strings.HasPrefix(plan.Runtime, "python"):
		for _, dir := range dirs {
			if pythonDeclaresSDK(dir) {
				return nil
			}
		}
		return fmt.Errorf("function %q is a workflow, so it needs gregale-sdk (faas_sdk) in its requirements.txt or pyproject.toml", plan.Spec.Name)
	}
	return nil
}

func nodeDeclaresSDK(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(raw, &pkg) != nil {
		return false
	}
	_, dep := pkg.Dependencies["@gregale/sdk-node"]
	return dep
}

func pythonDeclaresSDK(dir string) bool {
	if info, err := os.Stat(filepath.Join(dir, "faas_sdk")); err == nil && info.IsDir() {
		return true // vendored
	}
	for _, name := range []string{"requirements.txt", "pyproject.toml"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil && pythonWorkflowSDKRE.Match(raw) {
			return true
		}
	}
	return false
}
