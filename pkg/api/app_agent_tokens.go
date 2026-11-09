package api

import (
	"fmt"
	"slices"
)

// App-agent tokens use the existing app-bound deploy-token principal. They
// cannot acquire account administration or a global resource-management scope.
func NormalizeAppAgentScopes(requested []string) ([]string, error) {
	if len(requested) == 0 {
		requested = []string{ScopeAgent, ScopeAppsRead, ScopeDeployWrite}
	}
	if !slices.Contains(requested, ScopeAgent) {
		requested = append(slices.Clone(requested), ScopeAgent)
	}
	scopes, err := NormalizeCreateKeyScopes(requested)
	if err != nil {
		return nil, err
	}
	return scopes, ValidateDeployTokenScopes(scopes)
}

func ValidateDeployTokenScopes(scopes []string) error {
	agent := slices.Contains(scopes, ScopeAgent)
	for _, scope := range scopes {
		if scope == ScopeDeployWrite {
			continue
		}
		if agent && slices.Contains([]string{ScopeAgent, ScopeAppsRead, ScopeEnvWrite, ScopeSecretsRequest, ScopeStorageManage, ScopeUsageRead}, scope) {
			continue
		}
		return fmt.Errorf("unsupported app-bound token scope %q", scope)
	}
	if agent && !slices.Contains(scopes, ScopeAppsRead) {
		return fmt.Errorf("app-agent tokens require apps:read")
	}
	if len(scopes) == 0 {
		return fmt.Errorf("app-bound tokens require at least one scope")
	}
	return nil
}
