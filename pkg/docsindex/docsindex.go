// adr: 20261123180000

// Package docsindex is the customer docs as the gregale CLI and the
// management MCP server ship them: every published page under docs/, and
// every page held back only because its capability is in preview, tagged
// with the capabilities that unlock it. Search reads it when the live
// site lacks a page (a preview page is never on the site) and when the site
// is out of reach. Internal and dark pages are never in it.
//
// index.json.gz is generated from docs/customer-pages.json, the pages and
// the product catalog; TestIndexIsCurrent fails when it is stale and
// UPDATE_DOCS_INDEX=1 rewrites it.
package docsindex

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/productcap"
)

//go:embed index.json.gz
var indexGz []byte

// Page is one docs page.
type Page struct {
	// Slug is the page's route under /docs.
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
	// Capabilities is set on a preview page only: the page may be shown to
	// an account for which at least one of them is available.
	Capabilities []string `json:"capabilities,omitempty"`
	// Body is the page's Markdown without its "# Title" line.
	Body string `json:"body"`
}

// Preview reports whether the page is held back from the public site.
func (p Page) Preview() bool { return len(p.Capabilities) > 0 }

type index struct {
	Pages []Page `json:"pages"`
}

var loaded = sync.OnceValues(func() ([]Page, error) { return decode(indexGz) })

// Pages returns the embedded pages, sorted by slug.
func Pages() ([]Page, error) { return loaded() }

func decode(gz []byte) ([]Page, error) {
	if len(gz) == 0 {
		return nil, nil
	}
	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, fmt.Errorf("docs index: %w", err)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("docs index: %w", err)
	}
	var idx index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("docs index: %w", err)
	}
	return idx.Pages, nil
}

type route struct {
	Path        string `json:"path"`
	Source      string `json:"source"`
	External    bool   `json:"external"`
	Unpublished string `json:"unpublished"`
}

// previewReason is the phrase customer-pages.json uses for a page held back
// only until its capability leaves preview ("preview capability; …", or
// "Data API is a preview capability; …"). Every other reason (internal,
// dark, roadmap) keeps the page out of the index.
const previewReason = "preview capability"

var (
	titleRE   = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)
	docsURLRE = regexp.MustCompile(`/docs/([a-z0-9][a-z0-9_/-]*)`)
)

// Build renders the index from a repository's docs directory and the
// product catalog. The output is deterministic.
func Build(docsDir string, catalog productcap.Catalog) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(docsDir, "customer-pages.json"))
	if err != nil {
		return nil, err
	}
	var manifest struct {
		Routes []route `json:"routes"`
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		return nil, fmt.Errorf("customer-pages.json: %w", err)
	}
	capsByPage := map[string][]string{}
	known := map[string]bool{}
	for _, c := range catalog.Capabilities {
		known[c.ID] = true
		if m := docsURLRE.FindStringSubmatch(c.DocsURL); m != nil {
			capsByPage[m[1]] = append(capsByPage[m[1]], c.ID)
		}
	}
	// One page per source file; its shortest non-empty route stands for it.
	bySource := map[string]route{}
	for _, r := range manifest.Routes {
		if r.Source == "" || r.External || r.Path == "" {
			continue
		}
		if r.Unpublished != "" && !strings.Contains(r.Unpublished, previewReason) {
			continue
		}
		cur, ok := bySource[r.Source]
		if !ok || len(r.Path) < len(cur.Path) || (len(r.Path) == len(cur.Path) && r.Path < cur.Path) {
			bySource[r.Source] = r
		}
	}
	pages := make([]Page, 0, len(bySource))
	for source, r := range bySource {
		page, err := readPage(filepath.Join(docsDir, strings.TrimPrefix(source, "docs/")), r.Path)
		if err != nil {
			return nil, err
		}
		if r.Unpublished != "" {
			caps := append([]string{}, capsByPage[r.Path]...)
			if len(caps) == 0 && known[r.Path] {
				caps = []string{r.Path}
			}
			if len(caps) == 0 {
				continue // no capability can unlock it: keep it out
			}
			sort.Strings(caps)
			page.Capabilities = caps
		}
		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Slug < pages[j].Slug })
	raw, err := json.Marshal(index{Pages: pages})
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	w, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(raw); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func readPage(path, slug string) (Page, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Page{}, err
	}
	source := strings.ReplaceAll(string(b), "\r\n", "\n")
	m := titleRE.FindStringSubmatchIndex(source)
	if m == nil {
		return Page{}, fmt.Errorf("%s has no # title", path)
	}
	body := strings.TrimSpace(source[:m[0]] + source[m[1]:])
	return Page{Slug: slug, Title: source[m[2]:m[3]], Summary: summary(body), Body: body}, nil
}

const summaryMax = 200

// summary is the first sentence of the page's first prose paragraph: the
// site's hand-written summaries live in its own manifest.
func summary(body string) string {
	fence := false
	var para []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		if trimmed == "" {
			if len(para) > 0 {
				break
			}
			continue
		}
		if len(para) == 0 && strings.ContainsAny(trimmed[:1], "#>|-*<![") {
			continue
		}
		para = append(para, trimmed)
	}
	text := strings.Join(para, " ")
	text = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`).ReplaceAllString(text, "$1")
	text = strings.NewReplacer("`", "", "**", "").Replace(text)
	if end := strings.Index(text, ". "); end > 0 {
		text = text[:end+1]
	}
	if len(text) > summaryMax {
		cut := strings.LastIndexByte(text[:summaryMax], ' ')
		if cut < summaryMax/2 {
			cut = summaryMax
		}
		text = text[:cut] + "…"
	}
	return text
}
