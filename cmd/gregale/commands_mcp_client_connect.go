package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// gregale mcp connect writes the hosted Gregale MCP server into the user's
// agent clients and checks the connection (ADR-20261109121411).

const (
	mcpConnectClaude = "claude-code"
	mcpConnectCodex  = "codex"
	mcpServerName    = "gregale"
	// agentKeyEnv names the environment variable a headless config reads
	// the agent key from; the key itself is never written to a file.
	agentKeyEnv = "GREGALE_AGENT_KEY"
)

var mcpConnectClients = []string{mcpConnectClaude, mcpConnectCodex, mcpConnectCursor, mcpConnectVSCode, mcpConnectWindsurf, mcpConnectClaudeDesktop}

// Seams for tests: external commands, PATH lookup and the home directory.
var (
	mcpConnectLookPath = exec.LookPath
	mcpConnectRun      = func(name string, args ...string) (string, error) {
		out, err := exec.Command(name, args...).CombinedOutput() //nolint:gosec // fixed client binaries and arguments
		return string(out), err
	}
	mcpConnectInteractive = func(name string, args ...string) error {
		cmd := exec.Command(name, args...) //nolint:gosec // fixed client binaries and arguments
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stderr, os.Stderr
		return cmd.Run()
	}
	mcpConnectHome = os.UserHomeDir
)

// mcpConnectOptions is what one run writes.
type mcpConnectOptions struct {
	URL      string
	Toolsets string
	Project  bool
	Headless bool
	Force    bool
	DryRun   bool
}

// mcpConnectResult reports one client.
type mcpConnectResult struct {
	Client  string `json:"client"`
	Status  string `json:"status"` // connected, already_connected, would_connect, conflict, failed, skipped
	Config  string `json:"config,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Checked bool   `json:"checked"`
	Next    string `json:"next,omitempty"`
}

type mcpConnectSummary struct {
	URL      string             `json:"url"`
	Endpoint string             `json:"endpoint_check"`
	KeyID    string             `json:"agent_key_id,omitempty"`
	Tools    int                `json:"tools,omitempty"`
	Clients  []mcpConnectResult `json:"clients"`
}

const mcpConnectUsage = "usage: gregale mcp connect [--client claude-code|codex|cursor|vscode|windsurf|claude-desktop]... [--project] [--toolsets LIST] [--headless] [--force] [--dry-run] [--yes] | --check"

func cmdMCPConnect(args []string) int {
	fs := newFlagSet("mcp connect", flag.ContinueOnError)
	var clients stringListFlag
	fs.Var(&clients, "client", "agent client to connect: "+strings.Join(mcpConnectClients, ", ")+" (repeatable; default: every detected one)")
	project := fs.Bool("project", false, "write this project's config (.mcp.json) instead of the user-wide one")
	toolsets := fs.String("toolsets", "", "MCP toolsets to list, comma-separated (default core)")
	headless := fs.Bool("headless", false, "mint an agent key for an agent with no person at a browser; the config reads it from $"+agentKeyEnv)
	force := fs.Bool("force", false, "replace an existing gregale entry that differs")
	dryRun := fs.Bool("dry-run", false, "print what would be written without changing anything")
	yes := fs.Bool("yes", false, "connect every detected client without asking")
	check := fs.Bool("check", false, "only check why an agent does not see Gregale; changes nothing")
	flags, positional := splitArgsForFlags(args, "project", "headless", "force", "dry-run", "yes", "check")
	if err := fs.Parse(flags); err != nil || len(positional) != 0 {
		PrintUsage(os.Stderr, mcpConnectUsage, "mcp")
		return 1
	}
	if *check {
		return runMCPConnectCheck()
	}
	opts := mcpConnectOptions{URL: strings.TrimRight(apiBase(), "/") + "/mcp", Toolsets: strings.TrimSpace(*toolsets),
		Project: *project, Headless: *headless, Force: *force, DryRun: *dryRun}
	var named []string
	for _, c := range clients {
		for _, part := range strings.Split(c, ",") {
			if part = strings.TrimSpace(part); part != "" {
				named = append(named, part)
			}
		}
	}
	selected, code := mcpConnectSelect(named, *yes || nonInteractive || jsonOutput)
	if code != 0 || len(selected) == 0 {
		return code
	}
	summary := mcpConnectSummary{URL: opts.URL, Endpoint: mcpEndpointCheck(opts.URL)}
	var key string
	if opts.Headless && !opts.DryRun {
		minted, err := mintConnectAgentKey(selected)
		if err != nil {
			return printErr("Could not mint an agent key", err)
		}
		key, summary.KeyID = minted.Plaintext, minted.ID
	}
	for _, client := range selected {
		summary.Clients = append(summary.Clients, mcpConnectClient(client, opts))
	}
	if key != "" {
		summary.Tools = mcpHeadlessToolCount(opts, key)
	}
	return printMCPConnectSummary(summary, key, opts)
}

// mcpConnectSelect resolves the clients to connect: the named ones, or the
// detected ones, confirmed one by one in a terminal unless auto is set.
func mcpConnectSelect(named []string, auto bool) ([]string, int) {
	for _, c := range named {
		if !slices.Contains(mcpConnectClients, c) {
			return nil, printErr("Unknown agent client", fmt.Errorf("%q is not one of %s", c, strings.Join(mcpConnectClients, ", ")))
		}
	}
	if len(named) != 0 {
		return named, 0
	}
	var detected []string
	for _, c := range mcpConnectClients {
		if mcpClientDetected(c) {
			detected = append(detected, c)
		}
	}
	if len(detected) == 0 {
		return nil, printErr("No agent client found", fmt.Errorf("found none of Claude Code, Codex, Cursor, VS Code, Windsurf or Claude Desktop; install one, or see https://gregale.dev/docs/agents for other clients"))
	}
	if auto {
		return detected, 0
	}
	if !stdinIsTTY() {
		return nil, printErr("Confirmation required", fmt.Errorf("stdin is not a terminal; pass --yes or --client to choose the clients"))
	}
	reader := bufio.NewReader(osStdin)
	var chosen []string
	for _, c := range detected {
		_, _ = fmt.Fprintf(osStderr, "Connect %s to Gregale? [Y/n] ", mcpClientName(c))
		line, _ := reader.ReadString('\n')
		if answer := strings.ToLower(strings.TrimSpace(line)); answer == "" || answer == "y" || answer == "yes" {
			chosen = append(chosen, c)
		}
	}
	return chosen, 0
}

func mcpClientDetected(client string) bool {
	switch client {
	case mcpConnectClaude:
		_, err := mcpConnectLookPath("claude")
		return err == nil
	case mcpConnectCodex:
		if _, err := mcpConnectLookPath("codex"); err == nil {
			return true
		}
		home, err := mcpConnectHome()
		if err != nil {
			return false
		}
		_, err = os.Stat(filepath.Join(home, ".codex"))
		return err == nil
	case mcpConnectClaudeDesktop:
		return mcpDirExists(claudeDesktopDir)
	}
	if c, ok := mcpJSONClients[client]; ok {
		if _, err := mcpConnectLookPath(c.binary); err == nil {
			return true
		}
		return mcpDirExists(c.userDir)
	}
	return false
}

func mcpClientName(client string) string {
	switch client {
	case mcpConnectClaude:
		return "Claude Code"
	case mcpConnectCodex:
		return "Codex"
	case mcpConnectClaudeDesktop:
		return "Claude Desktop"
	}
	if c, ok := mcpJSONClients[client]; ok {
		return c.name
	}
	return client
}

func mcpConnectClient(client string, opts mcpConnectOptions) mcpConnectResult {
	var r mcpConnectResult
	switch client {
	case mcpConnectClaude:
		r = connectClaudeCode(opts)
	case mcpConnectCodex:
		r = connectCodex(opts)
	case mcpConnectClaudeDesktop:
		r = connectClaudeDesktop(opts)
	default:
		r = connectJSONClient(client, opts)
	}
	r.Client = client
	return r
}

// --- Claude Code ------------------------------------------------------------

func connectClaudeCode(opts mcpConnectOptions) mcpConnectResult {
	scope := "user"
	if opts.Project {
		scope = "project"
	}
	add := []string{"mcp", "add", "--transport", "http", "--scope", scope, mcpServerName, opts.URL}
	for _, h := range mcpConnectHeaders(opts, "${"+agentKeyEnv+"}") {
		add = append(add, "--header", h)
	}
	config := "~/.claude.json"
	if opts.Project {
		config = ".mcp.json"
	}
	if _, err := mcpConnectLookPath("claude"); err != nil {
		return mcpConnectResult{Status: "failed", Config: config, Detail: "the claude command is not on PATH; install Claude Code, or run: claude " + strings.Join(add, " ")}
	}
	existing, err := mcpConnectRun("claude", "mcp", "get", mcpServerName)
	if err == nil {
		if strings.Contains(existing, opts.URL) && !opts.Force {
			return mcpConnectResult{Status: "already_connected", Config: config, Checked: true, Next: claudeNext(opts)}
		}
		if !opts.Force {
			return mcpConnectResult{Status: "conflict", Config: config, Detail: "Claude Code already has a different gregale server; pass --force to replace it"}
		}
	}
	if opts.DryRun {
		return mcpConnectResult{Status: "would_connect", Config: config, Detail: "claude " + strings.Join(add, " ")}
	}
	if err == nil {
		if out, rmErr := mcpConnectRun("claude", "mcp", "remove", mcpServerName, "--scope", scope); rmErr != nil {
			return mcpConnectResult{Status: "failed", Config: config, Detail: "claude mcp remove: " + oneLine(out)}
		}
	}
	if out, err := mcpConnectRun("claude", add...); err != nil {
		return mcpConnectResult{Status: "failed", Config: config, Detail: "claude mcp add: " + oneLine(out)}
	}
	_, getErr := mcpConnectRun("claude", "mcp", "get", mcpServerName)
	return mcpConnectResult{Status: "connected", Config: config, Checked: getErr == nil, Next: claudeNext(opts)}
}

func claudeNext(opts mcpConnectOptions) string {
	if opts.Headless {
		return "export " + agentKeyEnv + " before starting Claude Code"
	}
	return "start Claude Code and run /mcp; your browser opens to sign in to Gregale"
}

// --- Codex ------------------------------------------------------------------

func connectCodex(opts mcpConnectOptions) mcpConnectResult {
	if opts.Project {
		return mcpConnectResult{Status: "skipped", Detail: "Codex has no project config; run without --project to connect it user-wide"}
	}
	home, err := mcpConnectHome()
	if err != nil {
		return mcpConnectResult{Status: "failed", Detail: "home directory: " + err.Error()}
	}
	path := filepath.Join(home, ".codex", "config.toml")
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return mcpConnectResult{Status: "failed", Config: path, Detail: err.Error()}
	}
	block := codexServerBlock(opts)
	updated, state := upsertCodexServer(string(current), block, opts.Force)
	switch state {
	case codexSame:
		return mcpConnectResult{Status: "already_connected", Config: path, Checked: true, Next: codexNext(opts)}
	case codexConflict:
		return mcpConnectResult{Status: "conflict", Config: path, Detail: "Codex already has a different [mcp_servers.gregale]; pass --force to replace it"}
	}
	if opts.DryRun {
		return mcpConnectResult{Status: "would_connect", Config: path, Detail: strings.TrimSpace(block)}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return mcpConnectResult{Status: "failed", Config: path, Detail: err.Error()}
	}
	if err := writeConfigAtomic(path, []byte(updated)); err != nil {
		return mcpConnectResult{Status: "failed", Config: path, Detail: err.Error()}
	}
	back, _ := os.ReadFile(path)
	r := mcpConnectResult{Status: "connected", Config: path, Checked: strings.Contains(string(back), strings.TrimSpace(block)), Next: codexNext(opts)}
	if !opts.Headless && !jsonOutput && !nonInteractive && stdinIsTTY() {
		if _, err := mcpConnectLookPath("codex"); err == nil {
			_, _ = fmt.Fprintln(osStderr, "Signing Codex in to Gregale (codex mcp login gregale)...")
			if err := mcpConnectInteractive("codex", "mcp", "login", mcpServerName); err == nil {
				r.Next = "start Codex; Gregale's tools are ready"
			}
		}
	}
	return r
}

func codexNext(opts mcpConnectOptions) string {
	if opts.Headless {
		return "export " + agentKeyEnv + " before starting Codex"
	}
	return "run: codex mcp login gregale"
}

func codexServerBlock(opts mcpConnectOptions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[mcp_servers.%s]\nurl = %q\n", mcpServerName, opts.URL)
	if opts.Toolsets != "" {
		fmt.Fprintf(&b, "http_headers = { \"Gregale-MCP-Toolsets\" = %q }\n", opts.Toolsets)
	}
	if opts.Headless {
		fmt.Fprintf(&b, "bearer_token_env_var = %q\n", agentKeyEnv)
	}
	return b.String()
}

const (
	codexAdded    = "added"
	codexSame     = "same"
	codexConflict = "conflict"
	codexReplaced = "replaced"
)

// upsertCodexServer adds the gregale server table to a Codex config, or with
// force replaces it. It edits only that table (and its subtables); every
// other line, comment and table stays byte for byte.
func upsertCodexServer(content, block string, force bool) (string, string) {
	lines := strings.SplitAfter(content, "\n")
	start, end := codexServerSpan(lines)
	if start < 0 {
		sep := ""
		if content != "" {
			if !strings.HasSuffix(content, "\n") {
				sep = "\n"
			}
			sep += "\n"
		}
		return content + sep + block, codexAdded
	}
	existing := strings.Join(lines[start:end], "")
	if strings.TrimSpace(existing) == strings.TrimSpace(block) {
		return content, codexSame
	}
	if !force {
		return content, codexConflict
	}
	tail := strings.Join(lines[end:], "")
	if tail != "" {
		block += "\n"
	}
	return strings.Join(lines[:start], "") + block + tail, codexReplaced
}

// codexServerSpan finds the lines of the gregale server table and its
// subtables: [start, end), or start -1 when there is none.
func codexServerSpan(lines []string) (start, end int) {
	start, end = -1, len(lines)
	for i, line := range lines {
		header := codexTableHeader(line)
		if header == "" {
			continue
		}
		owned := header == "mcp_servers."+mcpServerName || strings.HasPrefix(header, "mcp_servers."+mcpServerName+".")
		if start < 0 && owned {
			start = i
			continue
		}
		if start >= 0 && !owned {
			return start, i
		}
	}
	return start, end
}

// codexServerTable returns the gregale server table of a Codex config, or "".
func codexServerTable(content string) string {
	lines := strings.SplitAfter(content, "\n")
	start, end := codexServerSpan(lines)
	if start < 0 {
		return ""
	}
	return strings.Join(lines[start:end], "")
}

// codexTableHeader returns the dotted name of a TOML table header line, or
// "" for any other line. Quotes around key parts are dropped.
func codexTableHeader(line string) string {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "[") || strings.HasPrefix(t, "[[") {
		return ""
	}
	closing := strings.Index(t, "]")
	if closing < 0 {
		return ""
	}
	return strings.ReplaceAll(strings.ReplaceAll(t[1:closing], `"`, ""), " ", "")
}

// --- shared -----------------------------------------------------------------

// mcpConnectHeaders are the HTTP headers a client sends: the toolset
// selection, and for headless the agent key read from the environment.
func mcpConnectHeaders(opts mcpConnectOptions, keyRef string) []string {
	var h []string
	if opts.Toolsets != "" {
		h = append(h, "Gregale-MCP-Toolsets: "+opts.Toolsets)
	}
	if opts.Headless {
		h = append(h, "Authorization: Bearer "+keyRef)
	}
	return h
}

func mintConnectAgentKey(clients []string) (api.APIKeyResponse, error) {
	client, err := authedClient()
	if err != nil {
		return api.APIKeyResponse{}, err
	}
	host, _ := os.Hostname()
	label := "mcp-" + strings.Join(clients, "-")
	if host != "" {
		label += "-" + strings.ToLower(strings.SplitN(host, ".", 2)[0])
	}
	if len(label) > 64 {
		label = label[:64]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return client.CreateKeyWith(ctx, api.CreateKeyRequest{Label: label, Scopes: []string{api.ScopeAgent, api.ScopeDeployWrite}})
}

func printMCPConnectSummary(s mcpConnectSummary, key string, opts mcpConnectOptions) int {
	failed := slices.ContainsFunc(s.Clients, func(r mcpConnectResult) bool { return r.Status == "failed" || r.Status == "conflict" })
	if jsonOutput {
		if code := jsonOut(writeJSON(s)); code != 0 {
			return code
		}
		if key != "" {
			_, _ = fmt.Fprintf(osStderr, "%s=%s\n", agentKeyEnv, key)
		}
		if failed {
			return 1
		}
		return 0
	}
	for _, r := range s.Clients {
		line := fmt.Sprintf("%s: %s", mcpClientName(r.Client), strings.ReplaceAll(r.Status, "_", " "))
		if r.Config != "" {
			line += " (" + r.Config + ")"
		}
		_, _ = fmt.Fprintln(osStdout, line)
		if r.Detail != "" {
			_, _ = fmt.Fprintln(osStdout, "  "+strings.ReplaceAll(r.Detail, "\n", "\n  "))
		}
		if r.Next != "" && r.Status != "failed" && r.Status != "conflict" {
			_, _ = fmt.Fprintln(osStdout, "  Next: "+r.Next)
		}
	}
	_, _ = fmt.Fprintf(osStdout, "Server: %s (%s)\n", s.URL, s.Endpoint)
	if key != "" {
		_, _ = fmt.Fprintf(osStdout, "Agent key %s minted; it is shown once. Store it where your agent runs:\n  export %s=%s\n", s.KeyID, agentKeyEnv, key)
		if s.Tools > 0 {
			_, _ = fmt.Fprintf(osStdout, "The key opened an MCP session with %d tools.\n", s.Tools)
		}
	}
	if opts.DryRun {
		_, _ = fmt.Fprintln(osStdout, "Dry run: nothing was written.")
	}
	if failed {
		return 1
	}
	return 0
}

// writeConfigAtomic replaces a client config through a temporary file in the
// same directory, so a crash never leaves it half written. An existing
// file keeps its mode; a new one is private to the user.
func writeConfigAtomic(path string, data []byte) error {
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gregale-config-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// mcpEndpointCheck asks the hosted server, without credentials, to start a
// session: a 401 means it is up and asks the agent to sign in.
var mcpEndpointCheck = func(url string) string {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"gregale-mcp-connect","version":"1"}}}`)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "unreachable: " + err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "unreachable: " + oneLine(err.Error())
	}
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return "reachable, asks the agent to sign in"
	case resp.StatusCode < 300:
		return "reachable"
	default:
		return fmt.Sprintf("answered HTTP %d", resp.StatusCode)
	}
}

// mcpHeadlessToolCount opens an MCP session with the new agent key, as the
// agent will, and counts the tools it lists; 0 when that fails.
var mcpHeadlessToolCount = func(opts mcpConnectOptions, key string) int {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+key)
	if opts.Toolsets != "" {
		headers.Set("Gregale-MCP-Toolsets", opts.Toolsets)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	transport := &mcp.StreamableClientTransport{Endpoint: opts.URL, HTTPClient: &http.Client{Transport: headerTransport{headers: headers, base: http.DefaultTransport}}}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "gregale-mcp-connect", Version: "1"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		return 0
	}
	defer func() { _ = session.Close() }()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		return 0
	}
	return len(tools.Tools)
}

type headerTransport struct {
	headers http.Header
	base    http.RoundTripper
}

func (t headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range t.headers {
		r.Header[k] = v
	}
	return t.base.RoundTrip(r)
}
