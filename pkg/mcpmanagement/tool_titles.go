package mcpmanagement

import "strings"

// toolTitleWords spells out the words of a tool name that are not plain
// lowercase English when shown in a title.
var toolTitleWords = map[string]string{
	"api":      "API",
	"apis":     "APIs",
	"dns":      "DNS",
	"id":       "ID",
	"mcp":      "MCP",
	"openapi":  "OpenAPI",
	"postgres": "Postgres",
	"slo":      "SLO",
	"sql":      "SQL",
	"url":      "URL",
}

// toolTitleOverrides names the tools whose name alone reads badly as a title.
var toolTitleOverrides = map[string]string{
	"gregale_context": "Show connection context",
	findToolsName:     "Find more Gregale tools",
}

// toolTitle is the human-readable title MCP clients show for a tool, and
// the Claude Connectors Directory requires one on every tool:
// gregale_get_slo_status becomes "Get SLO status".
func toolTitle(name string) string {
	if title, ok := toolTitleOverrides[name]; ok {
		return title
	}
	words := strings.Split(strings.TrimPrefix(name, "gregale_"), "_")
	for i, word := range words {
		if spelled, ok := toolTitleWords[word]; ok {
			words[i] = spelled
		} else if i == 0 && word != "" {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}
