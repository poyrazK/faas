package main

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"regexp"
)

// Declarations are validated without reading bearer environment values.
func validateMCPAuthorizationDeclarations(role *mcpReleaseRole) error {
	if role.TokenEnv != "" && !mcpAuditEnvReference(role.TokenEnv) {
		return errors.New("role token reference must be a valid environment variable name")
	}
	policy := role.Authorization
	if policy == nil {
		return nil
	}
	if role.TokenEnv == "" || (policy.InsufficientScopeTokenEnv != "" && !mcpAuditEnvReference(policy.InsufficientScopeTokenEnv)) {
		return errors.New("authorization declarations require valid token environment references")
	}
	if policy.InsufficientScopeTokenEnv == role.TokenEnv {
		return errors.New("positive and insufficient-scope references must differ")
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	catalog := mcphosting.Catalog{Tools: role.contract.Tools, Resources: role.contract.Resources, ResourceTemplates: role.contract.ResourceTemplates, Prompts: role.contract.Prompts}
	if !mcpAuthorizationChecksPassed(mcphosting.CheckAuthorizationVisibility(policy.AuthorizationExpectations, catalog)) {
		return errors.New("authorization exclusions contradict the reviewed baseline")
	}
	return nil
}

func mcpAccessSourceChecks(cfg mcphosting.Config, roles []mcpReleaseRole) []mcphosting.Check {
	status := "passed"
	if cfg.Auth.Mode != "external-oauth" {
		status = "failed"
	}
	checks := []mcphosting.Check{{Name: "source_oauth", Status: status, Detail: "Access auditing requires a reviewed external-oauth source configuration."}}
	for _, role := range roles {
		status = "passed"
		if role.TokenEnv == "" || validateMCPAuthorizationDeclarations(&role) != nil || !mcpRoleCatalogListed(cfg, role) {
			status = "failed"
		}
		checks = append(checks, mcphosting.Check{Name: "role_source:" + role.Name, Status: status, Detail: "Role baseline entries must be admitted by source allowlists and must not contradict reviewed exclusions. Token claims are not evaluated."})
	}
	return checks
}

func mcpRoleCatalogListed(cfg mcphosting.Config, role mcpReleaseRole) bool {
	for _, tool := range role.contract.Tools {
		if cfg.Auth.ToolScopes != nil {
			if _, ok := cfg.Auth.ToolScopes[tool.Name]; !ok {
				return false
			}
		}
	}
	for _, prompt := range role.contract.Prompts {
		if cfg.Auth.PromptScopes != nil {
			if _, ok := cfg.Auth.PromptScopes[prompt.Name]; !ok {
				return false
			}
		}
	}
	// Grant every declared scope only to answer structural allowlist membership;
	// this does not infer or evaluate any real role's JWT claims.
	granted := []string{}
	for _, scopes := range cfg.Auth.ResourceScopes {
		granted = append(granted, scopes...)
	}
	for _, resource := range role.contract.Resources {
		if !api.MCPResourceAllowed(cfg.Auth.ResourceScopes, resource.URI, granted) {
			return false
		}
	}
	for _, resource := range role.contract.ResourceTemplates {
		if !api.MCPResourceAllowed(cfg.Auth.ResourceScopes, resource.URITemplate, granted) {
			return false
		}
	}
	return true
}

func mcpAuditEnvReference(name string) bool {
	valid, _ := regexp.MatchString(`^[A-Za-z_][A-Za-z0-9_]*$`, name)
	return valid
}
