package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

const publicBuildURLsHelperValue = "public-build-urls"

// Public configuration enters Railpack's literal step variables after prepare,
// so custom compile steps and generated steps see the same reviewed values.
// Changing a value also changes the plan and BuildKit's layer cache inputs.
func stampRailpackPublicBuildURLs(data []byte, urls map[string]string) ([]byte, error) {
	return stampRailpackBuildInputs(data, urls, "")
}

func stampRailpackBuildInputs(data []byte, urls map[string]string, digest string) ([]byte, error) {
	if !api.ValidBuildDependencyDigest(digest) {
		return nil, fmt.Errorf("invalid build dependency digest")
	}
	if len(data) > api.PublicBuildPlanMaxBytes || api.ValidatePublicBuildURLs(urls) != nil {
		return nil, fmt.Errorf("invalid public build configuration or Railpack plan")
	}
	var plan map[string]any
	if err := json.Unmarshal(data, &plan); err != nil || plan == nil {
		return nil, fmt.Errorf("invalid Railpack plan")
	}
	steps, ok := plan["steps"].([]any)
	if !ok || len(steps) == 0 {
		return nil, fmt.Errorf("build plan has no steps")
	}
	objects := make([]map[string]any, 0, len(steps)+1)
	for _, raw := range steps {
		step, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid Railpack step")
		}
		objects = append(objects, step)
	}
	deploy, ok := plan["deploy"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid Railpack deployment")
	}
	objects = append(objects, deploy)
	for i, object := range objects {
		variables, err := childObject(object, "variables")
		if err != nil {
			return nil, err
		}
		if digest != "" && i < len(steps) {
			variables["GREGALE_BUILD_DEPENDENCY_DIGEST"] = digest
		}
		for key, value := range urls {
			variables[key] = value
		}
	}
	return json.Marshal(plan)
}

func runPublicBuildURLs(args []string) int {
	if len(args) != 1 {
		return 1
	}
	raw, err := os.ReadFile(api.BuildManifestPath)
	var manifest api.BuildManifest
	if err != nil || json.Unmarshal(raw, &manifest) != nil || (len(manifest.PublicBuildURLs) == 0 && manifest.BuildDependencyDigest == "") {
		return 1
	}
	info, err := os.Lstat(args[0])
	if err != nil || !info.Mode().IsRegular() || info.Size() > api.PublicBuildPlanMaxBytes {
		return 1
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return 1
	}
	stamped, err := stampRailpackBuildInputs(data, manifest.PublicBuildURLs, manifest.BuildDependencyDigest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "public build URLs could not be applied to the Railpack plan")
		return 1
	}
	if err := os.WriteFile(args[0], stamped, 0o644); err != nil {
		return 1
	}
	return 0
}

func publicBuildURLKeys(urls map[string]string) []string {
	keys := make([]string, 0, len(urls))
	for key := range urls {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
