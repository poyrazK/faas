// Command errorcodes-md generates docs/error-codes.md, the customer reference
// for the stable problem `code` values the API returns. Each entry comes from
// the places the code's problem is built: the HTTP status, the title and hint
// written there (the strings a customer receives) and the docs page it links
// to. A code no call site titles or hints takes them from its pkg/whycopy
// row, which is what such a problem carries to the customer.
// docs/error-codes-unpublished.txt leaves out the codes of areas whose
// docs are not published yet.
//
//	make error-codes-md
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/docsmarkers"
	"github.com/onebox-faas/faas/pkg/whycopy"
)

const (
	exclusionsPath = "docs/error-codes-unpublished.txt"
	catalogPath    = "docs/customer-pages.json"
	docsSite       = "https://gregale.dev/docs"

	maxTitles = 3
	maxHints  = 2
	maxLinks  = 4
	// More statuses than this read as noise; the entry says they vary.
	maxStatuses = 3
	// A code built with more distinct titles than this is a generic one
	// (validation_failed, capacity_unavailable): its titles describe the
	// operations, not the code, so none are listed.
	genericTitles = 5
	genericHints  = 3
)

var codeRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// whycopyRow looks up the customer explanation catalog; tests replace it.
var whycopyRow = whycopy.Lookup

func main() {
	if err := run(".", os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "errorcodes-md:", err)
		os.Exit(1)
	}
}

func run(root string, w io.Writer) error {
	ex, err := loadExclusions(filepath.Join(root, exclusionsPath))
	if err != nil {
		return err
	}
	pg, err := loadPages(root)
	if err != nil {
		return err
	}
	c, err := collect(root)
	if err != nil {
		return err
	}
	entries := aggregate(c, pg)
	if unused := ex.unused(entries); len(unused) > 0 {
		return fmt.Errorf("%s: entries match no code, remove them: %s", exclusionsPath, strings.Join(unused, ", "))
	}
	if bad := entryLinks(c, entries, ex); len(bad) > 0 {
		return fmt.Errorf("problems link entries this page does not list, so the link lands at the top of the page: %s", strings.Join(bad, ", "))
	}
	_, err = io.WriteString(w, render(entries, ex))
	return err
}

// entry is everything known about one code.
type entry struct {
	code     string
	statuses map[int]int
	titles   map[string]int
	hints    map[string]int
	links    map[link]int
	// unpublished counts call-site links to docs pages that are not
	// published yet: a code that links only there belongs to an
	// unpublished area and is left out.
	unpublished int
	// mentioned is set when a published page names the code in backticks.
	mentioned bool
}

// hidden reports whether the entry stays off the page. A code a published
// page already names is always listed: the entry reveals nothing new.
func (e *entry) hidden(ex *exclusions) bool {
	if e.mentioned {
		return false
	}
	return ex.excluded(e.code) || (e.unpublished > 0 && len(e.links) == 0)
}

type link struct{ target, title string }

func aggregate(c *collection, pg pages) map[string]*entry {
	entries := map[string]*entry{}
	get := func(code string) *entry {
		e := entries[code]
		if e == nil {
			e = &entry{code: code, statuses: map[int]int{}, titles: map[string]int{}, hints: map[string]int{}, links: map[link]int{}}
			entries[code] = e
		}
		return e
	}
	for _, name := range c.apiCodes {
		if code, ok := c.resolve(c.consts[c.apiPath()][name]); ok && codeRE.MatchString(code) {
			get(code)
		}
	}
	for _, s := range c.sites {
		code, ok := c.resolve(s.code)
		if !ok || !codeRE.MatchString(code) {
			continue
		}
		e := get(code)
		if st := customerStatus(s.status); st != 0 {
			e.statuses[st]++
		}
		if t, ok := customerText(c, s.title); ok {
			e.titles[t]++
		}
		for _, h := range s.hints {
			if t, ok := customerText(c, h); ok {
				e.hints[t]++
			}
		}
		for _, d := range s.docs {
			if u, ok := c.resolve(d); ok {
				if l, ok := pg.link(u); ok {
					e.links[l]++
				} else if pg.unpublishedRoute(u) {
					e.unpublished++
				}
			}
		}
	}
	// A code no resolved call site names a status for falls back to the
	// API's own table, except for its catch-all 500.
	for code, e := range entries {
		if len(e.statuses) == 0 {
			if st := customerStatus(api.StatusForCode(code)); st != 0 && st != 500 {
				e.statuses[st] = 0
			}
		}
		// A code no call site titles or hints is built away from apid (a
		// build, deploy or gateway failure) and reaches customers through
		// its whycopy row, which Decorate copies onto the problem.
		if row, ok := whycopyRow(code); ok {
			if len(e.titles) == 0 && row.Title != "" {
				e.titles[row.Title] = 0
			}
			if len(e.hints) == 0 && row.Hint != "" {
				e.hints[row.Hint] = 0
			}
		}
		// Pages that name the code in backticks explain it.
		for _, l := range pg.mentioning(code) {
			e.mentioned = true
			if _, ok := e.links[l]; !ok {
				e.links[l] = 0
			}
		}
	}
	return entries
}

// generatedRoute is the docs route this command's page is served at.
const generatedRoute = "error-codes"

// entryLinks names the problems whose docs link points at an entry of this
// page that it does not list: a code left out, or no code at all. The API's
// errors#<code> links pointed at headings errors.md never had.
func entryLinks(c *collection, entries map[string]*entry, ex *exclusions) []string {
	var bad []string
	seen := map[string]bool{}
	for _, s := range c.sites {
		code, ok := c.resolve(s.code)
		if !ok {
			continue
		}
		for _, d := range s.docs {
			u, ok := c.resolve(d)
			if !ok {
				continue
			}
			path, anchor, ok := route(u)
			if !ok || path != generatedRoute || anchor == "" {
				continue
			}
			if e := entries[anchor]; e != nil && !e.hidden(ex) {
				continue
			}
			if msg := fmt.Sprintf("%s → %s#%s", code, generatedRoute, anchor); !seen[msg] {
				seen[msg] = true
				bad = append(bad, msg)
			}
		}
	}
	sort.Strings(bad)
	return bad
}

// customerStatus is the status a customer receives for a problem built with
// st. The public edge sends Gregale's own 502 and 504 problems as 503 so that
// Cloudflare passes the body through (see errors.md).
func customerStatus(st int) int {
	switch {
	case st == 502 || st == 504:
		return 503
	case st >= 400 && st < 600:
		return st
	}
	return 0
}

// customerText resolves s and returns it when it is fit for a customer page:
// one line, and free of operator and engineering material.
func customerText(c *collection, s strExpr) (string, bool) {
	v, ok := c.resolve(s)
	if !ok {
		return "", false
	}
	// A literal written for fmt keeps its escaped percent sign.
	v = strings.ReplaceAll(strings.Join(strings.Fields(v), " "), "%%", "%")
	if v == "" || len(docsmarkers.Internal(v)) > 0 {
		return "", false
	}
	return v, true
}

// exclusions are the codes and code prefixes left off the page.
type exclusions struct {
	exact    map[string]bool
	prefixes []string
}

func loadExclusions(name string) (*exclusions, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	ex := &exclusions{exact: map[string]bool{}}
	for i, raw := range strings.Split(string(b), "\n") {
		line := i + 1
		text, _, _ := strings.Cut(raw, "#")
		text = strings.TrimSpace(text)
		switch {
		case text == "":
		case strings.HasSuffix(text, "*") && codeRE.MatchString(strings.TrimSuffix(text, "*")):
			ex.prefixes = append(ex.prefixes, strings.TrimSuffix(text, "*"))
		case codeRE.MatchString(text):
			ex.exact[text] = true
		default:
			return nil, fmt.Errorf("%s:%d: %q is neither a code nor a prefix ending in *", name, line, text)
		}
	}
	return ex, nil
}

func (x *exclusions) excluded(code string) bool {
	if x.exact[code] {
		return true
	}
	for _, p := range x.prefixes {
		if strings.HasPrefix(code, p) {
			return true
		}
	}
	return false
}

// unused names the entries that match no code, so the list cannot rot.
func (x *exclusions) unused(entries map[string]*entry) []string {
	var out []string
	for code := range x.exact {
		if entries[code] == nil {
			out = append(out, code)
		}
	}
	for _, p := range x.prefixes {
		matched := false
		for code := range entries {
			if strings.HasPrefix(code, p) {
				matched = true
				break
			}
		}
		if !matched {
			out = append(out, p+"*")
		}
	}
	sort.Strings(out)
	return out
}

// pages are the published docs routes, keyed by path, and the paths of the
// unpublished ones.
type pages struct {
	published   map[string]page
	unpublished map[string]bool
}

type page struct{ source, title, text string }

// generatedPage is this command's own output: it names every code, so it is
// no evidence that a page explains one.
const generatedPage = "docs/error-codes.md"

// errorsPage is the hand-written errors guide. The API links to anchors on
// it that are this page's entries, so links to it are replaced by the
// mention link when the guide names the code.
const errorsPage = "docs/errors.md"

func loadPages(root string) (pages, error) {
	b, err := os.ReadFile(filepath.Join(root, catalogPath))
	if err != nil {
		return pages{}, err
	}
	var cat struct {
		Routes []struct {
			Path        string `json:"path"`
			Source      string `json:"source"`
			Unpublished string `json:"unpublished"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(b, &cat); err != nil {
		return pages{}, fmt.Errorf("%s: %w", catalogPath, err)
	}
	pg := pages{published: map[string]page{}, unpublished: map[string]bool{}}
	for _, r := range cat.Routes {
		if r.Unpublished != "" {
			pg.unpublished[r.Path] = true
			continue
		}
		if r.Source == "" {
			continue
		}
		if r.Source == generatedPage {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, r.Source))
		if err != nil {
			return pages{}, err
		}
		title, err := firstHeading(r.Source, string(b))
		if err != nil {
			return pages{}, err
		}
		pg.published[r.Path] = page{source: r.Source, title: title, text: string(b)}
	}
	return pg, nil
}

func firstHeading(name, text string) (string, error) {
	for _, line := range strings.Split(text, "\n") {
		if t, ok := strings.CutPrefix(line, "# "); ok {
			return strings.TrimSpace(t), nil
		}
	}
	return "", fmt.Errorf("%s: no # heading", name)
}

// route returns the docs route a gregale.dev docs URL names, and its anchor.
func route(u string) (path, anchor string, ok bool) {
	rest, ok := strings.CutPrefix(u, docsSite)
	if !ok || (rest != "" && rest[0] != '/' && rest[0] != '#') {
		return "", "", false
	}
	path, anchor, _ = strings.Cut(rest, "#")
	return strings.Trim(path, "/"), anchor, true
}

func (pg pages) unpublishedRoute(u string) bool {
	path, _, ok := route(u)
	return ok && pg.unpublished[path]
}

// link maps a gregale.dev docs URL to the published page it names.
func (pg pages) link(u string) (link, bool) {
	path, anchor, ok := route(u)
	if !ok {
		return link{}, false
	}
	p, ok := pg.published[path]
	if !ok || p.source == errorsPage {
		return link{}, false
	}
	target := strings.TrimPrefix(p.source, "docs/")
	if anchor != "" {
		target += "#" + anchor
	}
	return link{target: target, title: p.title}, true
}

// mentioning returns the pages that name code in backticks, in source order.
func (pg pages) mentioning(code string) []link {
	needle := "`" + code + "`"
	var out []link
	seen := map[string]bool{}
	for _, p := range pg.published {
		if seen[p.source] || !strings.Contains(p.text, needle) {
			continue
		}
		seen[p.source] = true
		out = append(out, link{target: strings.TrimPrefix(p.source, "docs/"), title: p.title})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].target < out[j].target })
	return out
}

func render(entries map[string]*entry, ex *exclusions) string {
	var codes []string
	for code, e := range entries {
		if !e.hidden(ex) {
			codes = append(codes, code)
		}
	}
	sort.Strings(codes)

	var b strings.Builder
	b.WriteString("# Error code reference\n\n")
	b.WriteString("Every error Gregale returns carries a stable `code`. [Errors](errors.md) explains how an error is shaped and what to do about the most common ones. ")
	b.WriteString("This page lists each code with the HTTP status Gregale sends it with, the title and hint Gregale writes for it, and the docs page it points to. ")
	b.WriteString("A real response's `detail` adds the specifics, such as the observed value, the limit or the field that failed.\n\n")
	b.WriteString("The list is generated from the code that builds each error, so a new code appears here when it ships. ")
	b.WriteString("Codes for features whose docs are not published yet are left out. ")
	b.WriteString("Each code's name is its anchor: link to `error-codes#plan_limit_apps`, for example.\n\n")
	b.WriteString("If you are fixing a failure rather than looking up one code, start with [the common codes](errors.md#common-codes), which group the frequent ones by what went wrong, ")
	b.WriteString("or [Troubleshooting](troubleshooting.md), which goes by symptom.\n")

	var letters []string
	for _, code := range codes {
		if l := strings.ToUpper(code[:1]); len(letters) == 0 || letters[len(letters)-1] != l {
			letters = append(letters, l)
		}
	}
	if len(letters) > 0 {
		jump := make([]string, len(letters))
		for i, l := range letters {
			jump[i] = fmt.Sprintf("[%s](#%s)", l, strings.ToLower(l))
		}
		fmt.Fprintf(&b, "\nJump to: %s\n", strings.Join(jump, " · "))
	}

	letter := ""
	for _, code := range codes {
		if l := strings.ToUpper(code[:1]); l != letter {
			letter = l
			fmt.Fprintf(&b, "\n## %s\n", letter)
		}
		writeEntry(&b, entries[code])
	}
	return b.String()
}

func writeEntry(b *strings.Builder, e *entry) {
	fmt.Fprintf(b, "\n### `%s`\n\n", e.code)

	titles := ranked(e.titles)
	generic := len(titles) > genericTitles
	var head []string
	if st := statusText(e.statuses); st != "" {
		head = append(head, st)
	}
	switch {
	case generic:
		head = append(head, "Many operations send this code; the response's `title` and `detail` name the failure.")
	case len(titles) > 0:
		if len(titles) > maxTitles {
			titles = titles[:maxTitles]
		}
		for i, t := range titles {
			titles[i] = sentence(t)
		}
		head = append(head, escape(strings.Join(titles, "; ")))
	}
	if len(head) == 0 {
		head = append(head, "The response's `title` and `detail` describe the failure.")
	}
	b.WriteString(strings.Join(head, " · "))
	b.WriteString("\n")

	if generic {
		return
	}
	if hints := ranked(e.hints); len(hints) <= genericHints {
		if len(hints) > maxHints {
			hints = hints[:maxHints]
		}
		for _, h := range hints {
			fmt.Fprintf(b, "\n%s\n", escape(sentence(h)))
		}
	}

	links := rankedLinks(e.links)
	if len(links) > maxLinks {
		links = links[:maxLinks]
	}
	if len(links) > 0 {
		parts := make([]string, len(links))
		for i, l := range links {
			parts[i] = fmt.Sprintf("[%s](%s)", l.title, l.target)
		}
		fmt.Fprintf(b, "\nSee %s.\n", strings.Join(parts, ", "))
	}
}

// statusText names each status with its reason phrase ("429 Too Many
// Requests"), the words people search the reference with.
func statusText(statuses map[int]int) string {
	var sts []int
	for st := range statuses {
		sts = append(sts, st)
	}
	if len(sts) == 0 {
		return ""
	}
	if len(sts) > maxStatuses {
		return "HTTP status varies by operation"
	}
	sort.Ints(sts)
	parts := make([]string, len(sts))
	for i, st := range sts {
		parts[i] = strconv.Itoa(st)
		if reason := http.StatusText(st); reason != "" {
			parts[i] += " " + reason
		}
	}
	return "HTTP " + strings.Join(parts, " or ")
}

// ranked orders strings by how many call sites use them, then alphabetically.
// Strings that differ only in case count as one, written as the most used
// variant (a capitalized one on a tie).
func ranked(in map[string]int) []string {
	m := map[string]int{}
	best := map[string]string{}
	for s, n := range in {
		key := strings.ToLower(s)
		m[key] += n
		b, ok := best[key]
		if !ok || in[s] > in[b] || (in[s] == in[b] && s < b) {
			best[key] = s
		}
	}
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Slice(out, func(i, j int) bool {
		if m[out[i]] != m[out[j]] {
			return m[out[i]] > m[out[j]]
		}
		return out[i] < out[j]
	})
	for i, key := range out {
		out[i] = best[key]
	}
	return out
}

// rankedLinks orders links by how many call sites use them and keeps one per
// page, preferring the most used and then a section anchor over the bare page.
func rankedLinks(m map[link]int) []link {
	out := make([]link, 0, len(m))
	for l := range m {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool {
		if m[out[i]] != m[out[j]] {
			return m[out[i]] > m[out[j]]
		}
		ai, aj := strings.Contains(out[i].target, "#"), strings.Contains(out[j].target, "#")
		if ai != aj {
			return ai
		}
		return out[i].target < out[j].target
	})
	seen := map[string]bool{}
	kept := out[:0]
	for _, l := range out {
		page, _, _ := strings.Cut(l.target, "#")
		if !seen[page] {
			seen[page] = true
			kept = append(kept, l)
		}
	}
	return kept
}

// escape keeps call-site text from turning into HTML when rendered:
// placeholders such as <app> would otherwise vanish as unknown tags. Code
// spans are left alone, since Markdown shows them verbatim.
func escape(s string) string {
	parts := strings.Split(s, "`")
	for i := 0; i < len(parts); i += 2 {
		parts[i] = strings.ReplaceAll(parts[i], "<", "&lt;")
	}
	return strings.Join(parts, "`")
}

// sentence capitalizes a hint's first letter.
func sentence(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError || !unicode.IsLower(r) {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}
