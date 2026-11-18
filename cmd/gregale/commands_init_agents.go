// commands_init_agents.go — `gregale init --agents` sets up an existing
// project for coding agents. `gregale init --template` writes a full
// AGENTS.md into new projects; an existing project gets a marked Gregale
// section instead, plus the one setting Claude Code and Gemini CLI each
// need to read AGENTS.md. Codex and Cursor read AGENTS.md on their own.
//
// Every change is idempotent: a second run replaces the marked section in
// place and leaves files that already point at AGENTS.md alone. Nothing the
// customer wrote outside the markers is changed.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

// Actions an agent file can take; dry runs report the same values.
const (
	agentFileCreated   = "created"
	agentFileUpdated   = "updated"
	agentFileUnchanged = "unchanged"
	agentFileManual    = "manual"
)

// agentFileChange is one file init --agents writes or leaves alone.
type agentFileChange struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Note   string `json:"note,omitempty"`
	data   []byte
}

// initAgentsReceipt is the --json result of init --agents.
type initAgentsReceipt struct {
	Path   string            `json:"path"`
	App    string            `json:"app"`
	DryRun bool              `json:"dry_run"`
	Files  []agentFileChange `json:"files"`
}

// agentsHomeDir locates the user's home for agent detection; tests swap it.
var agentsHomeDir = os.UserHomeDir

// runInitAgents plans every agent file for dir, then writes the changes
// unless dryRun.
func runInitAgents(dir, name string, dryRun bool, stdout io.Writer) int {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return printErr("Could not resolve --path", err)
	}
	if info, err := os.Stat(absDir); err != nil || !info.IsDir() {
		return printErr("Could not set up agents in "+absDir, fmt.Errorf("%s is not an existing directory", absDir))
	}
	// jobSlugPattern mirrors the server's slug rule for apps too.
	if name != "" && !jobSlugPattern.MatchString(name) {
		return printErr("Invalid --name", fmt.Errorf("%q is not an app slug: 3-40 lowercase letters, digits or hyphens", name))
	}
	app := initAgentsApp(absDir, name)
	changes, err := planAgentFiles(absDir, app)
	if err != nil {
		return printErr("Could not plan the agent files", err)
	}
	if !dryRun {
		if err := writeAgentFiles(absDir, changes); err != nil {
			return printErr("Could not write the agent files", err)
		}
	}
	receipt := initAgentsReceipt{Path: absDir, App: app, DryRun: dryRun, Files: changes}
	if jsonOutput {
		return jsonOut(writeJSONTo(stdout, receipt))
	}
	printInitAgents(stdout, receipt)
	return 0
}

// initAgentsApp names the app the section deploys: --name, else the app
// this checkout is linked to, else the directory name, as deploy does.
func initAgentsApp(dir, name string) string {
	if name != "" {
		return name
	}
	if context, _, err := findProjectContext(dir); err == nil && context.App != "" {
		return context.App
	}
	return sanitizeSlug(filepath.Base(dir))
}

func planAgentFiles(dir, app string) ([]agentFileChange, error) {
	agents, err := planAgentsMD(dir, app)
	if err != nil {
		return nil, err
	}
	changes := []agentFileChange{agents}
	if claude, ok, err := planClaudeImport(dir); err != nil {
		return nil, err
	} else if ok {
		changes = append(changes, claude)
	}
	if gemini, ok, err := planGeminiSettings(dir); err != nil {
		return nil, err
	} else if ok {
		changes = append(changes, gemini)
	}
	return changes, nil
}

// planAgentsMD creates AGENTS.md, replaces its marked Gregale section, or
// appends one. An AGENTS.md written by gregale init --template already has
// the full instructions and stays as it is.
func planAgentsMD(dir, app string) (agentFileChange, error) {
	section, err := templates.AgentsSection(app)
	if err != nil {
		return agentFileChange{}, err
	}
	change := agentFileChange{Path: "AGENTS.md"}
	current, err := readOptional(filepath.Join(dir, change.Path))
	if err != nil {
		return change, err
	}
	switch {
	case current == nil:
		change.Action, change.data = agentFileCreated, []byte("# Agent instructions\n\n"+section)
	case strings.Contains(string(current), templates.AgentsGuideMarker):
		change.Action, change.Note = agentFileUnchanged, "already has the instructions gregale init wrote"
	default:
		next := replaceAgentsSection(string(current), section)
		change.Action, change.data = agentFileUpdated, []byte(next)
		if next == string(current) {
			change.Action, change.data = agentFileUnchanged, nil
		}
	}
	return change, nil
}

// replaceAgentsSection swaps the marked section for section, or appends
// section after a blank line when there is none.
func replaceAgentsSection(current, section string) string {
	begin := strings.Index(current, templates.AgentsSectionBegin)
	if begin >= 0 {
		if end := strings.Index(current[begin:], templates.AgentsSectionEnd); end >= 0 {
			stop := begin + end
			if nl := strings.IndexByte(current[stop:], '\n'); nl >= 0 {
				stop += nl + 1
			} else {
				stop = len(current)
			}
			return current[:begin] + section + current[stop:]
		}
	}
	trimmed := strings.TrimRight(current, "\n")
	if trimmed == "" {
		return section
	}
	return trimmed + "\n\n" + section
}

// planClaudeImport makes an existing CLAUDE.md import AGENTS.md: Claude
// Code reads AGENTS.md by itself only when a project has no CLAUDE.md.
func planClaudeImport(dir string) (agentFileChange, bool, error) {
	change := agentFileChange{Path: "CLAUDE.md"}
	current, err := readOptional(filepath.Join(dir, change.Path))
	if err != nil || current == nil {
		return change, false, err
	}
	for _, line := range strings.Split(string(current), "\n") {
		if strings.TrimSpace(line) == "@AGENTS.md" {
			change.Action = agentFileUnchanged
			return change, true, nil
		}
	}
	change.Action, change.Note = agentFileUpdated, "imports AGENTS.md"
	change.data = append([]byte("@AGENTS.md\n\n"), current...)
	return change, true, nil
}

// planGeminiSettings points Gemini CLI at AGENTS.md when the project or
// this machine uses Gemini CLI. It creates .gemini/settings.json but never
// rewrites an existing one: re-encoding would reorder the customer's keys,
// so it says what to add instead.
func planGeminiSettings(dir string) (agentFileChange, bool, error) {
	change := agentFileChange{Path: filepath.Join(".gemini", "settings.json")}
	current, err := readOptional(filepath.Join(dir, change.Path))
	if err != nil {
		return change, false, err
	}
	if current == nil {
		if !usesGeminiCLI(dir) {
			return change, false, nil
		}
		change.Action = agentFileCreated
		change.data = []byte("{\n  \"context\": {\n    \"fileName\": [\"AGENTS.md\", \"GEMINI.md\"]\n  }\n}\n")
		return change, true, nil
	}
	if geminiReadsAgents(current) {
		change.Action = agentFileUnchanged
		return change, true, nil
	}
	change.Action = agentFileManual
	change.Note = `add "AGENTS.md" to context.fileName so Gemini CLI reads it`
	return change, true, nil
}

// usesGeminiCLI reports a .gemini folder or GEMINI.md in the project, or a
// ~/.gemini folder Gemini CLI made on this machine.
func usesGeminiCLI(dir string) bool {
	for _, path := range []string{filepath.Join(dir, ".gemini"), filepath.Join(dir, "GEMINI.md")} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	home, err := agentsHomeDir()
	if err != nil || home == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(home, ".gemini"))
	return err == nil && info.IsDir()
}

// geminiReadsAgents reports whether settings name AGENTS.md as a context
// file, under context.fileName or the older top-level contextFileName.
func geminiReadsAgents(settings []byte) bool {
	var parsed struct {
		Context struct {
			FileName json.RawMessage `json:"fileName"`
		} `json:"context"`
		ContextFileName json.RawMessage `json:"contextFileName"`
	}
	if json.Unmarshal(settings, &parsed) != nil {
		return false
	}
	return namesAgentsMD(parsed.Context.FileName) || namesAgentsMD(parsed.ContextFileName)
}

func namesAgentsMD(raw json.RawMessage) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == "AGENTS.md"
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil {
		return false
	}
	for _, name := range many {
		if name == "AGENTS.md" {
			return true
		}
	}
	return false
}

func writeAgentFiles(dir string, changes []agentFileChange) error {
	for _, change := range changes {
		if change.data == nil {
			continue
		}
		path := filepath.Join(dir, change.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(change.Path), err)
		}
		if err := os.WriteFile(path, change.data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", change.Path, err)
		}
	}
	return nil
}

// readOptional returns nil, nil for a file that does not exist.
func readOptional(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if data == nil {
		data = []byte{}
	}
	return data, nil
}

func printInitAgents(w io.Writer, receipt initAgentsReceipt) {
	verb := map[string]string{agentFileCreated: "Wrote", agentFileUpdated: "Updated", agentFileUnchanged: "Kept", agentFileManual: "Check"}
	if receipt.DryRun {
		verb = map[string]string{agentFileCreated: "Would write", agentFileUpdated: "Would update", agentFileUnchanged: "Would keep", agentFileManual: "Check"}
	}
	for _, change := range receipt.Files {
		line := verb[change.Action] + " " + change.Path
		if change.Note != "" {
			line += ": " + change.Note
		}
		if change.Action == agentFileManual {
			PrintWarn(w, "%s", line)
			continue
		}
		PrintProgress(w, "%s", line)
	}
	PrintProgress(w, "Agents will deploy this project as %s. Codex and Cursor read AGENTS.md on their own.", receipt.App)
	PrintProgress(w, "Docs: https://gregale.dev/docs/agents")
}
