package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// gregale mcp connect --check explains why an agent does not see Gregale: it checks
// the hosted endpoint, each agent client's config, this project's
// .mcp.json, the agent key a headless config reads, and the account's
// connections and waiting approvals (ADR-20261109121411 §5). It only reads.

type mcpDoctorCheck struct {
	Check  string `json:"check"`
	Status string `json:"status"` // ok, warn, fail, skip
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

type mcpDoctorReport struct {
	URL    string           `json:"url"`
	Checks []mcpDoctorCheck `json:"checks"`
}

// mcpConnectionExpiringSoon is how close to its end a connection is reported.
const mcpConnectionExpiringSoon = 72 * time.Hour

func runMCPConnectCheck() int {
	url := strings.TrimRight(apiBase(), "/") + "/mcp"
	report := mcpDoctorReport{URL: url}
	add := func(c mcpDoctorCheck) { report.Checks = append(report.Checks, c) }

	endpoint := mcpEndpointCheck(url)
	if strings.HasPrefix(endpoint, "reachable") {
		add(mcpDoctorCheck{Check: "endpoint", Status: "ok", Detail: url + ": " + endpoint})
	} else {
		add(mcpDoctorCheck{Check: "endpoint", Status: "fail", Detail: url + ": " + endpoint, Fix: "check your network and the profile's API (gregale config profile check)"})
	}
	headless, setUp := false, false
	for _, client := range mcpConnectClients {
		c, usesKey := mcpDoctorClient(client, url)
		headless = headless || usesKey
		setUp = setUp || c.Status == "ok"
		add(c)
	}
	project, usesKey := mcpDoctorProject(url)
	headless = headless || usesKey
	setUp = setUp || project.Status == "ok"
	add(project)
	if !setUp {
		add(mcpDoctorCheck{Check: "setup", Status: "fail", Detail: "no agent on this machine or in this directory is set up to use Gregale", Fix: "gregale mcp connect"})
	}
	add(mcpDoctorAgentKey(url, headless))
	for _, c := range mcpDoctorAccount() {
		add(c)
	}
	failed := false
	for _, c := range report.Checks {
		failed = failed || c.Status == "fail"
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(report)); code != 0 {
			return code
		}
	} else {
		for _, c := range report.Checks {
			_, _ = fmt.Fprintf(osStdout, "%-4s %s: %s\n", mcpDoctorMark(c.Status), c.Check, c.Detail)
			if c.Fix != "" {
				_, _ = fmt.Fprintf(osStdout, "     Fix: %s\n", c.Fix)
			}
		}
	}
	if failed {
		return 1
	}
	return 0
}

func mcpDoctorMark(status string) string {
	switch status {
	case "ok":
		return "ok"
	case "warn":
		return "warn"
	case "fail":
		return "FAIL"
	}
	return "-"
}

// mcpDoctorClient checks one agent client's gregale entry and reports
// whether it reads the agent key from the environment.
func mcpDoctorClient(client, url string) (mcpDoctorCheck, bool) {
	c := mcpDoctorCheck{Check: client}
	if !mcpClientDetected(client) {
		c.Status, c.Detail = "skip", mcpClientName(client)+" is not installed"
		return c, false
	}
	switch client {
	case mcpConnectClaudeDesktop:
		c.Status, c.Detail = "skip", "Claude Desktop adds Gregale under Settings → Connectors; check it there"
		return c, false
	case mcpConnectCursor, mcpConnectVSCode, mcpConnectWindsurf:
		return mcpDoctorJSONClient(client, url)
	}
	connect := "gregale mcp connect --client " + client
	var entry string
	if client == mcpConnectClaude {
		out, err := mcpConnectRun("claude", "mcp", "get", mcpServerName)
		if err != nil {
			c.Status, c.Detail, c.Fix = "warn", "Claude Code has no gregale server", connect
			return c, false
		}
		entry = out
	} else {
		home, err := mcpConnectHome()
		if err != nil {
			c.Status, c.Detail = "fail", "home directory: "+err.Error()
			return c, false
		}
		data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			c.Status, c.Detail = "fail", err.Error()
			return c, false
		}
		entry = codexServerTable(string(data))
		if entry == "" {
			c.Status, c.Detail, c.Fix = "warn", "~/.codex/config.toml has no [mcp_servers.gregale]", connect
			return c, false
		}
	}
	usesKey := strings.Contains(entry, agentKeyEnv)
	if !strings.Contains(entry, url) {
		c.Status, c.Detail, c.Fix = "warn", mcpClientName(client)+"'s gregale server is not "+url, connect+" --force"
		return c, usesKey
	}
	c.Status, c.Detail = "ok", mcpClientName(client)+" connects to "+url
	if usesKey {
		c.Detail += " with the agent key in $" + agentKeyEnv
	} else {
		c.Detail += "; it signs in through the browser"
	}
	return c, usesKey
}

// mcpDoctorProject checks the gregale entry of this directory's .mcp.json.
func mcpDoctorProject(url string) (mcpDoctorCheck, bool) {
	c := mcpDoctorCheck{Check: "project"}
	data, err := os.ReadFile(".mcp.json")
	if errors.Is(err, os.ErrNotExist) {
		c.Status, c.Detail = "skip", "no .mcp.json in this directory"
		return c, false
	}
	if err != nil {
		c.Status, c.Detail = "fail", ".mcp.json: "+err.Error()
		return c, false
	}
	var file struct {
		MCPServers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		c.Status, c.Detail = "fail", ".mcp.json is not valid JSON: "+err.Error()
		return c, false
	}
	server, ok := file.MCPServers[mcpServerName]
	if !ok {
		c.Status, c.Detail, c.Fix = "skip", ".mcp.json has no gregale server", "gregale mcp connect --client claude-code --project"
		return c, false
	}
	usesKey := strings.Contains(server.Headers["Authorization"], agentKeyEnv)
	if server.URL != url {
		c.Status, c.Detail, c.Fix = "warn", ".mcp.json's gregale server is "+server.URL+", not "+url, "gregale mcp connect --client claude-code --project --force"
		return c, usesKey
	}
	c.Status, c.Detail = "ok", ".mcp.json offers "+url
	return c, usesKey
}

// mcpDoctorAgentKey checks the key a headless config reads: it is set, and
// it opens an MCP session as the agent will.
func mcpDoctorAgentKey(url string, headless bool) mcpDoctorCheck {
	c := mcpDoctorCheck{Check: "agent_key"}
	key := strings.TrimSpace(os.Getenv(agentKeyEnv))
	switch {
	case key == "" && !headless:
		c.Status, c.Detail = "skip", "no config reads $"+agentKeyEnv
		return c
	case key == "":
		c.Status, c.Detail, c.Fix = "fail", "a config reads $"+agentKeyEnv+", which is not set in this shell", "export "+agentKeyEnv+" where the agent runs, or mint a key with gregale mcp connect --headless --force"
		return c
	}
	if n := mcpHeadlessToolCount(mcpConnectOptions{URL: url}, key); n > 0 {
		c.Status, c.Detail = "ok", fmt.Sprintf("$%s opens an MCP session with %d tools", agentKeyEnv, n)
		return c
	}
	c.Status, c.Detail, c.Fix = "fail", "$"+agentKeyEnv+" was refused: it is revoked, expired or not an agent key", "gregale mcp connect --headless --force"
	return c
}

// mcpDoctorAccount reports the account's connected agents, those about to
// end, and approval requests waiting for a person.
func mcpDoctorAccount() []mcpDoctorCheck {
	client, err := authedClient()
	if err != nil {
		return []mcpDoctorCheck{{Check: "account", Status: "skip", Detail: "not logged in; run gregale login to check connections and approvals"}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var checks []mcpDoctorCheck
	conns, err := client.ListAgentConnections(ctx)
	if err != nil {
		checks = append(checks, mcpDoctorCheck{Check: "connections", Status: "warn", Detail: "could not list connected agents: " + oneLine(err.Error())})
	} else {
		var ending []string
		for _, conn := range conns.Items {
			if at, err := time.Parse(time.RFC3339, conn.ExpiresAt); err == nil && time.Until(at) < mcpConnectionExpiringSoon {
				ending = append(ending, fmt.Sprintf("%s (%s)", mcpConnectionName(conn), at.UTC().Format("2006-01-02 15:04 MST")))
			}
		}
		c := mcpDoctorCheck{Check: "connections", Status: "ok", Detail: fmt.Sprintf("%d connected agent(s)", len(conns.Items))}
		if len(conns.Items) == 0 {
			c.Status, c.Detail = "warn", "no agent has connected yet"
			c.Fix = "start your agent and sign in when the browser opens"
		}
		if len(ending) > 0 {
			c.Status = "warn"
			c.Detail += "; ending soon: " + strings.Join(ending, ", ")
			c.Fix = "the agent asks you to connect again when one ends; renew an agent key with gregale mcp connect --headless --force"
		}
		checks = append(checks, c)
	}
	approvals, err := client.ListAgentApprovals(ctx)
	if err != nil {
		return append(checks, mcpDoctorCheck{Check: "approvals", Status: "warn", Detail: "could not list approval requests: " + oneLine(err.Error())})
	}
	var pending []api.AgentApprovalResponse
	for _, a := range approvals.Items {
		// A request past its expiry can still read pending until it is
		// touched; nobody can decide it any more.
		if at, err := time.Parse(time.RFC3339, a.ExpiresAt); a.Status == api.AgentApprovalPending && (err != nil || time.Now().Before(at)) {
			pending = append(pending, a)
		}
	}
	if len(pending) == 0 {
		return append(checks, mcpDoctorCheck{Check: "approvals", Status: "ok", Detail: "no request is waiting for a person"})
	}
	first := pending[0]
	return append(checks, mcpDoctorCheck{Check: "approvals", Status: "warn",
		Detail: fmt.Sprintf("%d request(s) wait for a person; an agent waiting on one is blocked (first: %s %s, until %s)", len(pending), first.Method, first.Path, first.ExpiresAt),
		Fix:    "approve or deny at " + first.ReviewURL})
}

func mcpConnectionName(c api.AgentConnectionResponse) string {
	for _, name := range []string{c.Label, c.ClientName, c.Prefix} {
		if name != "" {
			return name
		}
	}
	return c.ID
}
