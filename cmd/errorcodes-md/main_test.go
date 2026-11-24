package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/whycopy"
)

// fixture writes a small module whose problems exercise each way a code
// reaches the page.
func fixture(t *testing.T, exclusions string, extra ...string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/m\n",
		"pkg/api/errors.go": `package api

import "net/http"

const docsBase = "https://gregale.dev/docs"

const (
	CodeAppLimit  = "plan_limit_apps"
	CodeHidden    = "alpha_hidden"
	CodeExcluded  = "beta_excluded"
	CodeConstOnly = "omega_declared"
)

type Problem struct {
	Status int
	Code, Title, Hint, DocsURL string
}

func NewProblem(status int, code, title, detail string) *Problem { return &Problem{} }
func (p *Problem) WithHint(h string) *Problem                  { return p }
func (p *Problem) WithDocs(u string) *Problem                  { return p }

func ErrAppLimit() *Problem {
	return NewProblem(http.StatusForbidden, CodeAppLimit, "App limit reached", "x").
		WithHint("delete an app or upgrade the plan").
		WithDocs(docsBase + "/plans#apps")
}

func ErrAppLimitAgain() *Problem {
	return NewProblem(403, CodeAppLimit, "app limit reached", "y").WithDocs(docsBase + "/plans")
}
`,
		"pkg/feature/feature.go": `package feature

import (
	"net/http"

	"example.com/m/pkg/api"
)

func problems() {
	_ = api.NewProblem(http.StatusBadGateway, "gamma_upstream", "Upstream failed", "d")
	_ = &api.Problem{Status: 409, Code: api.CodeHidden, Title: "Hidden thing"}
	_ = &api.Problem{Status: 409, Code: api.CodeExcluded, Title: "Excluded thing"}
	_ = api.NewProblem(400, "epsilon_internal", "schedd refused the wake", "d")
	_ = api.NewProblem(400, "zeta_unpublished", "Zeta", "d").WithDocs("https://gregale.dev/docs/events")
	_ = api.NewProblem(400, "eta_placeholder", "Eta", "d").WithHint("run ` + "`gregale logs <app>`" + ` for <app>")
	_ = conflict("delta_helper", "Delta title")
}

func conflict(code, title string) *api.Problem {
	return api.NewProblem(http.StatusConflict, code, title, "")
}
`,
		"cmd/tool/main.go": `package main

import "example.com/m/pkg/api"

func main() { _ = api.NewProblem(503, "theta_cli", "Could not reach Gregale", "") }
`,
		"docs/customer-pages.json": `{"version": 1, "routes": [
	{"path": "plans", "source": "docs/plans.md"},
	{"path": "errors", "source": "docs/errors.md"},
	{"path": "events", "source": "docs/events.md", "unpublished": "preview"},
	{"path": "error-codes", "source": "docs/error-codes.md"}
]}`,
		"docs/plans.md":                    "# Plans and pricing\n",
		"docs/errors.md":                   "# Errors\n\n`alpha_hidden` means the thing is hidden.\n",
		"docs/events.md":                   "# Events\n",
		"docs/error-codes-unpublished.txt": exclusions,
	}
	// extra holds the bodies of more problems, one per string, built in
	// package feature.
	var more strings.Builder
	more.WriteString("package feature\n\nimport \"example.com/m/pkg/api\"\n\nfunc more() {\n")
	for _, body := range extra {
		fmt.Fprintf(&more, "\t_ = %s\n", body)
	}
	more.WriteString("}\n")
	files["pkg/feature/more.go"] = more.String()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func generate(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	if err := run(root, &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// entryOf returns the text of one code's entry.
func entryOf(page, code string) (string, bool) {
	head := "### `" + code + "`\n"
	i := strings.Index(page, head)
	if i < 0 {
		return "", false
	}
	rest := page[i+len(head):]
	if j := strings.Index(rest, "\n#"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest), true
}

func TestGeneratedEntries(t *testing.T) {
	page := generate(t, fixture(t, "# comment\nbeta_*  # trailing comment\nalpha_hidden\n"))
	tests := []struct {
		code string
		want string // "" means the code is left out
	}{
		// Status from net/http, case-folded titles, hint capitalized, one
		// link per page preferring the anchored one.
		{"plan_limit_apps", "HTTP 403 Forbidden · App limit reached\n\nDelete an app or upgrade the plan\n\nSee [Plans and pricing](plans.md#apps)."},
		// The edge sends Gregale's own 502 as 503.
		{"gamma_upstream", "HTTP 503 Service Unavailable · Upstream failed"},
		// Problems built under cmd/ count too.
		{"theta_cli", "HTTP 503 Service Unavailable · Could not reach Gregale"},
		// A helper's call site supplies the code and title.
		{"delta_helper", "HTTP 409 Conflict · Delta title"},
		// Engineering material is dropped; the status stays.
		{"epsilon_internal", "HTTP 400 Bad Request"},
		// Placeholders are escaped outside code spans only.
		{"eta_placeholder", "HTTP 400 Bad Request · Eta\n\nRun `gregale logs <app>` for &lt;app>"},
		// Excluded, but a published page already names it.
		{"alpha_hidden", "HTTP 409 Conflict · Hidden thing\n\nSee [Errors](errors.md)."},
		// Declared but never built: no status the API's table knows.
		{"omega_declared", "The response's `title` and `detail` describe the failure."},
		{"beta_excluded", ""},
		// Links only to an unpublished page.
		{"zeta_unpublished", ""},
	}
	for _, tt := range tests {
		got, ok := entryOf(page, tt.code)
		if tt.want == "" {
			if ok {
				t.Errorf("%s: listed, want left out:\n%s", tt.code, got)
			}
			continue
		}
		if !ok {
			t.Errorf("%s: missing from page", tt.code)
			continue
		}
		if got != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", tt.code, got, tt.want)
		}
	}
	if !strings.Contains(page, "\n## P\n") || strings.Index(page, "## A") > strings.Index(page, "## P") {
		t.Errorf("sections are not alphabetical letters:\n%s", page)
	}
	// The jump bar names each letter section once, in order, and no others.
	jump := regexp.MustCompile(`(?m)^Jump to: (.*)$`).FindStringSubmatch(page)
	if jump == nil {
		t.Fatalf("no jump bar:\n%s", page)
	}
	var want []string
	for _, m := range regexp.MustCompile(`(?m)^## ([A-Z])$`).FindAllStringSubmatch(page, -1) {
		want = append(want, fmt.Sprintf("[%s](#%s)", m[1], strings.ToLower(m[1])))
	}
	if got := jump[1]; got != strings.Join(want, " · ") {
		t.Errorf("jump bar %q, want %q", got, strings.Join(want, " · "))
	}
}

func TestExclusionsMustMatch(t *testing.T) {
	tests := []struct {
		name, exclusions, wantErr string
	}{
		{"unused prefix", "nope_*\n", "nope_*"},
		{"unused code", "nope_code\n", "nope_code"},
		{"bad line", "Not A Code\n", "neither a code nor a prefix"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(fixture(t, tt.exclusions), &strings.Builder{})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("run error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// A problem that links an entry on the page must name one the page lists:
// the API linked errors#<code> anchors that errors.md never had.
func TestEntryLinksMustBeListed(t *testing.T) {
	const ex = "beta_*\n"
	tests := []struct {
		name, problem, wantErr string
	}{
		{"own entry", `api.NewProblem(429, "kappa_limited", "K", "").WithDocs("https://gregale.dev/docs/error-codes#kappa_limited")`, ""},
		{"another listed entry", `api.NewProblem(429, "kappa_limited", "K", "").WithDocs("https://gregale.dev/docs/error-codes#plan_limit_apps")`, ""},
		{"bare page", `api.NewProblem(429, "kappa_limited", "K", "").WithDocs("https://gregale.dev/docs/error-codes")`, ""},
		{"no such code", `api.NewProblem(429, "kappa_limited", "K", "").WithDocs("https://gregale.dev/docs/error-codes#kappa_limit")`, "kappa_limited → error-codes#kappa_limit"},
		{"excluded code", `api.NewProblem(429, "kappa_limited", "K", "").WithDocs("https://gregale.dev/docs/error-codes#beta_excluded")`, "kappa_limited → error-codes#beta_excluded"},
		{"problem literal", `&api.Problem{Status: 409, Code: "kappa_conflict", DocsURL: "https://gregale.dev/docs/error-codes#nope"}`, "kappa_conflict → error-codes#nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(fixture(t, ex, tt.problem), &strings.Builder{})
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("run error = %v, want none", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("run error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestStatusText(t *testing.T) {
	tests := []struct {
		statuses map[int]int
		want     string
	}{
		{map[int]int{}, ""},
		{map[int]int{429: 1, 403: 2}, "HTTP 403 Forbidden or 429 Too Many Requests"},
		{map[int]int{400: 1, 403: 1, 404: 1, 409: 1}, "HTTP status varies by operation"},
	}
	for _, tt := range tests {
		if got := statusText(tt.statuses); got != tt.want {
			t.Errorf("statusText(%v) = %q, want %q", tt.statuses, got, tt.want)
		}
	}
}

func TestCustomerStatus(t *testing.T) {
	for in, want := range map[int]int{502: 503, 504: 503, 409: 409, 0: 0, 200: 0} {
		if got := customerStatus(in); got != want {
			t.Errorf("customerStatus(%d) = %d, want %d", in, got, want)
		}
	}
}

// A code no call site titles or hints takes its whycopy row's title and hint,
// which is what Decorate puts on the problem; a call site's own text wins.
func TestWhycopyFallback(t *testing.T) {
	prev := whycopyRow
	t.Cleanup(func() { whycopyRow = prev })
	whycopyRow = func(code string) (whycopy.Render, bool) {
		switch code {
		case "omega_declared":
			return whycopy.Render{Title: "Omega failed", Hint: "retry the omega step"}, true
		case "plan_limit_apps":
			return whycopy.Render{Title: "Catalog title", Hint: "catalog hint"}, true
		}
		return whycopy.Render{}, false
	}
	page := generate(t, fixture(t, ""))
	for code, want := range map[string]string{
		"omega_declared":  "Omega failed\n\nRetry the omega step",
		"plan_limit_apps": "HTTP 403 Forbidden · App limit reached\n\nDelete an app or upgrade the plan\n\nSee [Plans and pricing](plans.md#apps).",
	} {
		if got, ok := entryOf(page, code); !ok || got != want {
			t.Errorf("%s:\n got %q\nwant %q", code, got, want)
		}
	}
}
