package githubd

import (
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/hostidentity"
)

// AppsDomain is the primary apps domain app and preview links name
// (FAAS_APPS_DOMAIN; unset and the legacy apps.gregale.dev both mean
// gregale.dev).
func AppsDomain() string {
	domain := strings.Trim(strings.TrimSpace(os.Getenv(hostidentity.EnvAppsDomain)), ".")
	if domain == "" || domain == "apps.gregale.dev" {
		domain = "gregale.dev"
	}
	return domain
}

// PlatformDomain hosts the dashboard and API that GitHub links point at
// (ADR-20261026100000): FAAS_PLATFORM_DOMAIN, else the apps domain.
func PlatformDomain() string {
	if domain := strings.Trim(strings.TrimSpace(os.Getenv(hostidentity.EnvPlatformDomain)), "."); domain != "" {
		return domain
	}
	return AppsDomain()
}
