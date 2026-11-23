// adr: 20261116183445
package mcpmanagement

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// docsEvalSiteEnv points the eval at a deployed site's /llms.txt and
// /llms-full.txt instead of the repository's docs:
//
//	GREGALE_DOCS_EVAL_SITE=https://gregale.dev go test -run TestDocsSearchRetrieval -v ./pkg/mcpmanagement
const docsEvalSiteEnv = "GREGALE_DOCS_EVAL_SITE"

type docsEvalFile struct {
	Floors struct {
		HitAt1 float64 `json:"hit_at_1"`
		HitAt5 float64 `json:"hit_at_5"`
		MRR    float64 `json:"mrr"`
	} `json:"floors"`
	Cases []struct {
		Query string   `json:"query"`
		Want  []string `json:"want"`
	} `json:"cases"`
}

// TestDocsSearchRetrieval scores gregale_search_docs on the questions in
// testdata/docs_search_eval.json against the published docs, as an agent
// sees them: the top docsDefaultResults results. Run it with -v for each
// miss and the scores.
func TestDocsSearchRetrieval(t *testing.T) {
	var eval docsEvalFile
	b, err := os.ReadFile(filepath.Join("testdata", "docs_search_eval.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &eval); err != nil {
		t.Fatalf("docs_search_eval.json: %v", err)
	}
	routes := docsEvalRoutes(t)
	corpus := docsEvalCorpus(t, routes)

	// A case must name sections that exist, so it cannot rot when a page
	// is renamed or a heading changes.
	sources := map[string]*docsPage{}
	for _, page := range corpus.pages {
		if src, ok := routes[page.slug]; ok {
			sources[src] = page
		}
	}
	for _, c := range eval.Cases {
		for _, want := range c.Want {
			stem, anchor, _ := strings.Cut(want, "#")
			page := sources[stem]
			if page == nil {
				t.Errorf("case %q wants %q: no published page has source docs/%s.md", c.Query, want, stem)
				continue
			}
			if anchor != "" && page.section(anchor) == nil {
				t.Errorf("case %q wants %q: docs/%s.md has no section with anchor %q", c.Query, want, stem, anchor)
			}
		}
	}

	if t.Failed() {
		t.FailNow()
	}

	var hit1, hit5, mrr float64
	var misses []string
	for _, c := range eval.Cases {
		hits := corpus.search(c.Query, docsDefaultResults, nil)
		rank := 0
		for i, h := range hits {
			if docsEvalMatches(routes[h.Page], h.Section, c.Want) {
				rank = i + 1
				break
			}
		}
		switch {
		case rank == 1:
			hit1++
			fallthrough
		case rank > 0:
			hit5++
			mrr += 1 / float64(rank)
		}
		if rank != 1 {
			var got []string
			for i, h := range hits {
				if i == 3 {
					break
				}
				got = append(got, routes[h.Page]+"#"+h.Section)
			}
			misses = append(misses, fmt.Sprintf("%-50q rank %d, want %s, got %s", c.Query, rank, strings.Join(c.Want, " | "), strings.Join(got, ", ")))
		}
	}
	n := float64(len(eval.Cases))
	hit1, hit5, mrr = hit1/n, hit5/n, mrr/n
	for _, m := range misses {
		t.Log(m)
	}
	t.Logf("%d cases: hit@1 %.3f  hit@5 %.3f  MRR@5 %.3f", len(eval.Cases), hit1, hit5, mrr)
	if os.Getenv(docsEvalSiteEnv) != "" {
		return // a deployed site may lag the repository; report only
	}
	for _, s := range []struct {
		name       string
		got, floor float64
	}{{"hit@1", hit1, eval.Floors.HitAt1}, {"hit@5", hit5, eval.Floors.HitAt5}, {"MRR@5", mrr, eval.Floors.MRR}} {
		if s.got+1e-9 < s.floor {
			t.Errorf("%s fell to %.3f, below its floor %.3f in testdata/docs_search_eval.json; the misses are logged above", s.name, s.got, s.floor)
		}
	}
}

// docsEvalMatches reports whether a result from the page with this source
// stem and section is one of want.
func docsEvalMatches(stem, section string, want []string) bool {
	for _, w := range want {
		wantStem, anchor, _ := strings.Cut(w, "#")
		if wantStem == stem && (anchor == "" || anchor == section) {
			return true
		}
	}
	return false
}

// docsEvalRoutes maps each published docs route to its source stem: the
// file under docs/ without .md. The site's slugs are routes.
func docsEvalRoutes(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "customer-pages.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cat struct {
		Routes []struct {
			Path        string `json:"path"`
			Source      string `json:"source"`
			External    bool   `json:"external"`
			Unpublished string `json:"unpublished"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(b, &cat); err != nil {
		t.Fatal(err)
	}
	routes := map[string]string{}
	for _, r := range cat.Routes {
		if r.Source == "" || r.External || r.Unpublished != "" || r.Path == "" {
			continue
		}
		routes[r.Path] = strings.TrimSuffix(strings.TrimPrefix(r.Source, "docs/"), ".md")
	}
	return routes
}

var docsEvalTitleRE = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)

// docsEvalCorpus is the corpus the search tool would build from the site.
// From the repository it renders the agent files as the site does: one
// entry per page, each page as "# Title", "> summary", the body without
// its own title, and a "Source:" line. The site's hand-written summaries
// live in its manifest, so pages here have none.
func docsEvalCorpus(t *testing.T, routes map[string]string) *docsCorpus {
	t.Helper()
	if site := os.Getenv(docsEvalSiteEnv); site != "" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		corpus, err := newDocsLibrary(site, nil).load(ctx)
		if err != nil {
			t.Fatalf("fetch %s: %v", site, err)
		}
		return corpus
	}
	const site = "https://gregale.dev"
	slugs := map[string]string{}
	for slug, stem := range routes {
		// One page per source; the shortest route stands for it.
		if cur, ok := slugs[stem]; !ok || len(slug) < len(cur) || (len(slug) == len(cur) && slug < cur) {
			slugs[stem] = slug
		}
	}
	stems := make([]string, 0, len(slugs))
	for stem := range slugs {
		stems = append(stems, stem)
	}
	sort.Strings(stems) // ties rank by page order
	var index, full strings.Builder
	index.WriteString("# Gregale\n\n## Docs\n\n")
	for _, stem := range stems {
		slug := slugs[stem]
		b, err := os.ReadFile(filepath.Join("..", "..", "docs", stem+".md"))
		if err != nil {
			t.Fatal(err)
		}
		source := strings.ReplaceAll(string(b), "\r\n", "\n")
		m := docsEvalTitleRE.FindStringSubmatchIndex(source)
		if m == nil {
			t.Fatalf("docs/%s.md has no # title", stem)
		}
		title := source[m[2]:m[3]]
		body := strings.TrimSpace(source[:m[0]] + source[m[1]:])
		fmt.Fprintf(&index, "- [%s](%s/docs/%s.md)\n", title, site, slug)
		fmt.Fprintf(&full, "# %s\n\n> \n\n%s\n\nSource: %s/docs/%s\n\n", title, body, site, slug)
	}
	corpus, err := parseDocsCorpus(site, index.String(), full.String(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return corpus
}
