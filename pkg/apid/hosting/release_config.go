package hosting

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

type ConfigHost interface{ HostingConfigEnabled() bool }

func (s Service) configEnabled() bool {
	h, ok := s.Host.(ConfigHost)
	return ok && h.HostingConfigEnabled()
}

func (s Service) requireConfig(w http.ResponseWriter, manifest api.HostingManifest) bool {
	if manifest.Config == nil || s.configEnabled() {
		return true
	}
	api.WriteProblem(w, api.NewProblem(503, "frontend_hosting_config_disabled", "Release configuration unavailable", "internal release configuration is disabled"))
	return false
}
