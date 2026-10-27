package agentpresets_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/agentpresets"
	"github.com/onebox-faas/faas/pkg/api"
)

func repoFile(t *testing.T, rel string) []byte {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(here), "..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The catalog pins the same versions agent-base installs: package.json and
// the resolved lockfile both agree with it, so a harness bump is one change
// to all three files.
func TestCatalogMatchesAgentBaseLock(t *testing.T) {
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(repoFile(t, "images/agent-base/package.json"), &pkg); err != nil {
		t.Fatal(err)
	}
	var lock struct {
		LockfileVersion int `json:"lockfileVersion"`
		Packages        map[string]struct {
			Version   string `json:"version"`
			Resolved  string `json:"resolved"`
			Integrity string `json:"integrity"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(repoFile(t, "images/agent-base/package-lock.json"), &lock); err != nil {
		t.Fatal(err)
	}
	if lock.LockfileVersion != 3 {
		t.Fatalf("lockfileVersion %d, want 3", lock.LockfileVersion)
	}
	presets := agentpresets.All()
	if len(pkg.Dependencies) != len(presets) {
		t.Fatalf("package.json has %d dependencies, the catalog %d presets", len(pkg.Dependencies), len(presets))
	}
	for _, p := range presets {
		if got := pkg.Dependencies[p.Package]; got != p.Version {
			t.Errorf("%s: package.json pins %s %q, catalog %q", p.Name, p.Package, got, p.Version)
		}
		if got := lock.Packages["node_modules/"+p.Package].Version; got != p.Version {
			t.Errorf("%s: lockfile resolves %s %q, catalog %q", p.Name, p.Package, got, p.Version)
		}
	}
	for path, entry := range lock.Packages {
		if path == "" {
			continue
		}
		if !strings.HasPrefix(entry.Integrity, "sha512-") || !strings.HasPrefix(entry.Resolved, "https://registry.npmjs.org/") {
			t.Errorf("%s: every locked package needs a registry URL and a sha512 integrity", path)
		}
	}
}

func TestPresetsMatchTheAPI(t *testing.T) {
	want := []string{api.AgentPresetCodex, api.AgentPresetOpenCode, api.AgentPresetClaudeCode}
	if got := agentpresets.Names(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("names %v, want %v", got, want)
	}
	for _, p := range agentpresets.All() {
		for provider := range p.Models {
			if provider != api.AgentModelOpenAI && provider != api.AgentModelAnthropic {
				t.Errorf("%s: unknown model provider %q", p.Name, provider)
			}
		}
		if p.DefaultRAMMB < api.AgentRunRAMMBMin || p.DefaultRAMMB > api.AgentRunRAMMBMax[api.PlanHobby.PlanIndex()] {
			t.Errorf("%s: default_ram_mb %d must fit the smallest paid plan", p.Name, p.DefaultRAMMB)
		}
	}
	if agentpresets.Image() != "agent-base" {
		t.Fatalf("image %q", agentpresets.Image())
	}
}

func TestLookupReturnsCopies(t *testing.T) {
	p, ok := agentpresets.Lookup("codex")
	if !ok {
		t.Fatal("codex missing")
	}
	p.Run[0], p.Models["openai"] = "evil", "X"
	claude, _ := agentpresets.Lookup("claude-code")
	claude.Env["DISABLE_AUTOUPDATER"] = "0"
	if again, _ := agentpresets.Lookup("claude-code"); again.Env["DISABLE_AUTOUPDATER"] != "1" {
		t.Fatal("catalog env mutated through a lookup")
	}
	again, _ := agentpresets.Lookup("codex")
	if again.Run[0] != "codex" || again.Models["openai"] != "OPENAI_BASE_URL" {
		t.Fatalf("catalog mutated through a lookup: %+v", again)
	}
	if _, ok := agentpresets.Lookup("aider"); ok {
		t.Fatal("unknown preset found")
	}
}

func TestParseRejectsBadCatalogs(t *testing.T) {
	good := `{"name":"x","harness":"X","package":"x","version":"1","license":"MIT","binary":"/usr/local/bin/x","run":["x","{prompt}"],"models":{"openai":"OPENAI_BASE_URL"},"default_ram_mb":1024}`
	for name, body := range map[string]string{
		"no presets":       `{"version":1,"image":"agent-base","presets":[]}`,
		"wrong version":    `{"version":2,"image":"agent-base","presets":[` + good + `]}`,
		"unknown field":    `{"version":1,"image":"agent-base","presets":[` + good + `],"extra":1}`,
		"duplicate":        `{"version":1,"image":"agent-base","presets":[` + good + `,` + good + `]}`,
		"no prompt":        `{"version":1,"image":"agent-base","presets":[` + strings.Replace(good, `"{prompt}"`, `"p"`, 1) + `]}`,
		"two prompts":      `{"version":1,"image":"agent-base","presets":[` + strings.Replace(good, `"{prompt}"`, `"{prompt}","{prompt}"`, 1) + `]}`,
		"other binary":     `{"version":1,"image":"agent-base","presets":[` + strings.Replace(good, `"run":["x"`, `"run":["y"`, 1) + `]}`,
		"binary elsewhere": `{"version":1,"image":"agent-base","presets":[` + strings.Replace(good, `/usr/local/bin/x`, `/opt/x`, 1) + `]}`,
		"odd ram":          `{"version":1,"image":"agent-base","presets":[` + strings.Replace(good, `1024`, `700`, 1) + `]}`,
		"no models":        `{"version":1,"image":"agent-base","presets":[` + strings.Replace(good, `{"openai":"OPENAI_BASE_URL"}`, `{}`, 1) + `]}`,
		"reserved env":     `{"version":1,"image":"agent-base","presets":[` + strings.Replace(good, `"default_ram_mb"`, `"env":{"GREGALE_OUTBOUND_URL":"x"},"default_ram_mb"`, 1) + `]}`,
	} {
		if _, err := agentpresets.Parse([]byte(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := agentpresets.Parse([]byte(`{"version":1,"image":"agent-base","presets":[` + good + `]}`)); err != nil {
		t.Fatalf("good catalog: %v", err)
	}
}
