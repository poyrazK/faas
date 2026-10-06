// Package executionprofiles describes immutable, platform-built dependency sets.
package executionprofiles

import (
	_ "embed"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

//go:embed python-data-v1.json
var pythonDataV1 []byte

// Packages returns a new map so receipt callers cannot mutate the catalog.
func Packages(profile api.ExecutionProfile) map[string]string {
	if profile.Normalized() != api.ExecutionProfilePythonDataV1 {
		return nil
	}
	var manifest struct {
		Packages map[string]string `json:"packages"`
	}
	if err := json.Unmarshal(pythonDataV1, &manifest); err != nil {
		panic("invalid embedded execution profile")
	}
	return manifest.Packages
}
