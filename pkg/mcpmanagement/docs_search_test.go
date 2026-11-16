// adr: 20261116183445
package mcpmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const docsIndexFixture = `# Gregale

> A serverless platform.

Coding agents: start with [Use Gregale from coding agents](SITE/docs/agents.md).

## Ship releases

- [Rollouts](SITE/docs/rollouts.md): Canary and staged rollouts.
- [Releases](SITE/docs/releases.md): Promote and roll back releases.

## Reference

- [Plans and limits](SITE/docs/plans.md): What each plan includes.
- [Ignored](https://example.com/docs/other.md): Not on the site.
`

const docsFullFixture = "# Gregale\n\n> A serverless platform.\n\nKey facts: canary everything.\n\n" +
	"# Rollouts\n\n> Canary and staged rollouts.\n\nRoll out a new deployment gradually.\n\n" +
	"## Canary presets\n\nUse `gregale deploy --canary 10` to send 10% of traffic to the new deployment.\n\n" +
	"```bash\n# Cron limits are not a heading here\ngregale deploy --canary 10\n```\n\n" +
	"### Abort a canary\n\nRun `gregale rollout abort` to stop a canary.\n\n" +
	"## Watch a rollout\n\nUse [gregale rollout watch](SITE/docs/cli-operations.md) to follow progress.\n\n" +
	"Source: SITE/docs/rollouts\n\n" +
	"# Releases\n\n> Promote and roll back releases.\n\nEvery deploy creates a release.\n\n" +
	"## Use `gregale rollback`\n\nIt restores the previous release.\n\n" +
	"Source: SITE/docs/releases\n\n" +
	"# Plans and limits\n\n> What each plan includes.\n\n" +
	"## Cron limits\n\nHobby allows 5 crons per app; Pro allows 20 crons per app.\n\n" +
	"## Rollouts per plan\n\nCanary rollouts are available on every plan.\n\n" +
	"Source: SITE/docs/plans\n"

type docsSite struct {
	*httptest.Server
	fetches atomic.Int32
	failing atomic.Bool
	full    string
}

func newDocsSite(t *testing.T) *docsSite {
	t.Helper()
	site := &docsSite{full: docsFullFixture}
	site.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/llms.txt" {
			site.fetches.Add(1)
		}
		if site.failing.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		body := ""
		switch r.URL.Path {
		case "/llms.txt":
			body = docsIndexFixture
		case "/llms-full.txt":
			body = site.full
		default:
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(strings.ReplaceAll(body, "SITE", site.URL)))
	}))
	t.Cleanup(site.Close)
	return site
}

func connectDocsServer(t *testing.T, site *docsSite) (callDocs func(name string, args any, wantError bool) map[string]json.RawMessage) {
	t.Helper()
	backend := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(backend.Close)
	cs, ctx := connectSelectedTestServer(t, backend, Options{APIBase: backend.URL, Token: testToken, docs: newDocsLibrary(site.URL, site.Client())}, "2026-07-28")
	return func(name string, args any, wantError bool) map[string]json.RawMessage {
		t.Helper()
		return callTool(t, ctx, cs, name, args, wantError)
	}
}

func decodeField[T any](t *testing.T, out map[string]json.RawMessage, key string) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(out[key], &value); err != nil {
		t.Fatalf("%s: %v (%s)", key, err, out[key])
	}
	return value
}

func TestDocsSearchRanksPublishedSections(t *testing.T) {
	site := newDocsSite(t)
	call := connectDocsServer(t, site)

	out := call("gregale_search_docs", map[string]any{"query": "cron limits"}, false)
	hits := decodeField[[]docsHit](t, out, "results")
	if len(hits) == 0 || hits[0].Page != "plans" || hits[0].Section != "cron-limits" || hits[0].URL != site.URL+"/docs/plans#cron-limits" {
		t.Fatalf("cron limits hits = %+v", hits)
	}
	if !strings.Contains(hits[0].Snippet, "crons per app") {
		t.Fatalf("snippet = %q", hits[0].Snippet)
	}
	for _, hit := range hits {
		if strings.HasPrefix(hit.Section, "cron-limits-are-not") {
			t.Fatalf("a fenced comment became a heading: %+v", hit)
		}
	}
	if decodeField[string](t, out, "retrieved_at") == "" || !strings.Contains(decodeField[string](t, out, "next"), "gregale_get_docs_page") {
		t.Fatalf("search envelope = %v", out)
	}

	hits = decodeField[[]docsHit](t, call("gregale_search_docs", map[string]any{"query": "canary", "limit": 10}, false), "results")
	perPage := map[string]int{}
	for _, hit := range hits {
		perPage[hit.Page]++
	}
	if len(hits) == 0 || hits[0].Page != "rollouts" || perPage["rollouts"] != docsResultsPerPage || perPage["plans"] == 0 {
		t.Fatalf("canary hits = %+v", hits)
	}

	hits = decodeField[[]docsHit](t, call("gregale_search_docs", map[string]any{"query": "how do I roll back"}, false), "results")
	if !slices.ContainsFunc(hits, func(hit docsHit) bool { return hit.Section == "use-gregale-rollback" }) {
		t.Fatalf("roll back did not find rollback: %+v", hits)
	}

	empty := call("gregale_search_docs", map[string]any{"query": "zzzz"}, false)
	if hits := decodeField[[]docsHit](t, empty, "results"); len(hits) != 0 || !strings.Contains(decodeField[string](t, empty, "next"), "/docs") {
		t.Fatalf("unmatched search = %v", empty)
	}

	for _, args := range []map[string]any{{"query": " "}, {"query": strings.Repeat("a", docsQueryMaxBytes+1)}, {"query": "cron", "limit": docsMaxResults + 1}} {
		if code := decodeField[string](t, call("gregale_search_docs", args, true), "code"); code != "invalid_argument" {
			t.Errorf("args %v: code %s", args, code)
		}
	}
}

func TestDocsPageReadsPagesAndSections(t *testing.T) {
	site := newDocsSite(t)
	call := connectDocsServer(t, site)

	out := call("gregale_get_docs_page", map[string]any{"page": "rollouts"}, false)
	markdown := decodeField[string](t, out, "markdown")
	if !strings.HasPrefix(markdown, "Roll out a new deployment") || strings.Contains(markdown, "Source:") || strings.Contains(markdown, "# Rollouts") || strings.Contains(markdown, "Key facts") {
		t.Fatalf("page markdown = %q", markdown)
	}
	headings := decodeField[[]docsHeadingView](t, out, "headings")
	want := []docsHeadingView{{"canary-presets", "Canary presets", 2}, {"abort-a-canary", "Abort a canary", 3}, {"watch-a-rollout", "Watch a rollout", 2}}
	if len(headings) != len(want) {
		t.Fatalf("headings = %+v", headings)
	}
	for i := range want {
		if headings[i] != want[i] {
			t.Fatalf("headings = %+v, want %+v", headings, want)
		}
	}
	if decodeField[string](t, out, "summary") != "Canary and staged rollouts." || decodeField[string](t, out, "url") != site.URL+"/docs/rollouts" {
		t.Fatalf("page envelope = %v", out)
	}

	cases := []struct {
		name, page, section, wantSection, contains, excludes string
	}{
		{"h2 keeps its h3", "rollouts", "canary-presets", "canary-presets", "### Abort a canary", "## Watch a rollout"},
		{"heading text", "rollouts", "Watch a rollout", "watch-a-rollout", "follow progress", "Canary presets"},
		{"url with anchor", docsSiteURL + "/docs/rollouts.md#abort-a-canary", "", "abort-a-canary", "gregale rollout abort", "Watch"},
		{"code in heading", "/docs/releases", "use-gregale-rollback", "use-gregale-rollback", "## Use `gregale rollback`", "Every deploy"},
	}
	for _, tc := range cases {
		args := map[string]any{"page": tc.page}
		if tc.section != "" {
			args["section"] = tc.section
		}
		out := call("gregale_get_docs_page", args, false)
		markdown := decodeField[string](t, out, "markdown")
		if got := decodeField[string](t, out, "section"); got != tc.wantSection || !strings.Contains(markdown, tc.contains) || strings.Contains(markdown, tc.excludes) {
			t.Errorf("%s: section %q markdown %q", tc.name, got, markdown)
		}
	}

	for _, tc := range []struct {
		args map[string]any
		code string
	}{
		{map[string]any{"page": "billing"}, "docs_page_not_found"},
		{map[string]any{"page": "rollouts", "section": "nope"}, "docs_section_not_found"},
		{map[string]any{"page": "https://example.com/docs/rollouts"}, "invalid_argument"},
		{map[string]any{"page": "../etc/passwd"}, "invalid_argument"},
	} {
		if code := decodeField[string](t, call("gregale_get_docs_page", tc.args, true), "code"); code != tc.code {
			t.Errorf("args %v: code %s, want %s", tc.args, code, tc.code)
		}
	}
}

func TestDocsLibraryCachesHourlyAndServesStale(t *testing.T) {
	site := newDocsSite(t)
	library := newDocsLibrary(site.URL, site.Client())
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	library.now = func() time.Time { return now }
	ctx := context.Background()

	for range 2 {
		if corpus, err := library.load(ctx); err != nil || corpus.stale {
			t.Fatalf("load: %v stale=%v", err, corpus != nil && corpus.stale)
		}
	}
	if site.fetches.Load() != 1 {
		t.Fatalf("fresh corpus refetched: %d fetches", site.fetches.Load())
	}

	site.failing.Store(true)
	now = now.Add(docsFreshFor + time.Minute)
	corpus, err := library.load(ctx)
	if err != nil || !corpus.stale || len(corpus.pages) != 3 {
		t.Fatalf("stale fallback: %v", err)
	}
	fetches := site.fetches.Load()
	if _, err := library.load(ctx); err != nil || site.fetches.Load() != fetches {
		t.Fatalf("a failed refresh was retried at once: %v", err)
	}

	now = now.Add(docsStaleFor)
	var input *inputError
	if _, err := library.load(ctx); !errors.As(err, &input) || input.code != "docs_unavailable" {
		t.Fatalf("expired corpus: %v", err)
	}

	site.failing.Store(false)
	now = now.Add(docsRetryAfter)
	if corpus, err := library.load(ctx); err != nil || corpus.stale {
		t.Fatalf("recovery: %v", err)
	}
}

func TestDocsCorpusRefusesMismatchedFiles(t *testing.T) {
	site := "https://gregale.dev"
	index := strings.ReplaceAll(docsIndexFixture, "SITE", site)
	if _, err := parseDocsCorpus(site, "# Gregale\n", docsFullFixture, time.Now()); err == nil {
		t.Fatal("an index without pages was accepted")
	}
	if _, err := parseDocsCorpus(site, index, strings.ReplaceAll(docsFullFixture, "Source: ", "Origin: "), time.Now()); err == nil {
		t.Fatal("a full file without page boundaries was accepted")
	}
	corpus, err := parseDocsCorpus(site, index, strings.ReplaceAll(docsFullFixture, "SITE", site), time.Now())
	if err != nil || len(corpus.pages) != 3 || corpus.bySlug["plans"].group != "Reference" {
		t.Fatalf("corpus: %v", err)
	}
}

func TestDocsAnchorMatchesSiteSlugs(t *testing.T) {
	for heading, want := range map[string]string{
		"Canary presets":            "canary-presets",
		"Use gregale rollback":      "use-gregale-rollback",
		"What's new?":               "whats-new",
		"Step 1:  build":            "step-1-build",
		"  --no-traffic releases  ": "--no-traffic-releases",
	} {
		if got := docsAnchor(heading); got != want {
			t.Errorf("docsAnchor(%q) = %q, want %q", heading, got, want)
		}
	}
}

func TestTruncateDocsMarkdownCutsAtLines(t *testing.T) {
	for _, tc := range []struct {
		in        string
		limit     int
		want      string
		truncated bool
	}{
		{"short", 10, "short", false},
		{"line one\nline two\nline three", 20, "line one\nline two", true},
		{"ééééé", 5, "éé", true},
	} {
		got, truncated := truncateDocsMarkdown(tc.in, tc.limit)
		if got != tc.want || truncated != tc.truncated {
			t.Errorf("truncate(%q, %d) = %q, %v", tc.in, tc.limit, got, truncated)
		}
	}
}
