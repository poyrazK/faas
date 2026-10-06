package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

func TestMCPContractCLIJourney(t *testing.T) {
	oldOut, oldJSON := osStdout, jsonOutput
	var output bytes.Buffer
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	var candidate atomic.Bool
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer client-token-only" {
			t.Error("wrong client credential")
		}
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.Method == "server/discover" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":["%s"],"capabilities":{"tools":{}}}}`, request.ID, mcphosting.ProtocolVersion)
			return
		}
		if request.Method != "tools/list" {
			calls.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		required := ""
		if candidate.Load() {
			required = `,"required":["id"]`
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"lookup","inputSchema":{"type":"object"%s}}]}}`, request.ID, required)
	}))
	defer s.Close()
	t.Setenv("GREG_TEST_MCP_CLIENT_TOKEN", "client-token-only")
	dir := t.TempDir()
	before := filepath.Join(dir, "baseline.json")
	after := filepath.Join(dir, "candidate.json")
	lock := func(path string) int {
		output.Reset()
		return cmdMCP([]string{"lock", "--url", s.URL + "/mcp", "--token-env", "GREG_TEST_MCP_CLIENT_TOKEN", "--out", path})
	}
	if code := lock(before); code != 0 {
		t.Fatalf("baseline exit=%d: %s", code, output.String())
	}
	first, err := os.ReadFile(before)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(first, []byte("client-token-only")) || bytes.Contains(first, []byte(s.URL)) {
		t.Fatal("snapshot contains credential or endpoint")
	}
	candidate.Store(true)
	if code := lock(before); code == 0 {
		t.Fatal("snapshot overwritten without force")
	}
	still, _ := os.ReadFile(before)
	if !bytes.Equal(first, still) {
		t.Fatal("failed lock changed baseline")
	}
	if code := lock(after); code != 0 {
		t.Fatalf("candidate exit=%d", code)
	}
	for _, check := range []bool{false, true} {
		output.Reset()
		args := []string{"diff", "--before", before, "--after", after}
		if check {
			args = append(args, "--check")
		}
		code := cmdMCP(args)
		want := 0
		if check {
			want = 1
		}
		var d mcphosting.ContractDiff
		if err := json.Unmarshal(output.Bytes(), &d); err != nil || code != want || !d.Breaking || d.Compatible {
			t.Fatalf("diff exit=%d receipt=%s err=%v", code, output.String(), err)
		}
	}
	output.Reset()
	if code := cmdMCP([]string{"diff", "--before", before, "--after", before, "--check"}); code != 0 {
		t.Fatalf("unchanged exit=%d", code)
	}
	if calls.Load() != 0 {
		t.Fatalf("contract commands invoked %d tools", calls.Load())
	}
}

func TestMCPResourcePromptOnlyServerJourneyAndPrivateSnapshot(t *testing.T) {
	oldOut, oldJSON := osStdout, jsonOutput
	var output bytes.Buffer
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	var reads, renders, toolCalls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var request struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		respond := func(body string) { _, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, request.ID, body) }
		switch request.Method {
		case "server/discover":
			respond(fmt.Sprintf(`{"supportedVersions":[%q],"capabilities":{"resources":{},"prompts":{}}}`, mcphosting.ProtocolVersion))
		case "resources/list":
			respond(`{"resources":[{"uri":"file:///reports/latest","name":"latest","description":"Current report"}]}`)
		case "resources/templates/list":
			respond(`{"resourceTemplates":[]}`)
		case "prompts/list":
			respond(`{"prompts":[{"name":"summarize","description":"Summarize a report","arguments":[{"name":"period","required":true}]}]}`)
		case "resources/read":
			reads.Add(1)
			respond(`{"contents":[{"uri":"file:///reports/latest","text":"PRIVATE_RESOURCE_BODY"}]}`)
		case "prompts/get":
			renders.Add(1)
			respond(`{"messages":[{"role":"user","content":{"type":"text","text":"PRIVATE_RENDERED_PROMPT"}}]}`)
		case "tools/list", "tools/call":
			toolCalls.Add(1)
			respond(`{"tools":[]}`)
		default:
			t.Errorf("unexpected method %q", request.Method)
			respond(`{}`)
		}
	}))
	defer s.Close()
	commands := [][]string{
		{"doctor", "--url", s.URL + "/mcp"},
		{"resources", "--url", s.URL + "/mcp"},
		{"prompts", "--url", s.URL + "/mcp"},
	}
	lock := filepath.Join(t.TempDir(), "catalog.json")
	commands = append(commands, []string{"lock", "--url", s.URL + "/mcp", "--out", lock})
	for _, command := range commands {
		output.Reset()
		if code := cmdMCP(command); code != 0 {
			t.Fatalf("%v exit=%d: %s", command, code, output.String())
		}
		if strings.Contains(output.String(), "PRIVATE_RESOURCE_BODY") || strings.Contains(output.String(), "PRIVATE_RENDERED_PROMPT") {
			t.Fatalf("non-explicit command exposed private content: %v: %s", command, output.String())
		}
	}
	data, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}
	var contract mcphosting.Contract
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Version != 2 || len(contract.Capabilities) != 2 || contract.Capabilities[0] != "prompts" || contract.Capabilities[1] != "resources" || len(contract.Tools) != 0 || len(contract.Resources) != 1 || len(contract.Prompts) != 1 || strings.Contains(string(data), "PRIVATE_") {
		t.Fatalf("unsafe or incomplete lock: %s", data)
	}
	output.Reset()
	if code := cmdMCP([]string{"resource-read", "--url", s.URL + "/mcp", "--uri", "file:///reports/latest"}); code != 0 || !strings.Contains(output.String(), "PRIVATE_RESOURCE_BODY") {
		t.Fatalf("explicit resource read exit=%d: %s", code, output.String())
	}
	output.Reset()
	if code := cmdMCP([]string{"prompt-get", "--url", s.URL + "/mcp", "--prompt", "summarize", "--arguments", `{"period":"week"}`}); code != 0 || !strings.Contains(output.String(), "PRIVATE_RENDERED_PROMPT") {
		t.Fatalf("explicit prompt render exit=%d: %s", code, output.String())
	}
	if reads.Load() != 1 || renders.Load() != 1 || toolCalls.Load() != 0 {
		t.Fatalf("read=%d render=%d tools=%d", reads.Load(), renders.Load(), toolCalls.Load())
	}
}

func TestMCPContractLockRejectsIncompleteDiscovery(t *testing.T) {
	oldOut, oldJSON := osStdout, jsonOutput
	var output bytes.Buffer
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		w.Header().Set("Content-Type", "application/json")
		if request.Method == "server/discover" {
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":["2026-07-28"],"capabilities":{"tools":{}}}}`, request.ID)
			return
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"tools":[{"name":"bad","inputSchema":{"properties":{"n":{"type":"number","x-mcp-header":"N"}}}},{"name":"good","inputSchema":{}}]}}`, request.ID)
	}))
	defer s.Close()
	path := filepath.Join(t.TempDir(), "lock.json")
	if err := os.WriteFile(path, []byte("preserve baseline"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := cmdMCP([]string{"lock", "--url", s.URL + "/mcp", "--out", path, "--force"}); code != 1 {
		t.Fatalf("exit=%d", code)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "preserve baseline" || !strings.Contains(output.String(), `"rejected_tools"`) {
		t.Fatalf("incomplete snapshot: %s %s", data, output.String())
	}
	var receipt map[string]any
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil || receipt["written"] != false {
		t.Fatalf("failure receipt is not one JSON object: %s (%v)", output.String(), err)
	}
}

func TestMCPContractAtomicFileSafety(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.json")
	if err := writeMCPContract(path, []byte("old"), false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions=%v err=%v", info, err)
	}
	if err := writeMCPContract(path, []byte("new"), false); err == nil {
		t.Fatal("existing snapshot replaced")
	}
	if err := writeMCPContract(path, []byte("new"), true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Fatal("force did not publish candidate")
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, force := range []bool{false, true} {
		if err := writeMCPContract(link, []byte("unsafe"), force); err == nil {
			t.Fatal("symlink destination accepted")
		}
	}
	if _, err := readMCPContract(link); err == nil {
		t.Fatal("symlink snapshot read")
	}
	if err := writeMCPContract(dir, []byte("unsafe"), true); err == nil {
		t.Fatal("directory destination accepted")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".gregale-mcp-lock-") {
			t.Fatal("temporary snapshot leaked")
		}
	}
}

func TestMCPContractCLICheckRejectsReview(t *testing.T) {
	oldOut, oldJSON := osStdout, jsonOutput
	var output bytes.Buffer
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json")}
	for i, path := range paths {
		c, _ := mcphosting.NewContract(mcphosting.ProtocolVersion, []mcphosting.Tool{{Name: "lookup", InputSchema: map[string]any{"type": "object", "maxProperties": i + 1}}})
		data, err := mcphosting.MarshalContract(c)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if code := cmdMCP([]string{"diff", "--before", paths[0], "--after", paths[1], "--check"}); code != 1 || !strings.Contains(output.String(), `"needs_review": true`) {
		t.Fatalf("review gate exit=%d: %s", code, output.String())
	}
}

func TestMCPContractCLIStrictCatalog(t *testing.T) {
	oldOut, oldJSON := osStdout, jsonOutput
	var output bytes.Buffer
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "reader.json"), filepath.Join(dir, "expanded-reader.json")}
	for i, path := range paths {
		tools := []mcphosting.Tool{{Name: "read", InputSchema: map[string]any{"type": "object"}}}
		if i == 1 {
			tools = append(tools, mcphosting.Tool{Name: "write", InputSchema: map[string]any{"type": "object"}})
		}
		contract, _ := mcphosting.NewContract(mcphosting.ProtocolVersion, tools)
		data, err := mcphosting.MarshalContract(contract)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name           string
		flags          []string
		unchanged      bool
		code           int
		strict, review bool
	}{
		{"default check permits addition", []string{"--check"}, false, 0, false, false},
		{"strict report preserves exit zero", []string{"--strict-catalog"}, false, 0, true, true},
		{"strict check rejects addition", []string{"--strict-catalog", "--check"}, false, 1, true, true},
		{"strict check permits unchanged", []string{"--strict-catalog", "--check"}, true, 0, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output.Reset()
			after := paths[1]
			if tc.unchanged {
				after = paths[0]
			}
			args := append([]string{"diff", "--before", paths[0], "--after", after}, tc.flags...)
			code := cmdMCP(args)
			var diff mcphosting.ContractDiff
			if err := json.Unmarshal(output.Bytes(), &diff); err != nil || code != tc.code || diff.StrictCatalog != tc.strict || diff.NeedsReview != tc.review || diff.Breaking || diff.Compatible == tc.review {
				t.Fatalf("exit=%d receipt=%s err=%v", code, output.String(), err)
			}
			if !tc.unchanged && (len(diff.Changes) != 1 || diff.Changes[0].Tool != "write" || diff.Changes[0].Kind != "tool_added") {
				t.Fatalf("changes=%+v", diff.Changes)
			}
		})
	}
}
