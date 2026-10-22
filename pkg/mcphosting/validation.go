package mcphosting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// ValidationReport describes the stateless Gregale hosting profile. Catalog
// validation never executes tools, reads resource contents or renders prompts.
type ValidationReport struct {
	Catalog           Catalog `json:"-"`
	OK                bool    `json:"ok"`
	ProtocolVersion   string  `json:"protocol_version"`
	Checks            []Check `json:"checks"`
	CatalogSHA256     string  `json:"catalog_sha256,omitempty"`
	Tools             int     `json:"tools"`
	Resources         int     `json:"resources"`
	ResourceTemplates int     `json:"resource_templates"`
	Prompts           int     `json:"prompts"`
}

// ValidateCompatibility is shared by local preflight and exact-instance API
// qualification. The caller supplies a bounded context and an authorized client.
// Returned diagnostics omit remote bodies and errors, which can contain secrets.
func ValidateCompatibility(ctx context.Context, c *Client, baseline *Contract) ValidationReport {
	report := ValidationReport{Checks: []Check{}, ProtocolVersion: c.Version}
	add := func(name, detail string, err error) bool {
		check := Check{Name: name, Status: "passed"}
		if err != nil {
			check.Status = "failed"
			check.Detail = detail
		}
		report.Checks = append(report.Checks, check)
		return err == nil
	}
	if !add("initialize", "Initialization failed: check reachability, authorization, protocol negotiation and stateless session configuration.", c.InitializeStateless(ctx)) {
		return report
	}
	catalog, exchange, err := c.DiscoverCatalog(ctx)
	if !add("catalog_discovery", "An advertised catalog could not be listed. Check capability declarations and paginated JSON/SSE responses.", err) {
		return report
	}
	for _, tool := range catalog.Tools {
		if tool.InputSchema["type"] != "object" {
			err = fmt.Errorf("input schema must describe an object")
		}
		if len(tool.OutputSchema) > 0 {
			var schema map[string]any
			if json.Unmarshal(tool.OutputSchema, &schema) != nil || schema["type"] != "object" {
				err = fmt.Errorf("output schema must describe an object")
			}
		}
	}
	if len(exchange.RejectedTools) != 0 {
		err = fmt.Errorf("rejected tool definitions")
	}
	if !add("tool_schemas", "Tools contain unsupported or invalid input schemas; inspect the tool definitions with mcp doctor.", err) {
		return report
	}
	contract, err := NewCatalogContract(c.Version, catalog)
	if !add("catalog_definitions", "Catalog definitions are invalid or inconsistent with advertised capabilities.", err) {
		return report
	}
	encoded, err := MarshalContract(contract)
	if !add("catalog_encoding", "Catalog cannot be encoded as a stable contract.", err) {
		return report
	}
	digest := sha256.Sum256(encoded)
	report.CatalogSHA256 = hex.EncodeToString(digest[:])
	report.Tools = len(catalog.Tools)
	report.Resources = len(catalog.Resources)
	report.ResourceTemplates = len(catalog.ResourceTemplates)
	report.Prompts = len(catalog.Prompts)
	if baseline != nil {
		diff, e := CompareContractsWithOptions(*baseline, contract, ContractDiffOptions{StrictCatalog: true})
		if e == nil && !diff.Compatible {
			e = fmt.Errorf("incompatible catalog")
		}
		if !add("baseline", "Catalog differs from the reviewed baseline; inspect with mcp diff.", e) {
			return report
		}
	}
	report.Checks = append(report.Checks, Check{Name: "application_execution", Status: "skipped", Detail: "Tool execution, resource contents and rendered prompt responses require explicit application fixtures."}, Check{Name: "authentication_rejection", Status: "skipped", Detail: "Use a release policy with authorization expectations to verify rejection and role visibility."})
	report.Catalog = catalog
	report.OK = true
	return report
}
