package mcpmanagement

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/toolgrant"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// Discovery and delegated invocation use exact native grants and personal keys.
func (m *management) toolDiscoveryAccess() error {
	if m.opts.Org != "" || m.opts.agentKey && m.opts.AllowedApps != nil {
		return invalid("tool_registry_access_denied", "shared-organization and app-bound agent tool delegation is not available")
	}
	return nil
}

func (m *management) scopedToolDiscovery() bool { return m.remote() || m.opts.AllowedApps != nil }

func (m *management) registryAppAllowed(slug, id string) bool {
	return m.opts.AllowedApps == nil || m.opts.AllowedApps[slug] == id
}

func (m *management) describeScopedTool(ctx context.Context, a describeToolArgs) (any, error) {
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	p, err := m.client.GetRegistryTool(ctx, a.Version, a.Name)
	if err != nil {
		return nil, err
	}
	if err = validateScopedRegistryTool(p, account.ID, a); err != nil {
		return nil, err
	}
	if !m.registryAppAllowed(p.App, p.AppID) {
		return nil, invalid("app_access_denied", "published tool is outside this connection's app consent")
	}
	catalog, err := mcphosting.ReadContract(bytes.NewReader(p.Catalog))
	if err != nil {
		return nil, invalid("invalid_response", "invalid scoped tool catalog")
	}
	canonical, err := mcphosting.MarshalContract(catalog)
	if err != nil || !bytes.Equal(canonical, p.Catalog) || catalog.Version != 2 || len(catalog.Tools) != 1 || catalog.Tools[0].Name != a.Name || len(catalog.Resources) != 0 || len(catalog.ResourceTemplates) != 0 || len(catalog.Prompts) != 0 || len(catalog.Extensions) != 0 || len(catalog.Capabilities) != 1 || catalog.Capabilities[0] != "tools" {
		return nil, invalid("invalid_response", "scoped catalog must contain only the exact requested tool")
	}
	end := min(a.CatalogOffset+toolTemplateChunkBytes, len(p.Catalog))
	next := -1
	if end < len(p.Catalog) {
		next = end
	}
	tool := catalog.Tools[0]
	var toolView any = tool
	data, err := json.Marshal(tool)
	if err != nil {
		return nil, err
	}
	inline := len(data) <= maxResultBytes/4
	if !inline {
		toolView = map[string]any{"name": tool.Name}
	}
	return map[string]any{"schema_version": 1, "version": p.Version, "app": p.App, "app_id": p.AppID, "deployment_id": p.DeploymentID,
		"tool": toolView, "tool_schema_inline": inline, "access_mode": p.AccessMode, "required_scopes": p.RequiredScopes,
		"source_sha256": p.SourceSHA256, "deployment_source_sha256": p.DeploymentSourceSHA256, "catalog_sha256": p.CatalogSHA256,
		"catalog_base64": base64.StdEncoding.EncodeToString(p.Catalog[a.CatalogOffset:end]), "catalog_contract_sha256": p.CatalogContractSHA256,
		"published_catalog_contract_sha256": p.PublishedCatalogContractSHA256, "catalog_projection": "single_tool", "grants": p.Grants,
		"catalog_offset": a.CatalogOffset, "next_catalog_offset": next, "catalog_bytes": len(p.Catalog), "evidence_origin": p.EvidenceOrigin, "availability": p.Availability, "execution_authorized": false}, nil
}

func validateScopedRegistryTool(p api.ToolRegistryTool, account string, a describeToolArgs) error {
	if account == "" || p.AccountID != account || p.Version != a.Version || p.Name != a.Name || !api.ValidAppSlug(p.App) || !toolgrant.ValidResourceID(p.AppID) || p.DeploymentID == "" ||
		!toolpublication.ValidID(p.SourceSHA256) || !toolpublication.ValidID(p.DeploymentSourceSHA256) || !toolpublication.ValidID(p.CatalogSHA256) ||
		!toolpublication.ValidID(p.PublishedCatalogContractSHA256) || toolpublication.Digest(p.Catalog) != p.CatalogContractSHA256 || len(p.Catalog) > api.ToolPublicationCatalogMaxBytes ||
		p.EvidenceOrigin != "publisher_attested" || p.Availability != "not_checked" || (p.AccessMode != "open" && p.AccessMode != "external-oauth") || a.CatalogOffset > len(p.Catalog) || len(p.RequiredScopes) > 64 || len(p.Grants) > api.ToolGrantsPerKey {
		return invalid("invalid_response", "scoped registry returned an inconsistent version, tool or catalog")
	}
	for _, scope := range p.RequiredScopes {
		if scope == "" || len(scope) > 256 {
			return invalid("invalid_response", "invalid scoped access requirement")
		}
	}
	seen := map[string]bool{}
	for _, grant := range p.Grants {
		if grant.CredentialBindingSHA256 != "" && (!toolpublication.ValidID(grant.CredentialBindingSHA256) || p.AccessMode != "external-oauth" || !grant.AllowInvocation) {
			return invalid("invalid_response", "invalid private credential binding")
		}
		if !toolgrant.ValidID(grant.GrantID) || seen[grant.GrantID] || grant.ExpiresAt.IsZero() {
			return invalid("invalid_response", "invalid scoped invocation grant")
		}
		seen[grant.GrantID] = true
	}
	return nil
}
