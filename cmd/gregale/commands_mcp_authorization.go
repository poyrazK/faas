package main

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpReleaseAuthorization struct {
	mcphosting.AuthorizationExpectations
	InsufficientScopeTokenEnv string `json:"insufficient_scope_token_env,omitempty"`
	insufficientToken         string
}

func loadMCPRoleAuthorization(role *mcpReleaseRole) error {
	if role.Authorization == nil {
		return nil
	}
	if role.token == "" || !api.ValidMCPProbeToken(role.token) {
		return errors.New("authorization expectations require a valid positive role token environment reference")
	}
	policy := role.Authorization
	if err := policy.Validate(); err != nil {
		return err
	}
	catalog := mcphosting.Catalog{Tools: role.contract.Tools, Resources: role.contract.Resources, ResourceTemplates: role.contract.ResourceTemplates, Prompts: role.contract.Prompts}
	if !mcpAuthorizationChecksPassed(mcphosting.CheckAuthorizationVisibility(policy.AuthorizationExpectations, catalog)) {
		return errors.New("authorization exclusions contradict the reviewed role baseline")
	}
	if policy.InsufficientScopeTokenEnv != "" {
		token, err := mcpToken(policy.InsufficientScopeTokenEnv)
		if err != nil {
			return errors.New("insufficient-scope token environment reference must be populated with a valid credential")
		}
		if !api.ValidMCPProbeToken(token) || token == role.token || token == "gregale-invalid-bearer-probe" {
			return errors.New("insufficient-scope token must be a distinct valid bearer token")
		}
		policy.insufficientToken = token
	}
	return nil
}

func mcpAuthorizationChecksPassed(checks []mcphosting.Check) bool {
	for _, check := range checks {
		if check.Status != "passed" {
			return false
		}
	}
	return true
}

func mcpReleaseRequiresAuthorization(roles []mcpReleaseRole) bool {
	for _, role := range roles {
		if role.Authorization != nil {
			return true
		}
	}
	return false
}
