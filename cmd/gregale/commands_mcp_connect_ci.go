package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// gregale mcp connect --ci sets up an agent that runs in GitHub Actions
// (ADR-20261109121411 §8): it mints an agent key, stores it as the
// repository secret GREGALE_AGENT_KEY with gh, and writes a workflow that
// gives an agent Gregale's hosted MCP server. The key is never printed or
// written to disk; gh reads it on stdin.

const (
	ciSecretName   = agentKeyEnv
	ciWorkflowPath = ".github/workflows/gregale-agent.yml"
)

// mcpConnectRunStdin runs a command with stdin; a seam for tests.
var mcpConnectRunStdin = func(stdin, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...) //nolint:gosec // fixed gh arguments
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

var ciRepoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type mcpConnectCIOptions struct {
	Repo      string
	Scopes    []string
	Approvals *[]string
	Force     bool
	DryRun    bool
	URL       string
}

type mcpConnectCIResult struct {
	Repo     string `json:"repo"`
	Secret   string `json:"secret"`
	KeyID    string `json:"agent_key_id,omitempty"`
	Workflow string `json:"workflow"`
	// WorkflowStatus is written, kept (it existed) or would_write.
	WorkflowStatus string   `json:"workflow_status"`
	DryRun         bool     `json:"dry_run,omitempty"`
	Next           []string `json:"next"`
}

func runMCPConnectCI(opts mcpConnectCIOptions) int {
	if _, err := mcpConnectLookPath("gh"); err != nil {
		return printErr("GitHub CLI not found", errors.New("--ci stores the key with gh; install it from https://cli.github.com and run gh auth login"))
	}
	if out, err := mcpConnectRun("gh", "auth", "status"); err != nil {
		return printErr("GitHub CLI not signed in", fmt.Errorf("run gh auth login, then retry: %s", oneLine(out)))
	}
	if opts.Repo == "" {
		out, err := mcpConnectRun("gh", "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner")
		if err != nil {
			return printErr("No repository", errors.New("name one with --repo OWNER/NAME, or run inside a clone of it"))
		}
		opts.Repo = strings.TrimSpace(out)
	}
	if !ciRepoRe.MatchString(opts.Repo) {
		return printErr("Invalid --repo", fmt.Errorf("%q is not OWNER/NAME", opts.Repo))
	}
	if out, err := mcpConnectRun("gh", "secret", "list", "--repo", opts.Repo, "--json", "name"); err != nil {
		return printErr("Cannot read the repository's secrets", fmt.Errorf("%s needs admin access with gh: %s", opts.Repo, oneLine(out)))
	} else if ciSecretExists(out) && !opts.Force {
		return printErr("Secret already set", fmt.Errorf("%s already has %s; pass --force to mint a new key and replace it (revoke the old key with gregale mcp connection revoke)", opts.Repo, ciSecretName))
	}
	res := mcpConnectCIResult{Repo: opts.Repo, Secret: ciSecretName, Workflow: ciWorkflowPath, DryRun: opts.DryRun}
	workflow := ciWorkflow(opts.URL)
	exists := fileExists(ciWorkflowPath)
	switch {
	case exists && !opts.Force:
		res.WorkflowStatus = "kept"
	case opts.DryRun:
		res.WorkflowStatus = "would_write"
	}
	if opts.DryRun {
		return printMCPConnectCI(res, opts, workflow)
	}
	key, err := mintAgentKey(ciKeyLabel(opts.Repo), opts.Scopes, opts.Approvals)
	if err != nil {
		return printErr("Could not mint an agent key", err)
	}
	res.KeyID = key.ID
	if out, err := mcpConnectRunStdin(key.Plaintext, "gh", "secret", "set", ciSecretName, "--repo", opts.Repo); err != nil {
		return printErr("Could not store the key", fmt.Errorf("gh secret set failed (%s); revoke the unused key with gregale mcp connection revoke %s", oneLine(out), key.ID))
	}
	if res.WorkflowStatus == "" {
		if err := os.MkdirAll(filepath.Dir(ciWorkflowPath), 0o755); err != nil {
			return printErr("Could not write the workflow", err)
		}
		if err := writeConfigAtomic(ciWorkflowPath, []byte(workflow)); err != nil {
			return printErr("Could not write the workflow", err)
		}
		res.WorkflowStatus = "written"
	}
	return printMCPConnectCI(res, opts, workflow)
}

func ciSecretExists(listJSON string) bool {
	var secrets []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(listJSON), &secrets) != nil {
		return false
	}
	for _, s := range secrets {
		if s.Name == ciSecretName {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ciKeyLabel(repo string) string {
	label := "ci-" + strings.ReplaceAll(repo, "/", "-")
	if len(label) > 64 {
		label = label[:64]
	}
	return label
}

// mintAgentKey creates an agent key; nil approvals keeps the server's
// default of every kind.
func mintAgentKey(label string, scopes []string, approvals *[]string) (api.APIKeyResponse, error) {
	client, err := authedClient()
	if err != nil {
		return api.APIKeyResponse{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return client.CreateKeyWith(ctx, api.CreateKeyRequest{Label: label, Scopes: scopes, ApprovalCategories: approvals})
}

func printMCPConnectCI(res mcpConnectCIResult, opts mcpConnectCIOptions, workflow string) int {
	res.Next = []string{
		"gh secret set ANTHROPIC_API_KEY --repo " + res.Repo + "   # the workflow's agent is Claude Code; swap it for yours",
		"git add " + ciWorkflowPath + " && git commit -m 'Add Gregale agent workflow' && git push",
		"gh workflow run gregale-agent.yml --repo " + res.Repo + " -f task='List my apps and their status'",
	}
	if res.WorkflowStatus == "kept" {
		res.Next[1] = ciWorkflowPath + " already exists and was kept; --force replaces it"
	}
	if jsonOutput {
		return jsonOut(writeJSON(res))
	}
	if res.DryRun {
		_, _ = fmt.Fprintf(osStdout, "Would mint an agent key (%s), store it as %s in %s, and write %s:\n\n%s\n", strings.Join(opts.Scopes, ","), ciSecretName, res.Repo, ciWorkflowPath, workflow)
		_, _ = fmt.Fprintln(osStdout, "Dry run: nothing was minted or written.")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "Agent key %s minted and stored as %s in %s; it is not shown.\n", res.KeyID, ciSecretName, res.Repo)
	switch res.WorkflowStatus {
	case "written":
		_, _ = fmt.Fprintf(osStdout, "Wrote %s.\n", ciWorkflowPath)
	case "kept":
		_, _ = fmt.Fprintf(osStdout, "Kept the existing %s.\n", ciWorkflowPath)
	}
	_, _ = fmt.Fprintln(osStdout, "Next:")
	for _, n := range res.Next {
		_, _ = fmt.Fprintln(osStdout, "  "+n)
	}
	return 0
}

// ciWorkflow is the GitHub Actions workflow: run on demand with a task, check
// the key opens an MCP session, then give Claude Code Gregale's tools.
func ciWorkflow(url string) string {
	return strings.ReplaceAll(`# Written by gregale mcp connect --ci. An agent works on Gregale with the
# agent key in the GREGALE_AGENT_KEY secret. Risky requests (deletes,
# production changes, credentials, billing) wait for a person to approve
# them in Gregale; the agent prints the review link.
name: Gregale agent

on:
  workflow_dispatch:
    inputs:
      task:
        description: What the agent should do on Gregale
        required: true

permissions:
  contents: read

jobs:
  agent:
    runs-on: ubuntu-latest
    timeout-minutes: 30
    env:
      GREGALE_AGENT_KEY: ${{ secrets.GREGALE_AGENT_KEY }}
      GREGALE_MCP_URL: {{URL}}
    steps:
      - uses: actions/checkout@v4

      - name: Check the agent key
        run: |
          status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$GREGALE_MCP_URL" \
            -H "Authorization: Bearer $GREGALE_AGENT_KEY" \
            -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
            -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"gregale-ci","version":"1"}}}')
          if [ "$status" != 200 ]; then
            echo "::error::Gregale refused the agent key (HTTP $status); run gregale mcp connect --ci --force"
            exit 1
          fi

      - name: Run the agent
        env:
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
          TASK: ${{ inputs.task }}
        run: |
          npm install -g @anthropic-ai/claude-code
          cat > "$RUNNER_TEMP/mcp.json" <<'JSON'
          {"mcpServers": {"gregale": {"type": "http", "url": "{{URL}}",
            "headers": {"Authorization": "Bearer ${GREGALE_AGENT_KEY}"}}}}
          JSON
          claude -p "$TASK" --mcp-config "$RUNNER_TEMP/mcp.json" --allowedTools "mcp__gregale"
`, "{{URL}}", url)
}
