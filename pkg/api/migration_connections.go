package api

import "strings"

// ProjectServiceConnections maps selected callers' existing runtime variable
// names to declared services in the same migration. It contains no values.
type ProjectServiceConnections map[string]map[string]string

// MigrationServiceConnectionCheck pins one runtime variable to a logical
// service and an exact deployment. Values are resolved only in the task guest.
type MigrationServiceConnectionCheck struct {
	EnvKey       string `json:"env_key"`
	Service      string `json:"service"`
	DeploymentID string `json:"deployment_id"`
}

// ValidateMigrationConnectionKey excludes platform settings and conventional
// browser/build-time variables, which cannot use private runtime addresses.
func ValidateMigrationConnectionKey(key string) *Problem {
	if ValidateSecretKey(key) != nil || key == "PORT" || strings.HasPrefix(key, "GREGALE_") || strings.HasPrefix(key, "FAAS_") || IsPlatformIdentityEnvKey(key) {
		return ErrValidation("service connections require a customer runtime environment key")
	}
	for _, prefix := range []string{"VITE_", "NEXT_PUBLIC_", "REACT_APP_", "PUBLIC_", "NUXT_PUBLIC_", "GATSBY_"} {
		if strings.HasPrefix(key, prefix) {
			return ErrValidation("browser and build-time variables need a public URL configured separately")
		}
	}
	return nil
}
