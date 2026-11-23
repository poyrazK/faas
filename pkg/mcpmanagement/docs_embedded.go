// adr: 20261123180000
package mcpmanagement

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/docsindex"
)

// Docs search reads the live site and the index shipped in this binary
// (pkg/docsindex). A page the site serves wins: it carries the site's
// hand-written summary and any change published after this binary was
// built. The embedded index adds the pages the site lacks, and stands in
// for the whole site when it is out of reach. A preview page comes only
// from the embedded index and is shown only to an account for which one of
// its capabilities is available.

// embeddedDocsPages converts the shipped index; each call returns fresh
// pages, because building a corpus indexes them in place.
func embeddedDocsPages() []*docsPage {
	pages, err := docsindex.Pages()
	if err != nil {
		return nil
	}
	out := make([]*docsPage, 0, len(pages))
	for _, p := range pages {
		group := "Docs"
		if p.Preview() {
			group = "Preview"
		}
		out = append(out, &docsPage{slug: p.Slug, title: p.Title, summary: p.Summary, group: group,
			lines: strings.Split(p.Body, "\n"), capabilities: p.Capabilities})
	}
	return out
}

// mergeDocsPages appends the embedded pages the live site lacks; embedded
// nil adds none.
func mergeDocsPages(live []*docsPage, embedded func() []*docsPage) []*docsPage {
	if embedded == nil {
		return live
	}
	seen := make(map[string]bool, len(live))
	for _, p := range live {
		seen[p.slug] = true
	}
	out := append([]*docsPage{}, live...)
	for _, p := range embedded() {
		if !seen[p.slug] {
			out = append(out, p)
		}
	}
	return out
}

// embeddedDocsCorpus is the shipped index alone, for when the site is out
// of reach; nil when there is none.
func embeddedDocsCorpus(site string, embedded func() []*docsPage, at time.Time) *docsCorpus {
	if embedded == nil {
		return nil
	}
	pages := embedded()
	if len(pages) == 0 {
		return nil
	}
	return buildDocsCorpus(site, pages, at)
}

func (c *docsCorpus) hasPreview() bool {
	for _, p := range c.pages {
		if len(p.capabilities) > 0 {
			return true
		}
	}
	return false
}

// previewVisibility shows every published page, and a preview page when one
// of its capabilities is not in unavailable. Without a capability answer
// (ok false) preview pages stay hidden.
func previewVisibility(unavailable map[string]bool, ok bool) func(*docsPage) bool {
	return func(p *docsPage) bool {
		if len(p.capabilities) == 0 {
			return true
		}
		if !ok {
			return false
		}
		for _, id := range p.capabilities {
			if !unavailable[id] {
				return true
			}
		}
		return false
	}
}

// docsVisibility asks the account's capabilities only when the corpus holds
// preview pages.
func (m *management) docsVisibility(ctx context.Context, corpus *docsCorpus) func(*docsPage) bool {
	if !corpus.hasPreview() {
		return nil
	}
	unavailable, ok := m.unavailableCapabilities(ctx)
	return previewVisibility(unavailable, ok)
}

// --- the CLI's search (`gregale guide "<text>"`) ---------------------------

// DocsHit is one docs search result.
type DocsHit = docsHit

const (
	cliDocsCacheTTL     = 24 * time.Hour
	cliDocsFetchTimeout = 3 * time.Second
	cliDocsIndexFile    = "llms.txt"
	cliDocsFullFile     = "llms-full.txt"
)

// LocalDocsOptions configures SearchLocalDocs.
type LocalDocsOptions struct {
	// CacheDir keeps the live site's agent files for a day; empty skips the
	// live site and searches the shipped index alone.
	CacheDir string
	// Visible reports whether a preview page unlocked by these capabilities
	// shows for the account; nil hides every preview page.
	Visible func(capabilities []string) bool
	// Client fetches the live site; nil uses a short-timeout client.
	Client *http.Client
	// Site is the docs site; empty is https://gregale.dev.
	Site string
}

// SearchLocalDocs searches the shipped docs index merged with the live
// site's pages, read from a day-old cache or fetched within a few seconds.
// It never fails because the site is out of reach.
func SearchLocalDocs(ctx context.Context, query string, limit int, opts LocalDocsOptions) ([]DocsHit, error) {
	site := strings.TrimRight(opts.Site, "/")
	if site == "" {
		site = docsSiteURL
	}
	pages := liveDocsPagesCached(ctx, site, opts)
	corpus := buildDocsCorpus(site, mergeDocsPages(pages, embeddedDocsPages), time.Now())
	if len(corpus.pages) == 0 {
		return nil, errors.New("this gregale binary ships no docs index and the docs site is out of reach")
	}
	visible := func(p *docsPage) bool {
		return len(p.capabilities) == 0 || (opts.Visible != nil && opts.Visible(p.capabilities))
	}
	return corpus.search(query, limit, visible), nil
}

// liveDocsPagesCached reads the site's agent files from the cache, fetching
// them when the cache is missing or a day old. Any failure returns what the
// cache holds, or nothing.
func liveDocsPagesCached(ctx context.Context, site string, opts LocalDocsOptions) []*docsPage {
	if opts.CacheDir == "" {
		return nil
	}
	indexPath, fullPath := filepath.Join(opts.CacheDir, cliDocsIndexFile), filepath.Join(opts.CacheDir, cliDocsFullFile)
	fresh := false
	if info, err := os.Stat(fullPath); err == nil && time.Since(info.ModTime()) < cliDocsCacheTTL {
		fresh = true
	}
	if !fresh {
		client := opts.Client
		if client == nil {
			client = &http.Client{Timeout: cliDocsFetchTimeout}
		}
		library := newDocsLibrary(site, client)
		fetchCtx, cancel := context.WithTimeout(ctx, cliDocsFetchTimeout)
		index, err := library.get(fetchCtx, "/llms.txt", docsIndexMaxBytes)
		var full string
		if err == nil {
			full, err = library.get(fetchCtx, "/llms-full.txt", docsFullMaxBytes)
		}
		cancel()
		if err == nil {
			if _, perr := parseDocsPages(site, index, full); perr == nil && os.MkdirAll(opts.CacheDir, 0o700) == nil {
				_ = writeFileAtomic(indexPath, index)
				_ = writeFileAtomic(fullPath, full)
			}
		}
	}
	index, err1 := os.ReadFile(indexPath)
	full, err2 := os.ReadFile(fullPath)
	if err1 != nil || err2 != nil {
		return nil
	}
	pages, err := parseDocsPages(site, string(index), string(full))
	if err != nil {
		return nil
	}
	return pages
}

func writeFileAtomic(path, body string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".docs-*")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// --- command search ----------------------------------------------------------

// SearchEntry is one searchable thing that is not a docs page, such as a CLI
// command: Title is what a hit shows (the command), Body the text it is found
// by.
type SearchEntry struct {
	ID, Title, Summary, Body string
	// Weight scales the entry's score; zero means 1.
	Weight float64
}

// SearchIndex ranks SearchEntry values with the docs search's scoring.
type SearchIndex struct{ corpus *docsCorpus }

// NewSearchIndex indexes entries; IDs must be unique.
func NewSearchIndex(entries []SearchEntry) *SearchIndex {
	pages := make([]*docsPage, 0, len(entries))
	for _, e := range entries {
		pages = append(pages, &docsPage{slug: e.ID, title: e.Title, summary: e.Summary, lines: strings.Split(e.Body, "\n"), weight: e.Weight})
	}
	return &SearchIndex{corpus: buildDocsCorpus("", pages, time.Time{})}
}

// Search returns the IDs of the best entries, best first, one per entry.
func (i *SearchIndex) Search(query string, limit int) []string {
	var ids []string
	seen := map[string]bool{}
	for _, hit := range i.corpus.search(query, limit*docsResultsPerPage, nil) {
		if !seen[hit.Page] {
			seen[hit.Page] = true
			ids = append(ids, hit.Page)
		}
		if len(ids) == limit {
			break
		}
	}
	return ids
}
