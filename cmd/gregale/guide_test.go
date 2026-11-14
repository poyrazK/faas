// adr: 20261112223000

package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// agentGuideDir holds the generated pages the docs site serves to agents
// (llms.txt). UPDATE_AGENT_GUIDE=1 rewrites them.
const agentGuideDir = "../../docs/agent-guide"

var inlineCommand = regexp.MustCompile("`(gregale [^`]+)`")

func TestEveryGuideCommandIsARealCommand(t *testing.T) {
	for _, task := range main.GuideTasks() {
		commands := main.GuideCommands(task)
		for _, m := range inlineCommand.FindAllStringSubmatch(main.RenderGuidePage(task), -1) {
			commands = append(commands, m[1])
		}
		for _, command := range commands {
			if err := main.ResolveHintCommand(command); err != nil {
				t.Errorf("guide %s: %v", task, err)
			}
		}
	}
	// Pages point at each other by name.
	for _, m := range regexp.MustCompile("`gregale guide ([a-z-]+)`").FindAllStringSubmatch(strings.Join(allGuidePages(), "\n"), -1) {
		if !containsString(main.GuideTasks(), m[1]) {
			t.Errorf("a page points at guide task %q, which does not exist", m[1])
		}
	}
}

func TestGuidePagesStaySmall(t *testing.T) {
	for _, task := range main.GuideTasks() {
		if n := len(main.RenderGuidePage(task)); n > main.GuidePageMaxBytes {
			t.Errorf("guide %s is %d bytes; keep a page under %d", task, n, main.GuidePageMaxBytes)
		}
	}
}

// The ADR's task list (ADR-20261112223000 §4).
func TestGuideHasTheADRTasks(t *testing.T) {
	for _, task := range []string{"deploy", "compose-project", "health-checks", "private-services", "secrets-and-env", "rollouts", "debug-a-failure"} {
		if !containsString(main.GuideTasks(), task) {
			t.Errorf("guide lacks task %s", task)
		}
	}
}

func TestAgentGuideDocsAreGenerated(t *testing.T) {
	want := map[string]string{"README.md": "# Gregale agent guide\n\n" + strings.TrimPrefix(main.RenderGuideIndex(), "Gregale task guide. ")}
	for _, task := range main.GuideTasks() {
		want[task+".md"] = main.RenderGuidePage(task)
	}
	if os.Getenv("UPDATE_AGENT_GUIDE") == "1" {
		if err := os.MkdirAll(agentGuideDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range want {
			if err := os.WriteFile(filepath.Join(agentGuideDir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, body := range want {
		got, err := os.ReadFile(filepath.Join(agentGuideDir, name))
		if err != nil || string(got) != body {
			t.Errorf("docs/agent-guide/%s is stale; run UPDATE_AGENT_GUIDE=1 go test -run TestAgentGuideDocsAreGenerated ./cmd/gregale", name)
		}
	}
}

func TestGuideCommand(t *testing.T) {
	main.ResetJSONOut(t)
	stdout, _, restore := main.SwapIO(t)
	code := main.Run([]string{"guide", "private-services"})
	restore()
	if code != 0 || !strings.Contains(stdout.String(), "gregale app <app> --ports <name>=tcp:<port>:internal") {
		t.Fatalf("exit=%d stdout=%q", code, stdout.String())
	}

	main.ResetJSONOut(t)
	stdout, _, restore = main.SwapIO(t)
	code = main.Run([]string{"guide", "--json"})
	restore()
	var index struct {
		Tasks []struct {
			Task string `json:"task"`
		} `json:"tasks"`
	}
	if code != 0 || json.Unmarshal(stdout.Bytes(), &index) != nil || len(index.Tasks) != len(main.GuideTasks()) {
		t.Fatalf("exit=%d json=%q", code, stdout.String())
	}

	main.ResetJSONOut(t)
	_, stderr, restore := main.SwapIO(t)
	code = main.Run([]string{"guide", "no-such-task"})
	restore()
	if code == 0 || !strings.Contains(stderr(), "tasks: ") {
		t.Fatalf("exit=%d stderr=%q", code, stderr())
	}
}

// The agent pointer goes to stderr on help and errors, never to stdout,
// and GREGALE_NO_AGENT_HINT=1 silences it (ADR-20261112223000 §5).
func TestAgentHintOnlyOnStderr(t *testing.T) {
	for _, marker := range []string{"CLAUDECODE", "GEMINI_CLI", "CODEX_THREAD_ID", "CODEX_SANDBOX", "CURSOR_AGENT"} {
		t.Setenv(marker, "")
	}
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("GREGALE_NO_AGENT_HINT", "")
	main.ResetAgentHint()
	main.ResetJSONOut(t)
	stdout, stderr, restore := main.SwapIO(t)
	_ = main.Run([]string{"guide", "--help"})
	restore()
	if !strings.Contains(stderr(), "Agent: run `gregale guide`") || strings.Contains(stdout.String(), "Agent: run") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr())
	}

	t.Setenv("GREGALE_NO_AGENT_HINT", "1")
	main.ResetAgentHint()
	main.ResetJSONOut(t)
	_, stderr, restore = main.SwapIO(t)
	_ = main.Run([]string{"guide", "no-such-task"})
	restore()
	if strings.Contains(stderr(), "Agent: run") {
		t.Fatalf("GREGALE_NO_AGENT_HINT=1 printed the hint: %q", stderr())
	}
}

func allGuidePages() []string {
	var pages []string
	for _, task := range main.GuideTasks() {
		pages = append(pages, main.RenderGuidePage(task))
	}
	return pages
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
