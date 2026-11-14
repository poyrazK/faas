package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
)

// Agent clients whose MCP config is a JSON file Gregale edits: Cursor,
// VS Code and Windsurf (ADR-20261109121411 §7). Only the gregale entry
// changes; every other key keeps its value and order. A file that is not
// plain JSON (comments, trailing commas) is refused, never rewritten.
// Claude Desktop adds remote servers only in its own settings, so connect
// prints those steps instead.

const (
	mcpConnectCursor        = "cursor"
	mcpConnectVSCode        = "vscode"
	mcpConnectWindsurf      = "windsurf"
	mcpConnectClaudeDesktop = "claude-desktop"
)

// mcpJSONClient describes one JSON-configured client.
type mcpJSONClient struct {
	name     string
	binary   string                   // on PATH when installed
	userDir  func(home string) string // exists when installed
	userFile func(home string) string // user-wide config
	project  string                   // project config, "" for none
	root     string                   // object holding the servers
	entry    func(url string) map[string]any
	// keyRef is how the client reads the agent key from the environment.
	keyRef string
}

// mcpEnvKeyRef is how VS Code and Windsurf read an environment variable
// inside their MCP config. Cursor's agent expands ${NAME} and sends
// ${env:NAME} literally (cursor-agent 2026.04.17), so Cursor gets
// mcpPlainKeyRef.
const (
	mcpEnvKeyRef   = "${env:" + agentKeyEnv + "}"
	mcpPlainKeyRef = "${" + agentKeyEnv + "}"
)

var mcpJSONClients = map[string]mcpJSONClient{
	mcpConnectCursor: {
		name: "Cursor", binary: "cursor",
		userDir:  func(home string) string { return filepath.Join(home, ".cursor") },
		userFile: func(home string) string { return filepath.Join(home, ".cursor", "mcp.json") },
		project:  filepath.Join(".cursor", "mcp.json"),
		root:     "mcpServers",
		entry:    func(url string) map[string]any { return map[string]any{"url": url} },
		keyRef:   mcpPlainKeyRef,
	},
	mcpConnectVSCode: {
		name: "VS Code", binary: "code",
		userDir:  vscodeUserDir,
		userFile: func(home string) string { return filepath.Join(vscodeUserDir(home), "mcp.json") },
		project:  filepath.Join(".vscode", "mcp.json"),
		root:     "servers",
		entry:    func(url string) map[string]any { return map[string]any{"type": "http", "url": url} },
	},
	mcpConnectWindsurf: {
		name: "Windsurf", binary: "windsurf",
		userDir:  func(home string) string { return filepath.Join(home, ".codeium", "windsurf") },
		userFile: func(home string) string { return filepath.Join(home, ".codeium", "windsurf", "mcp_config.json") },
		root:     "mcpServers",
		entry:    func(url string) map[string]any { return map[string]any{"serverUrl": url} },
	},
}

// vscodeUserDir is VS Code's user settings directory.
func vscodeUserDir(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Code", "User")
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "Code", "User")
		}
		return filepath.Join(home, "AppData", "Roaming", "Code", "User")
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "Code", "User")
	}
	return filepath.Join(home, ".config", "Code", "User")
}

// claudeDesktopDir is Claude Desktop's settings directory.
func claudeDesktopDir(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude")
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "Claude")
		}
		return filepath.Join(home, "AppData", "Roaming", "Claude")
	}
	return filepath.Join(home, ".config", "Claude")
}

func mcpDirExists(dir func(string) string) bool {
	home, err := mcpConnectHome()
	if err != nil {
		return false
	}
	info, err := os.Stat(dir(home))
	return err == nil && info.IsDir()
}

// mcpJSONEntry is the gregale server entry a client gets.
func mcpJSONEntry(c mcpJSONClient, opts mcpConnectOptions) map[string]any {
	entry := c.entry(opts.URL)
	headers := map[string]any{}
	keyRef := c.keyRef
	if keyRef == "" {
		keyRef = mcpEnvKeyRef
	}
	for _, h := range mcpConnectHeaders(opts, keyRef) {
		k, v, _ := strings.Cut(h, ": ")
		headers[k] = v
	}
	if len(headers) > 0 {
		entry["headers"] = headers
	}
	return entry
}

func connectJSONClient(client string, opts mcpConnectOptions) mcpConnectResult {
	c := mcpJSONClients[client]
	path := c.project
	if opts.Project {
		if path == "" {
			return mcpConnectResult{Status: "skipped", Detail: c.name + " has no project config; run without --project to connect it user-wide"}
		}
	} else {
		home, err := mcpConnectHome()
		if err != nil {
			return mcpConnectResult{Status: "failed", Detail: "home directory: " + err.Error()}
		}
		path = c.userFile(home)
	}
	entry := mcpJSONEntry(c, opts)
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return mcpConnectResult{Status: "failed", Config: path, Detail: err.Error()}
	}
	updated, state, err := upsertJSONServer(current, c.root, entry, opts.Force)
	if err != nil {
		snippet, _ := json.Marshal(map[string]any{c.root: map[string]any{mcpServerName: entry}})
		return mcpConnectResult{Status: "failed", Config: path, Detail: path + " is not plain JSON (" + err.Error() + "); add this yourself: " + string(snippet)}
	}
	next := jsonClientNext(c, opts)
	switch state {
	case codexSame:
		return mcpConnectResult{Status: "already_connected", Config: path, Checked: true, Next: next}
	case codexConflict:
		return mcpConnectResult{Status: "conflict", Config: path, Detail: c.name + " already has a different gregale server; pass --force to replace it"}
	}
	if opts.DryRun {
		block, _ := json.MarshalIndent(map[string]any{mcpServerName: entry}, "", "  ")
		return mcpConnectResult{Status: "would_connect", Config: path, Detail: string(block)}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return mcpConnectResult{Status: "failed", Config: path, Detail: err.Error()}
	}
	if err := writeConfigAtomic(path, updated); err != nil {
		return mcpConnectResult{Status: "failed", Config: path, Detail: err.Error()}
	}
	back, _ := os.ReadFile(path)
	got, _ := jsonServerEntry(back, c.root)
	return mcpConnectResult{Status: "connected", Config: path, Checked: sameJSON(got, entry), Next: next}
}

func jsonClientNext(c mcpJSONClient, opts mcpConnectOptions) string {
	if opts.Headless {
		return "export " + agentKeyEnv + " before starting " + c.name
	}
	return "open " + c.name + "'s MCP settings and start gregale; your browser opens to sign in to Gregale"
}

// connectClaudeDesktop prints the steps: Claude Desktop adds a remote MCP
// server only in its own settings.
func connectClaudeDesktop(opts mcpConnectOptions) mcpConnectResult {
	r := mcpConnectResult{Status: "manual",
		Detail: "In Claude Desktop open Settings → Connectors → Add custom connector, name it Gregale, enter " + opts.URL + ", then Connect and sign in to Gregale in the browser.",
		Next:   "add the connector in Claude Desktop's settings"}
	if opts.Headless {
		r.Detail += " Connectors sign in through the browser; an agent key cannot be used there."
	}
	return r
}

// --- an order-keeping JSON object -------------------------------------------

type jsonMember struct {
	key   string
	value json.RawMessage
}

// parseJSONObject reads a JSON object keeping its keys in order. Empty
// input is an empty object.
func parseJSONObject(data []byte) ([]jsonMember, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	var members []jsonMember
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		members = append(members, jsonMember{key: key, value: value})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("trailing data after the JSON object")
	}
	if _, err := dec.Token(); err == nil {
		return nil, errors.New("trailing data after the JSON object")
	}
	return members, nil
}

func encodeJSONObject(members []jsonMember) []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(m.key)
		b.Write(key)
		b.WriteByte(':')
		b.Write(m.value)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func jsonServerEntry(data []byte, root string) (json.RawMessage, error) {
	top, err := parseJSONObject(data)
	if err != nil {
		return nil, err
	}
	for _, m := range top {
		if m.key != root {
			continue
		}
		servers, err := parseJSONObject(m.value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", root, err)
		}
		for _, s := range servers {
			if s.key == mcpServerName {
				return s.value, nil
			}
		}
	}
	return nil, nil
}

func sameJSON(raw json.RawMessage, want map[string]any) bool {
	if raw == nil {
		return false
	}
	var got any
	if json.Unmarshal(raw, &got) != nil {
		return false
	}
	wantRaw, _ := json.Marshal(want)
	var w any
	_ = json.Unmarshal(wantRaw, &w)
	return reflect.DeepEqual(got, w)
}

// upsertJSONServer adds the gregale entry under root, or with force
// replaces a different one. Other keys keep their values and order.
func upsertJSONServer(content []byte, root string, entry map[string]any, force bool) ([]byte, string, error) {
	top, err := parseJSONObject(content)
	if err != nil {
		return nil, "", err
	}
	value, _ := json.Marshal(entry)
	state := codexAdded
	rootAt := -1
	for i, m := range top {
		if m.key == root {
			rootAt = i
		}
	}
	if rootAt < 0 {
		top = append(top, jsonMember{key: root, value: encodeJSONObject([]jsonMember{{key: mcpServerName, value: value}})})
	} else {
		servers, err := parseJSONObject(top[rootAt].value)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", root, err)
		}
		found := false
		for i, s := range servers {
			if s.key != mcpServerName {
				continue
			}
			found = true
			if sameJSON(s.value, entry) {
				return content, codexSame, nil
			}
			if !force {
				return content, codexConflict, nil
			}
			servers[i].value, state = value, codexReplaced
		}
		if !found {
			servers = append(servers, jsonMember{key: mcpServerName, value: value})
		}
		top[rootAt].value = encodeJSONObject(servers)
	}
	var out bytes.Buffer
	if err := json.Indent(&out, encodeJSONObject(top), "", "  "); err != nil {
		return nil, "", err
	}
	out.WriteByte('\n')
	return out.Bytes(), state, nil
}

// mcpDoctorJSONClient checks a JSON client's gregale entry: the user config,
// else this directory's project config.
func mcpDoctorJSONClient(client, url string) (mcpDoctorCheck, bool) {
	c := mcpJSONClients[client]
	check := mcpDoctorCheck{Check: client}
	home, err := mcpConnectHome()
	if err != nil {
		check.Status, check.Detail = "fail", "home directory: "+err.Error()
		return check, false
	}
	paths := []string{c.userFile(home)}
	if c.project != "" {
		paths = append(paths, c.project)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			check.Status, check.Detail = "fail", err.Error()
			return check, false
		}
		raw, err := jsonServerEntry(data, c.root)
		if err != nil {
			check.Status, check.Detail = "warn", path+" is not plain JSON: "+err.Error()
			return check, false
		}
		if raw == nil {
			continue
		}
		usesKey := strings.Contains(string(raw), agentKeyEnv)
		var entry struct {
			URL       string `json:"url"`
			ServerURL string `json:"serverUrl"`
		}
		_ = json.Unmarshal(raw, &entry)
		got := entry.URL + entry.ServerURL
		if got != url {
			check.Status, check.Detail, check.Fix = "warn", c.name+"'s gregale server in "+path+" is "+got+", not "+url, "gregale mcp connect --client "+client+" --force"
			return check, usesKey
		}
		check.Status, check.Detail = "ok", c.name+" connects to "+url+" ("+path+")"
		if usesKey {
			check.Detail += " with the agent key in $" + agentKeyEnv
		}
		return check, usesKey
	}
	check.Status, check.Detail, check.Fix = "warn", c.name+" has no gregale server", "gregale mcp connect --client "+client
	return check, false
}
