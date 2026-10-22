package mcphosting

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

// AuthorizationExpectations contains reviewed catalog exclusions, never tokens.
// Presence in a release role opts into missing/malformed bearer rejection.
type AuthorizationExpectations struct {
	DeniedTools             []string `json:"denied_tools,omitempty"`
	DeniedResources         []string `json:"denied_resources,omitempty"`
	DeniedResourceTemplates []string `json:"denied_resource_templates,omitempty"`
	DeniedPrompts           []string `json:"denied_prompts,omitempty"`
}

func (e AuthorizationExpectations) Validate() error {
	for _, group := range [][]string{e.DeniedTools, e.DeniedResources, e.DeniedResourceTemplates, e.DeniedPrompts} {
		seen := map[string]bool{}
		for _, entry := range group {
			if strings.TrimSpace(entry) == "" || strings.TrimSpace(entry) != entry || strings.IndexFunc(entry, unicode.IsControl) >= 0 || seen[entry] {
				return fmt.Errorf("authorization exclusions must be unique non-empty identifiers without surrounding whitespace or control characters")
			}
			seen[entry] = true
		}
	}
	return nil
}

// CheckAuthorizationVisibility inspects definitions only; it never calls tools,
// reads resource contents, renders prompts, or attempts forbidden operations.
func CheckAuthorizationVisibility(e AuthorizationExpectations, catalog Catalog) []Check {
	actual := [][]string{{}, {}, {}, {}}
	for _, item := range catalog.Tools {
		actual[0] = append(actual[0], item.Name)
	}
	for _, item := range catalog.Resources {
		actual[1] = append(actual[1], item.URI)
	}
	for _, item := range catalog.ResourceTemplates {
		actual[2] = append(actual[2], item.URITemplate)
	}
	for _, item := range catalog.Prompts {
		actual[3] = append(actual[3], item.Name)
	}
	checks := []Check{}
	groups := []struct {
		name   string
		denied []string
	}{{"tools", e.DeniedTools}, {"resources", e.DeniedResources}, {"resource_templates", e.DeniedResourceTemplates}, {"prompts", e.DeniedPrompts}}
	for i, group := range groups {
		if len(group.denied) == 0 {
			continue
		}
		seen := map[string]bool{}
		for _, entry := range actual[i] {
			seen[entry] = true
		}
		check := Check{Name: "hidden_" + group.name, Status: "passed", Detail: "Reviewed exclusions are absent from this role's catalog."}
		for _, denied := range group.denied {
			if seen[denied] {
				check.Status = "failed"
				check.Detail = "A reviewed exclusion appeared in this role's catalog."
				break
			}
		}
		checks = append(checks, check)
	}
	return checks
}

// CheckAuthorization uses only authentication/discovery/list RPCs. The optional
// insufficient-scope token is supplied separately and is never in the report.
// A 403 confirms the configured rejection expectation, not token validity.
func (c *Client) CheckAuthorization(ctx context.Context, e AuthorizationExpectations, catalog Catalog, insufficientScopeToken string) []Check {
	if e.Validate() != nil {
		return []Check{{Name: "authorization_configuration", Status: "failed", Detail: "Invalid reviewed authorization exclusions."}}
	}
	checks := CheckAuthorizationVisibility(e, catalog)
	credentials := []struct {
		name, token string
		status      int
	}{{"missing_token", "", http.StatusUnauthorized}, {"malformed_token", "gregale-invalid-bearer-probe", http.StatusUnauthorized}}
	if insufficientScopeToken != "" {
		credentials = append(credentials, struct {
			name, token string
			status      int
		}{"insufficient_scope", insufficientScopeToken, http.StatusForbidden})
	}
	methods := []string{"initialize", "tools/list", "resources/list", "resources/templates/list", "prompts/list"}
	if c.Version == ProtocolVersion {
		methods = append(methods, "server/discover")
	}
	for _, credential := range credentials {
		probe, err := NewClient(c.Endpoint, credential.token, c.Version)
		if err != nil {
			checks = append(checks, Check{Name: credential.name, Status: "failed", Detail: "Could not configure authorization probe."})
			continue
		}
		probe.HTTP = c.HTTP
		for _, method := range methods {
			params := map[string]any{}
			if method == "initialize" {
				params = map[string]any{"protocolVersion": c.Version, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "gregale-authorization-probe", "version": "1"}}
			}
			exchange, _ := probe.request(ctx, method, params, nil, false)
			check := Check{Name: credential.name + ":" + method, Status: "passed", Detail: fmt.Sprintf("Read-only probe rejected with HTTP %d.", credential.status)}
			if exchange.HTTPStatus != credential.status {
				check.Status = "failed"
				check.Detail = fmt.Sprintf("Read-only probe must reject with HTTP %d; observed HTTP %d. Check authentication policy and connectivity.", credential.status, exchange.HTTPStatus)
			}
			checks = append(checks, check)
		}
	}
	return checks
}
