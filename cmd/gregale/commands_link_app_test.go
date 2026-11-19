package main_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// linkAPI serves the app, project list and project reads `gregale link --app`
// makes.
func linkAPI(t *testing.T, routes map[string]string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.Error(w, `{"code":"not_found","title":"Not found","status":404}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_API", srv.URL)
}

func captureLinkStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	previous := *main.OsStdout
	*main.OsStdout = &out
	t.Cleanup(func() { *main.OsStdout = previous })
	return &out
}

// Deploy's unlinked-app notice suggests `gregale link --app <slug>`; it used
// to print usage because a project slug was required (Kroki feedback
// 2026-11-17).
func TestCmdLinkAppAloneLinksStandaloneApp(t *testing.T) {
	main.ResetJSONOut(t)
	root := t.TempDir()
	t.Chdir(root)
	linkAPI(t, map[string]string{"/v1/apps/kroki": `{"slug":"kroki"}`})
	out := captureLinkStdout(t)

	if code := main.CmdLink([]string{"--app", "kroki"}); code != 0 {
		t.Fatalf("link --app kroki exit = %d, output %q", code, out.String())
	}
	ctx, _, err := main.LinkedProjectContext(root)
	if err != nil {
		t.Fatalf("read context: %v", err)
	}
	if ctx.Project != "" || ctx.App != "kroki" {
		t.Fatalf("context = %+v, want standalone app kroki", ctx)
	}
	if !strings.Contains(out.String(), "Linked to app kroki") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestCmdLinkAppAloneResolvesItsProject(t *testing.T) {
	main.ResetJSONOut(t)
	root := t.TempDir()
	t.Chdir(root)
	linkAPI(t, map[string]string{
		"/v1/apps/api":       `{"slug":"api","project_id":"p-1"}`,
		"/v1/projects":       `[{"id":"p-0","slug":"other"},{"id":"p-1","slug":"shop"}]`,
		"/v1/projects/shop":  `{"id":"p-1","slug":"shop","workloads":[{"slug":"api"},{"slug":"worker"}]}`,
		"/v1/projects/other": `{"id":"p-0","slug":"other","workloads":[]}`,
	})
	captureLinkStdout(t)

	if code := main.CmdLink([]string{"--app", "api", "--env", "staging"}); code == 0 {
		// The environment is validated against the project; the fake has
		// none, so --env must reach that check rather than be unknown.
		t.Fatal("link with an unknown environment succeeded")
	}
	if code := main.CmdLink([]string{"--app", "api"}); code != 0 {
		t.Fatalf("link --app api exit = %d", code)
	}
	ctx, _, err := main.LinkedProjectContext(root)
	if err != nil {
		t.Fatalf("read context: %v", err)
	}
	if ctx.Project != "shop" || ctx.App != "api" {
		t.Fatalf("context = %+v, want project shop app api", ctx)
	}
}

// Every `gregale link ...` command the CLI suggests in prose must parse.
func TestSuggestedLinkCommandsParse(t *testing.T) {
	re := regexp.MustCompile("`gregale link ([^`]*)`")
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	type suggestion struct{ file, command string }
	var suggestions []suggestion
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue // code comments are not suggestions
			}
			for _, m := range re.FindAllStringSubmatch(line, -1) {
				suggestions = append(suggestions, suggestion{file, m[1]})
			}
		}
	}
	placeholders := strings.NewReplacer("<slug>", "kroki", "%s", "kroki", "<project-slug>", "shop", "<project>", "shop")
	for _, s := range suggestions {
		t.Run(s.file+"/"+s.command, func(t *testing.T) {
			main.ResetJSONOut(t)
			t.Chdir(t.TempDir())
			linkAPI(t, map[string]string{
				"/v1/apps/kroki":    `{"slug":"kroki"}`,
				"/v1/projects/shop": `{"id":"p-1","slug":"shop","workloads":[{"slug":"kroki"}]}`,
			})
			captureLinkStdout(t)
			if code := main.CmdLink(strings.Fields(placeholders.Replace(s.command))); code != 0 {
				t.Errorf("%s suggests `gregale link %s`, which exits %d", s.file, s.command, code)
			}
		})
	}
	if len(suggestions) == 0 {
		t.Fatal("found no suggested link commands; the pattern is stale")
	}
}
