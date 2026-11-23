package mcpmanagement

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Docs search lets a coding agent answer "how do I ... on Gregale" from the
// published customer docs instead of guessing. The corpus is what the site
// already serves to agents: /llms.txt names every published page (title,
// summary, table-of-contents section) and /llms-full.txt holds every page as
// Markdown, each ending with a "Source: <page URL>" line. Unpublished pages
// are in neither file, and results link to the anchors the site renders.
const (
	docsSiteURL        = "https://gregale.dev"
	docsFreshFor       = time.Hour
	docsStaleFor       = 24 * time.Hour
	docsRetryAfter     = time.Minute
	docsFetchTimeout   = 20 * time.Second
	docsIndexMaxBytes  = 1 << 20
	docsFullMaxBytes   = 16 << 20
	docsQueryMaxBytes  = 200
	docsPageArgMax     = 512
	docsDefaultResults = 5
	docsMaxResults     = 10
	docsResultsPerPage = 2
	docsSnippetBytes   = 240
	docsPageMaxBytes   = 48 << 10
)

func registerDocs(s *mcp.Server, m *management) {
	register(s, m, "gregale_search_docs", "Search the published Gregale docs for how to use the CLI, deploys, configuration, limits and errors. Returns ranked sections with links; read one with gregale_get_docs_page.", m.searchDocs)
	register(s, m, "gregale_get_docs_page", "Read a published Gregale docs page, or one section of it, as Markdown. Pass page and section from gregale_search_docs.", m.getDocsPage)
}

type docsSearchArgs struct {
	Query string `json:"query" jsonschema:"What to look up, e.g. canary rollout or cron limits"`
	Limit int    `json:"limit,omitempty" jsonschema:"Results from 1 to 10; default 5"`
}

type docsPageArgs struct {
	Page    string `json:"page" jsonschema:"Page slug from gregale_search_docs, or a gregale.dev/docs URL"`
	Section string `json:"section,omitempty" jsonschema:"Section anchor or heading; omit for the whole page"`
}

type docsHit struct {
	Page    string `json:"page"`
	Title   string `json:"title"`
	Section string `json:"section,omitempty"`
	Heading string `json:"heading,omitempty"`
	// URL is the page on the docs site; empty for a preview page, which the
	// site does not serve: read it with gregale_get_docs_page.
	URL     string `json:"url,omitempty"`
	Preview bool   `json:"preview,omitempty"`
	Snippet string `json:"snippet"`
}

type docsSearchView struct {
	Query       string    `json:"query"`
	Results     []docsHit `json:"results"`
	RetrievedAt time.Time `json:"retrieved_at"`
	Stale       bool      `json:"stale,omitempty"`
	Next        string    `json:"next"`
}

type docsHeadingView struct {
	Section string `json:"section"`
	Heading string `json:"heading"`
	Level   int    `json:"level"`
}

type docsPageView struct {
	Page        string            `json:"page"`
	Title       string            `json:"title"`
	Summary     string            `json:"summary,omitempty"`
	URL         string            `json:"url,omitempty"`
	Preview     bool              `json:"preview,omitempty"`
	Section     string            `json:"section,omitempty"`
	Markdown    string            `json:"markdown"`
	Headings    []docsHeadingView `json:"headings"`
	Truncated   bool              `json:"truncated,omitempty"`
	RetrievedAt time.Time         `json:"retrieved_at"`
	Stale       bool              `json:"stale,omitempty"`
	Next        string            `json:"next,omitempty"`
}

func (m *management) searchDocs(ctx context.Context, args docsSearchArgs) (any, error) {
	query := strings.TrimSpace(args.Query)
	if query == "" || len(query) > docsQueryMaxBytes || !utf8.ValidString(query) {
		return nil, invalid("invalid_argument", fmt.Sprintf("query must be 1 to %d bytes of text", docsQueryMaxBytes))
	}
	limit := args.Limit
	if limit == 0 {
		limit = docsDefaultResults
	}
	if limit < 1 || limit > docsMaxResults {
		return nil, invalid("invalid_argument", fmt.Sprintf("limit must be from 1 to %d", docsMaxResults))
	}
	corpus, err := m.opts.docs.load(ctx)
	if err != nil {
		return nil, err
	}
	view := docsSearchView{Query: query, Results: corpus.search(query, limit, m.docsVisibility(ctx, corpus)), RetrievedAt: corpus.fetchedAt.UTC(), Stale: corpus.stale}
	view.Next = "Read a result with gregale_get_docs_page using its page and section."
	if len(view.Results) == 0 {
		view.Next = "No published page matched; try other words, or browse " + corpus.site + "/docs."
	}
	return view, nil
}

func (m *management) getDocsPage(ctx context.Context, args docsPageArgs) (any, error) {
	slug, anchor, err := docsPageRef(args.Page)
	if err != nil {
		return nil, err
	}
	section := strings.TrimSpace(args.Section)
	if section == "" {
		section = anchor
	}
	if len(section) > docsPageArgMax {
		return nil, invalid("invalid_argument", "section is too long")
	}
	corpus, err := m.opts.docs.load(ctx)
	if err != nil {
		return nil, err
	}
	page := corpus.bySlug[slug]
	if page != nil && len(page.capabilities) > 0 && !m.docsVisibility(ctx, corpus)(page) {
		page = nil
	}
	if page == nil {
		return nil, invalid("docs_page_not_found", fmt.Sprintf("no published docs page %q; find pages with gregale_search_docs", slug))
	}
	view := docsPageView{Page: page.slug, Title: page.title, Summary: page.summary, URL: corpus.pageURL(page, ""), Preview: len(page.capabilities) > 0, RetrievedAt: corpus.fetchedAt.UTC(), Stale: corpus.stale, Headings: []docsHeadingView{}}
	for _, s := range page.sections[1:] {
		view.Headings = append(view.Headings, docsHeadingView{Section: s.anchor, Heading: s.heading, Level: s.level})
	}
	markdown := page.markdown()
	if section != "" {
		s := page.section(section)
		if s == nil {
			return nil, invalid("docs_section_not_found", fmt.Sprintf("page %q has no section %q; read the page without section to list its headings", page.slug, section))
		}
		view.Section, view.URL = s.anchor, corpus.pageURL(page, s.anchor)
		markdown = page.sectionMarkdown(s)
	}
	view.Markdown, view.Truncated = truncateDocsMarkdown(markdown, docsPageMaxBytes)
	if view.Truncated {
		view.Next = "The Markdown was cut at " + fmt.Sprint(docsPageMaxBytes>>10) + " KiB; read one section at a time with section set to a headings entry."
	}
	return view, nil
}

var docsSlugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*(?:/[a-z0-9][a-z0-9_-]*)*$`)

// docsPageRef accepts a slug, /docs/<slug> or a gregale.dev/docs URL, with
// an optional .md suffix and #anchor.
func docsPageRef(raw string) (slug, anchor string, err error) {
	ref := strings.TrimSpace(raw)
	if ref == "" || len(ref) > docsPageArgMax {
		return "", "", invalid("invalid_argument", "page must be a docs page slug or URL")
	}
	if before, after, ok := strings.Cut(ref, "#"); ok {
		ref, anchor = before, after
	}
	if strings.Contains(ref, "://") {
		u, err := url.Parse(ref)
		if err != nil || (u.Host != "gregale.dev" && u.Host != "www.gregale.dev") {
			return "", "", invalid("invalid_argument", "page URLs must be on gregale.dev/docs")
		}
		ref = u.Path
	}
	ref = strings.TrimPrefix(ref, "/")
	ref = strings.TrimPrefix(ref, "docs/")
	ref = strings.TrimSuffix(strings.TrimSuffix(ref, "/"), ".md")
	if !docsSlugRE.MatchString(ref) {
		return "", "", invalid("invalid_argument", "page must be a docs page slug such as rollouts, or a gregale.dev/docs URL")
	}
	return ref, anchor, nil
}

func truncateDocsMarkdown(markdown string, limit int) (string, bool) {
	if len(markdown) <= limit {
		return markdown, false
	}
	cut := strings.LastIndexByte(markdown[:limit], '\n')
	if cut <= 0 {
		cut = limit
		for cut > 0 && !utf8.RuneStart(markdown[cut]) {
			cut--
		}
	}
	return markdown[:cut], true
}

// docsLibrary fetches and caches the corpus. One library is shared by every
// connection of a process, so a hosted bridge fetches the site at most once
// an hour; a failed refresh keeps serving the previous corpus, marked stale,
// for up to a day.
type docsLibrary struct {
	site   string
	client *http.Client
	now    func() time.Time
	// embedded supplies the pages shipped in this binary; nil searches the
	// site alone.
	embedded func() []*docsPage

	mu         sync.Mutex
	corpus     *docsCorpus
	failedAt   time.Time
	lastFailed error
	// offline is the embedded corpus built once for site outages; the index
	// never changes within a process.
	offline *docsCorpus
}

func newDocsLibrary(site string, client *http.Client) *docsLibrary {
	site = strings.TrimRight(site, "/")
	if client == nil {
		host := ""
		if u, err := url.Parse(site); err == nil {
			host = u.Host
		}
		client = &http.Client{Timeout: docsFetchTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Host != host || len(via) >= 3 {
				return errors.New("docs redirect refused")
			}
			return nil
		}}
	}
	return &docsLibrary{site: site, client: client, now: time.Now}
}

func (l *docsLibrary) load(ctx context.Context) (*docsCorpus, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if l.corpus != nil && now.Sub(l.corpus.fetchedAt) < docsFreshFor {
		return l.corpus, nil
	}
	err := l.lastFailed
	if l.failedAt.IsZero() || now.Sub(l.failedAt) >= docsRetryAfter {
		var corpus *docsCorpus
		if corpus, err = l.fetch(ctx); err == nil {
			l.corpus, l.failedAt, l.lastFailed = corpus, time.Time{}, nil
			return corpus, nil
		}
		if ctx.Err() == nil {
			l.failedAt, l.lastFailed = now, err
		}
	}
	if l.corpus != nil && now.Sub(l.corpus.fetchedAt) < docsStaleFor {
		stale := *l.corpus
		stale.stale = true
		return &stale, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	// The site is out of reach: the index shipped in this binary stands in.
	if l.offline == nil {
		if l.offline = embeddedDocsCorpus(l.site, l.embedded, now); l.offline != nil {
			l.offline.stale = true
		}
	}
	if l.offline != nil {
		return l.offline, nil
	}
	return nil, invalid("docs_unavailable", fmt.Sprintf("the Gregale docs could not be fetched (%v); retry in a minute or read %s/docs", err, l.site))
}

func (l *docsLibrary) fetch(ctx context.Context) (*docsCorpus, error) {
	index, err := l.get(ctx, "/llms.txt", docsIndexMaxBytes)
	if err != nil {
		return nil, err
	}
	full, err := l.get(ctx, "/llms-full.txt", docsFullMaxBytes)
	if err != nil {
		return nil, err
	}
	live, err := parseDocsPages(l.site, index, full)
	if err != nil {
		return nil, err
	}
	return buildDocsCorpus(l.site, mergeDocsPages(live, l.embedded), l.now()), nil
}

func (l *docsLibrary) get(ctx context.Context, path string, limit int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.site+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/plain, text/markdown;q=0.9")
	req.Header.Set("User-Agent", "gregale-mcp")
	resp, err := l.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: HTTP %d", path, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if int64(len(body)) > limit {
		return "", fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	if !utf8.Valid(body) {
		return "", fmt.Errorf("%s is not UTF-8 text", path)
	}
	return strings.ReplaceAll(string(body), "\r\n", "\n"), nil
}

type docsCorpus struct {
	site      string
	pages     []*docsPage
	bySlug    map[string]*docsPage
	sections  []*docsSection
	docFreq   map[string]int
	avgLength float64
	fetchedAt time.Time
	stale     bool
}

type docsPage struct {
	slug, title, summary, group string
	lines                       []string
	// capabilities is set on a preview page from the embedded index: it is
	// shown only to an account for which one of them is available.
	capabilities []string
	// weight scales the page's scores; zero means 1. Command search uses it
	// to favour curated and common entries.
	weight     float64
	titleTerms map[string]bool
	// sections[0] is the introduction above the first heading.
	sections []*docsSection
}

type docsSection struct {
	page         *docsPage
	order        int
	heading      string
	anchor       string
	level        int
	start, end   int // line range in page.lines, heading line included
	text         string
	lower        string
	termFreq     map[string]int
	headingTerms map[string]bool
	length       int
}

var (
	docsIndexEntryRE = regexp.MustCompile(`^- \[(.+)\]\((\S+)\)(?::\s*(.*))?$`)
	docsHeadingRE    = regexp.MustCompile(`^(#{2,3})\s+(.+?)\s*$`)
	docsFenceRE      = regexp.MustCompile("^\\s*(`{3,}|~{3,})")
	docsLinkRE       = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	docsAnchorDropRE = regexp.MustCompile(`[^\w\s-]`)
	docsSpaceRE      = regexp.MustCompile(`\s+`)
	docsMarkerRE     = regexp.MustCompile(`(?m)^\s*(?:#+|>+)\s?`)
)

// parseDocsCorpus reads the site's agent files. A page missing from
// llms-full.txt stays searchable by its title and summary; a corpus whose
// pages mostly fail to match is refused, because the format changed.
func parseDocsCorpus(site, index, full string, fetchedAt time.Time) (*docsCorpus, error) {
	pages, err := parseDocsPages(site, index, full)
	if err != nil {
		return nil, err
	}
	return buildDocsCorpus(site, pages, fetchedAt), nil
}

// parseDocsPages reads the site's agent files into pages.
func parseDocsPages(site, index, full string) ([]*docsPage, error) {
	c := &docsCorpus{site: site, bySlug: map[string]*docsPage{}}
	prefix, group := site+"/docs/", ""
	for _, line := range strings.Split(index, "\n") {
		if strings.HasPrefix(line, "## ") {
			group = strings.TrimSpace(line[3:])
			continue
		}
		m := docsIndexEntryRE.FindStringSubmatch(line)
		if m == nil || !strings.HasPrefix(m[2], prefix) || !strings.HasSuffix(m[2], ".md") {
			continue
		}
		slug := strings.TrimSuffix(strings.TrimPrefix(m[2], prefix), ".md")
		if !docsSlugRE.MatchString(slug) || c.bySlug[slug] != nil {
			continue
		}
		page := &docsPage{slug: slug, title: strings.TrimSpace(m[1]), summary: strings.TrimSpace(m[3]), group: group}
		c.pages = append(c.pages, page)
		c.bySlug[slug] = page
	}
	if len(c.pages) == 0 {
		return nil, errors.New("llms.txt names no docs pages")
	}
	matched := 0
	var chunk []string
	for _, line := range strings.Split(full, "\n") {
		if rest, ok := strings.CutPrefix(line, "Source: "+prefix); ok {
			if page := c.bySlug[strings.TrimSpace(rest)]; page != nil && page.lines == nil {
				page.lines = docsPageBody(chunk, page.title)
				matched++
				chunk = nil
				continue
			}
		}
		chunk = append(chunk, line)
	}
	if matched*2 < len(c.pages) {
		return nil, fmt.Errorf("llms-full.txt matches %d of the %d pages llms.txt names", matched, len(c.pages))
	}
	return c.pages, nil
}

// buildDocsCorpus indexes pages for search. A slug seen twice keeps its
// first page.
func buildDocsCorpus(site string, pages []*docsPage, fetchedAt time.Time) *docsCorpus {
	c := &docsCorpus{site: site, bySlug: map[string]*docsPage{}, docFreq: map[string]int{}, fetchedAt: fetchedAt}
	for _, page := range pages {
		if c.bySlug[page.slug] == nil {
			c.pages = append(c.pages, page)
			c.bySlug[page.slug] = page
		}
	}
	total := 0
	for _, page := range c.pages {
		page.titleTerms = docsTermSet(page.title)
		page.sections = splitDocsSections(page)
		for _, s := range page.sections {
			s.order = len(c.sections)
			c.sections = append(c.sections, s)
			total += s.length
			seen := map[string]bool{}
			for term := range s.termFreq {
				seen[term] = true
			}
			for term := range s.headingTerms {
				seen[term] = true
			}
			for term := range page.titleTerms {
				seen[term] = true
			}
			for term := range seen {
				c.docFreq[term]++
			}
		}
	}
	c.avgLength = math.Max(1, float64(total)/float64(max(1, len(c.sections))))
	return c
}

// docsPageBody drops what precedes the page's "# Title" and "> summary"
// lines (the intro, on the first page) and those lines themselves.
func docsPageBody(chunk []string, title string) []string {
	for i, line := range chunk {
		if line != "# "+title {
			continue
		}
		j := i + 1
		for j < len(chunk) && strings.TrimSpace(chunk[j]) == "" {
			j++
		}
		if j < len(chunk) && strings.HasPrefix(chunk[j], "> ") {
			chunk = chunk[j+1:]
			break
		}
	}
	for len(chunk) > 0 && strings.TrimSpace(chunk[0]) == "" {
		chunk = chunk[1:]
	}
	for len(chunk) > 0 && strings.TrimSpace(chunk[len(chunk)-1]) == "" {
		chunk = chunk[:len(chunk)-1]
	}
	return append([]string{}, chunk...)
}

// splitDocsSections cuts a page at its level-2 and level-3 headings outside
// fenced code, as the site's table of contents does.
func splitDocsSections(page *docsPage) []*docsSection {
	intro := &docsSection{page: page, level: 1}
	sections := []*docsSection{intro}
	fence := ""
	for i, line := range page.lines {
		if marker := docsFenceRE.FindStringSubmatch(line); marker != nil {
			if fence == "" {
				fence = marker[1]
			} else if marker[1][0] == fence[0] && len(marker[1]) >= len(fence) {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if m := docsHeadingRE.FindStringSubmatch(line); m != nil {
			heading := docsLinkRE.ReplaceAllString(strings.ReplaceAll(m[2], "`", ""), "$1")
			sections = append(sections, &docsSection{page: page, heading: heading, anchor: docsAnchor(heading), level: len(m[1]), start: i})
		}
	}
	for i, s := range sections {
		s.end = len(page.lines)
		if i+1 < len(sections) {
			s.end = sections[i+1].start
		}
		body := page.lines[s.start:s.end]
		if s.level > 1 && len(body) > 0 {
			body = body[1:]
		}
		text := strings.Join(body, "\n")
		if s.level == 1 {
			text = page.summary + "\n" + text
		}
		s.text = docsPlainText(text)
		s.lower = strings.ToLower(s.text)
		s.termFreq = map[string]int{}
		for _, term := range docsTerms(s.text) {
			s.termFreq[term]++
			s.length++
		}
		s.headingTerms = docsTermSet(s.heading)
	}
	return sections
}

// docsAnchor matches the site's GitHub-compatible heading ids.
func docsAnchor(heading string) string {
	return docsSpaceRE.ReplaceAllString(docsAnchorDropRE.ReplaceAllString(strings.ToLower(strings.TrimSpace(heading)), ""), "-")
}

func docsPlainText(markdown string) string {
	text := docsLinkRE.ReplaceAllString(markdown, "$1")
	text = docsMarkerRE.ReplaceAllString(text, "")
	text = strings.NewReplacer("`", "", "**", "", "|", " ").Replace(text)
	return strings.TrimSpace(docsSpaceRE.ReplaceAllString(text, " "))
}

var docsStopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true, "can": true,
	"do": true, "does": true, "for": true, "from": true, "how": true, "i": true, "in": true, "is": true, "it": true,
	"my": true, "of": true, "on": true, "or": true, "that": true, "the": true, "this": true, "to": true, "what": true,
	"when": true, "where": true, "which": true, "with": true, "you": true, "your": true,
}

func docsTerms(text string) []string {
	var terms []string
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		for _, part := range docsSplitDigits(word) {
			if !docsStopwords[part] {
				terms = append(terms, docsStem(part))
			}
		}
	}
	return terms
}

// docsSplitDigits splits a word where letters meet digits, so "python313"
// is found by "python" and "node24" by "node".
func docsSplitDigits(word string) []string {
	var parts []string
	start := 0
	for i, r := range word {
		if i > start && unicode.IsDigit(r) != unicode.IsDigit(rune(word[i-1])) {
			parts = append(parts, word[start:i])
			start = i
		}
	}
	return append(parts, word[start:])
}

// docsStem folds plurals so "deploys" finds "deploy" and "policies" finds
// "policy". Queries and pages are stemmed alike, so odd stems still match.
func docsStem(word string) string {
	switch {
	case len(word) > 4 && strings.HasSuffix(word, "ies"):
		return word[:len(word)-3] + "y"
	case len(word) > 3 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss"):
		return word[:len(word)-1]
	}
	return word
}

func docsTermSet(text string) map[string]bool {
	set := map[string]bool{}
	for _, term := range docsTerms(text) {
		set[term] = true
	}
	return set
}

// pageURL is the page on the docs site, or empty for a preview page: those
// come only from the index shipped in this binary and the site serves none.
func (c *docsCorpus) pageURL(page *docsPage, anchor string) string {
	if len(page.capabilities) > 0 {
		return ""
	}
	u := c.site + "/docs/" + page.slug
	if anchor != "" {
		u += "#" + anchor
	}
	return u
}

func (c *docsCorpus) idf(term string) float64 {
	df := float64(c.docFreq[term])
	if df == 0 {
		return 0
	}
	n := float64(len(c.sections))
	return math.Log(1 + (n-df+0.5)/(df+0.5))
}

type docsScored struct {
	section *docsSection
	score   float64
}

// search ranks sections with BM25 over the body plus fixed boosts for
// heading and page-title matches, favouring sections that contain every
// query word and the query as a phrase. Two adjacent words also match their
// joined form, so "roll back" finds "rollback". At most two sections per page.
func (c *docsCorpus) search(query string, limit int, visible func(*docsPage) bool) []docsHit {
	var words, terms []string
	covers := map[string]int{}
	for _, term := range docsTerms(query) {
		if covers[term] == 0 {
			covers[term] = 1
			words = append(words, term)
		}
	}
	if len(words) == 0 {
		return []docsHit{}
	}
	terms = append(terms, words...)
	for i := 0; i+1 < len(words); i++ {
		if joined := words[i] + words[i+1]; covers[joined] == 0 && c.docFreq[joined] > 0 {
			covers[joined] = 2
			terms = append(terms, joined)
		}
	}
	phrase := strings.Join(strings.Fields(strings.ToLower(query)), " ")
	codes := docsQueryCodes(query)
	const k1, b = 1.2, 0.75
	var scored []docsScored
	for _, s := range c.sections {
		if visible != nil && !visible(s.page) {
			continue
		}
		matched, score := 0, 0.0
		for _, term := range terms {
			idf := c.idf(term)
			weight := 0.0
			if tf := float64(s.termFreq[term]); tf > 0 {
				weight += tf * (k1 + 1) / (tf + k1*(1-b+b*float64(s.length)/c.avgLength))
			}
			if s.headingTerms[term] {
				weight += 2
			}
			if s.page.titleTerms[term] {
				weight += 1.5
			}
			if weight > 0 {
				matched += covers[term]
				score += idf * weight
			}
		}
		if matched == 0 {
			continue
		}
		coverage := math.Min(1, float64(matched)/float64(len(words)))
		score *= coverage * coverage
		if s.page.weight > 0 {
			score *= s.page.weight
		}
		if len(words) > 1 && (strings.Contains(s.lower, phrase) || strings.Contains(strings.ToLower(s.heading), phrase)) {
			score *= 1.5
		}
		if s.page.slug == docsCodeReference {
			if codes[s.anchor] {
				score *= docsCodeMatchBoost
			} else {
				score *= docsCodeReferenceWeight
			}
		}
		scored = append(scored, docsScored{s, score})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].section.order < scored[j].section.order
	})
	hits := []docsHit{}
	perPage := map[string]int{}
	for _, candidate := range scored {
		if len(hits) == limit {
			break
		}
		s := candidate.section
		if perPage[s.page.slug] == docsResultsPerPage {
			continue
		}
		perPage[s.page.slug]++
		hits = append(hits, docsHit{Page: s.page.slug, Title: s.page.title, Section: s.anchor, Heading: s.heading, URL: c.pageURL(s.page, s.anchor), Preview: len(s.page.capabilities) > 0, Snippet: c.snippet(s, terms)})
	}
	return hits
}

// docsCodeReference is the error code reference: one short section per
// code, headed by the code. Its hundreds of entries spell ordinary words
// ("invalid_canary_preset"), so on a question that names no code they would
// crowd out the guides; they count at docsCodeReferenceWeight unless the
// query names the entry's code, which puts that entry first.
const (
	docsCodeReference       = "error-codes"
	docsCodeReferenceWeight = 0.5
	docsCodeMatchBoost      = 4
)

// docsQueryCodes are the query's snake_case words: error codes, as an agent
// pastes them from a response.
func docsQueryCodes(query string) map[string]bool {
	codes := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if strings.Contains(strings.Trim(word, "_"), "_") {
			codes[word] = true
		}
	}
	return codes
}

// snippet is a window of the section's text around its rarest query term.
func (c *docsCorpus) snippet(s *docsSection, terms []string) string {
	text := s.text
	if text == "" {
		text = s.page.summary
	}
	ordered := append([]string{}, terms...)
	sort.SliceStable(ordered, func(i, j int) bool { return c.idf(ordered[i]) > c.idf(ordered[j]) })
	position := -1
	if len(s.lower) == len(text) {
		for _, term := range ordered {
			if at := strings.Index(s.lower, term); at >= 0 {
				position = at
				break
			}
		}
	}
	start := 0
	if position > docsSnippetBytes/4 {
		start = position - docsSnippetBytes/4
		if space := strings.IndexByte(text[start:position], ' '); space >= 0 {
			start += space + 1
		}
	}
	end := len(text)
	if end-start > docsSnippetBytes {
		end = start + docsSnippetBytes
		if space := strings.LastIndexByte(text[start:end], ' '); space > 0 {
			end = start + space
		}
	}
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	for end < len(text) && end > start && !utf8.RuneStart(text[end]) {
		end--
	}
	out := text[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(text) {
		out += "…"
	}
	return out
}

func (p *docsPage) markdown() string { return strings.Join(p.lines, "\n") }

// section finds a heading by anchor, by the anchor its text would get, or
// by its text.
func (p *docsPage) section(ref string) *docsSection {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "#")
	for _, match := range []func(*docsSection) bool{
		func(s *docsSection) bool { return s.anchor == ref },
		func(s *docsSection) bool { return s.anchor == docsAnchor(ref) },
		func(s *docsSection) bool { return strings.EqualFold(s.heading, ref) },
	} {
		for _, s := range p.sections[1:] {
			if match(s) {
				return s
			}
		}
	}
	return nil
}

// sectionMarkdown is a heading and everything under it up to the next
// heading of the same or a higher level.
func (p *docsPage) sectionMarkdown(s *docsSection) string {
	end := len(p.lines)
	for _, next := range p.sections {
		if next.start > s.start && next.level <= s.level {
			end = next.start
			break
		}
	}
	return strings.TrimRight(strings.Join(p.lines[s.start:end], "\n"), "\n")
}
