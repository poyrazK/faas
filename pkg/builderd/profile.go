package builderd

import (
	"encoding/json"
	"strings"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/state"
)

// persistedProfileFramework returns the build pipeline selected by the
// profile captured from the exact source archive at enqueue time. A missing,
// malformed, or newer profile is intentionally ignored so legacy deployments
// retain the detector fallback.
func persistedProfileFramework(dep state.Deployment) (Framework, string, bool) {
	if len(dep.InferredProfile) == 0 {
		return FrameworkUnknown, "", false
	}
	var profile frameworkprofile.Profile
	if err := json.Unmarshal(dep.InferredProfile, &profile); err != nil || profile.Version != frameworkprofile.Version {
		return FrameworkUnknown, "", false
	}
	fw, ok := frameworkFromProfile(profile.Framework)
	if !ok {
		return FrameworkUnknown, "", false
	}
	return fw, profile.FrameworkVer, true
}

// frameworkFromProfile keeps the persisted profile's specific framework names
// on the receipt while mapping them to the small set of builder pipelines.
func frameworkFromProfile(name string) (Framework, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "node", "express", "hono", "fastify", "nestjs":
		return FrameworkNode, true
	case "python", "fastapi", "flask", "django":
		return FrameworkPython, true
	case "go", "gin", "go-net-http":
		return FrameworkGo, true
	case "docker", "oci":
		return FrameworkDocker, true
	default:
		return FrameworkUnknown, false
	}
}
