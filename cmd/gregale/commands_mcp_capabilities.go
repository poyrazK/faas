package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// This is the built-in starter contract, not a discovery result for arbitrary
// hosted code or a claim that native/provider qualification has completed.
type mcpStarterCapability struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Node        string `json:"node"`
	Go          string `json:"go"`
	Python      string `json:"python"`
}

func mcpStarterCapabilities() []mcpStarterCapability {
	all := func(id, description, support string) mcpStarterCapability {
		return mcpStarterCapability{id, description, support, support, support}
	}
	return []mcpStarterCapability{
		all("streamable_http", "Stateless Streamable HTTP", "supported"),
		all("legacy_protocol", "Legacy protocol compatibility (not the deprecated HTTP+SSE transport)", "supported"),
		all("catalogs", "Tools, resources, URI templates and prompts", "supported"),
		all("completions", "Bounded prompt and resource-template completions", "supported"),
		all("progress", "Streamed tool progress", "supported"),
		all("protocol_telemetry", "Bounded protocol outcomes, execution/stream timing and request correlation", "supported"),
		all("external_oauth", "External OAuth JWT validation and protected-resource metadata", "supported"),
		all("entry_scopes", "Caller-scoped tools, resources, prompts and completions", "supported"),
		all("origin_allowlist", "Reject browser Origin by default; explicit exact-origin allowlist", "supported"),
		all("cors_preflight", "Browser CORS preflight and protocol header exposure", "supported"),
		all("durable_tasks", "Encrypted PostgreSQL Tasks and separate web/worker/observer roles", "opt-in; Tasks use modern protocol"),
		all("task_input", "Durable task form input and resume", "opt-in; requires client form support"),
		all("subscriptions", "Task/resource updates and catalog list-change subscriptions", "modern protocol"),
		all("catalog_integrity", "Runtime catalog revision, digest and expected-digest readiness gate", "supported; process-local catalog"),
		all("shared_resource_updates", "Updates across web replicas for application-owned resources", "requires application-supplied shared adapter"),
		all("scale_to_zero", "Task workers scale from zero using backlog metrics", "requires separate always-on observer and database"),
		all("schema_setup", "Task schema initialization", "separate migration credential"),
		all("stateful_sessions", "Instance-local MCP sessions", "not supported by hosting profile"),
		all("managed_oauth", "Hosted login, consent, token issuance and refresh", "external provider required"),
	}
}

func cmdMCPCapabilities(args []string) int {
	fs := newFlagSet("mcp-capabilities", flag.ContinueOnError)
	runtime := fs.String("runtime", "", "filter to node, go or python")
	markdown := fs.Bool("markdown", false, "render the generated starter capability reference")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || (*runtime != "" && *runtime != "node" && *runtime != "go" && *runtime != "python") {
		return printErr("Invalid MCP capability flags", errors.New("--runtime must be node, go or python; positional arguments are not accepted"))
	}
	if *markdown && (*runtime != "" || jsonOutput) {
		return printErr("Invalid MCP capability flags", errors.New("--markdown cannot be combined with --runtime or --json"))
	}
	if *markdown {
		return jsonOut(renderMCPStarterCapabilities(osStdout))
	}
	rows := mcpStarterCapabilities()
	if jsonOutput {
		type entry struct {
			ID          string            `json:"id"`
			Description string            `json:"description"`
			Runtimes    map[string]string `json:"runtimes"`
		}
		entries := make([]entry, 0, len(rows))
		for _, row := range rows {
			runtimes := map[string]string{"node": row.Node, "go": row.Go, "python": row.Python}
			if *runtime != "" {
				runtimes = map[string]string{*runtime: runtimes[*runtime]}
			}
			entries = append(entries, entry{row.ID, row.Description, runtimes})
		}
		return jsonOut(writeJSON(struct {
			Version          int      `json:"version"`
			Maturity         string   `json:"maturity"`
			Scope            string   `json:"scope"`
			ProtocolVersions []string `json:"protocol_versions"`
			Capabilities     []entry  `json:"capabilities"`
			Qualification    string   `json:"qualification"`
		}{1, "preview", "built-in starters", []string{mcphosting.ProtocolVersion, mcphosting.LegacyProtocolVersion}, entries, "Native lifecycle and real provider/client behavior require separate release evidence; this command does not inspect that evidence."}))
	}
	_, _ = fmt.Fprintf(osStdout, "Built-in MCP starters (preview): protocols %s and %s\n", mcphosting.ProtocolVersion, mcphosting.LegacyProtocolVersion)
	for _, row := range rows {
		if *runtime == "" {
			_, _ = fmt.Fprintf(osStdout, "%s: node=%s; go=%s; python=%s\n", row.Description, row.Node, row.Go, row.Python)
		} else {
			value := map[string]string{"node": row.Node, "go": row.Go, "python": row.Python}[*runtime]
			_, _ = fmt.Fprintf(osStdout, "%s: %s\n", row.Description, value)
		}
	}
	_, _ = fmt.Fprintln(osStdout, "Native lifecycle and real provider/client qualification require separate release evidence.")
	return 0
}

func renderMCPStarterCapabilities(w io.Writer) error {
	_, err := fmt.Fprintf(w, "# Gregale MCP starter capabilities\n\nGenerated by `gregale mcp capabilities --markdown`. This reference describes the built-in starters, not arbitrary hosted code. Hosting remains **preview**.\n\nCurrent protocol: `%s`. Legacy protocol: `%s`. Legacy protocol compatibility uses Streamable HTTP and does not imply deprecated HTTP+SSE transport support.\n\n| Capability | Node | Go | Python |\n| --- | --- | --- | --- |\n", mcphosting.ProtocolVersion, mcphosting.LegacyProtocolVersion)
	if err != nil {
		return err
	}
	for _, row := range mcpStarterCapabilities() {
		if _, err := fmt.Fprintf(w, "| %s | %s | %s | %s |\n", row.Description, row.Node, row.Go, row.Python); err != nil {
			return err
		}
	}
	_, err = io.WriteString(w, "\nAll starters default to public access and disabled Tasks. Use `mcp init --auth external-oauth` to configure private access and `--tasks --name <prefix>` to scaffold a split durable stack. Browser CORS requires an exact allowed origin; preflight does not replace bearer authorization.\n\nCatalog hashes cover advertised definitions and policy, not handler code. Application handlers must validate arguments and business-data ownership. External side effects need Task-ID idempotency because recovery can replay work. Runtime catalog mutations and default application-resource update adapters are process-local.\n\nPortable protocol, SDK and PostgreSQL checks do not establish native Firecracker cold boot/restore or real provider/client login interoperability. Follow [release qualification](ops/mcp-release-qualification.md) for the evidence required before promoting hosting maturity. This command does not inspect release evidence.\n")
	return err
}
