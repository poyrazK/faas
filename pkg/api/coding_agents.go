package api

import "strings"

// CodingAgent pairs the environment variable a coding agent sets in the
// shells it runs with the name the CLI reports for it.
type CodingAgent struct {
	Marker string
	Name   string
}

// CodingAgents is the one table of coding agents the CLI recognises
// (ADR-20261112223000 §5 and Amendment 1). The CLI uses it for its agent
// hint and its User-Agent, and apid uses it to read the User-Agent back, so
// the reported names stay a closed set. Each marker was confirmed against
// that agent's installed release on 2026-11-14.
var CodingAgents = []CodingAgent{
	{Marker: "CLAUDECODE", Name: "claude-code"},
	{Marker: "GEMINI_CLI", Name: "gemini-cli"},
	{Marker: "CODEX_THREAD_ID", Name: "codex"},
	{Marker: "CODEX_SANDBOX", Name: "codex"},
	{Marker: "CURSOR_AGENT", Name: "cursor"},
}

// cliUserAgentProduct is the product token the Gregale CLI sends.
const cliUserAgentProduct = "gregale/"

// CodingAgentFromEnv returns the name of the first coding agent whose marker
// is set to a non-empty value, or "" when none is.
func CodingAgentFromEnv(getenv func(string) string) string {
	for _, a := range CodingAgents {
		if getenv(a.Marker) != "" {
			return a.Name
		}
	}
	return ""
}

// CodingAgentFromUserAgent returns the coding agent a Gregale CLI
// User-Agent reports in its comment, as in
// "gregale/0.1.0 (linux/amd64; agent=claude-code)", or "" when it reports
// none. Only names in CodingAgents are returned. The value is self-reported
// by the client: it attributes usage and never grants or refuses anything.
func CodingAgentFromUserAgent(ua string) string {
	if !strings.HasPrefix(ua, cliUserAgentProduct) {
		return ""
	}
	open := strings.IndexByte(ua, '(')
	end := strings.LastIndexByte(ua, ')')
	if open < 0 || end < open {
		return ""
	}
	for part := range strings.SplitSeq(ua[open+1:end], ";") {
		name, ok := strings.CutPrefix(strings.TrimSpace(part), "agent=")
		if ok && knownCodingAgent(name) {
			return name
		}
	}
	return ""
}

func knownCodingAgent(name string) bool {
	for _, a := range CodingAgents {
		if a.Name == name {
			return true
		}
	}
	return false
}
