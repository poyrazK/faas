// Package mcptoolscan is the tool safety scan for sold MCP servers
// (ADR-20261128135329): a deterministic, versioned rule set over the text an
// MCP server shows a model (tool, prompt and resource names, descriptions and
// schemas) that looks for prompt injection, hidden instructions and
// exfiltration. The same catalog always gives the same findings, so a verdict
// can be explained and reproduced.
package mcptoolscan

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// RuleSet is the version of the rules below. Bump it whenever a rule is
// added, removed or changed, so a stored scan names the rules that made it.
const RuleSet = 1

// Severities. High findings block a listing publish and a production deploy
// unless the seller records an audited override; medium and low ones warn.
const (
	SeverityHigh   = "high"
	SeverityMedium = "medium"
	SeverityLow    = "low"
)

// Kinds of catalog entries a finding can point at.
const (
	KindTool             = "tool"
	KindPrompt           = "prompt"
	KindResource         = "resource"
	KindResourceTemplate = "resource_template"
)

// Finding is one rule match.
type Finding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	// Path is where the text sits in the entry, such as /description or
	// /inputSchema/properties/query/description.
	Path string `json:"path"`
	// Excerpt quotes the matched text, with invisible and control characters
	// written as U+XXXX.
	Excerpt string `json:"excerpt,omitempty"`
}

// Report is the result of one scan.
type Report struct {
	RuleSet  int       `json:"rule_set"`
	Findings []Finding `json:"findings"`
	// Truncated is set when there were more than api.MCPToolScanFindingsMax
	// findings; the highest-severity ones are kept.
	Truncated bool `json:"truncated,omitempty"`
}

// High counts the high-severity findings.
func (r Report) High() int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == SeverityHigh {
			n++
		}
	}
	return n
}

// Rule describes one rule, for documentation and the owner view.
type Rule struct {
	ID       string
	Severity string
	Summary  string
}

// Rules lists the current rule set.
func Rules() []Rule {
	out := make([]Rule, 0, len(textRules)+3)
	out = append(out, Rule{ruleInvisible, SeverityHigh, "invisible, bidirectional or control characters that hide text from people"})
	for _, r := range textRules {
		out = append(out, Rule{r.id, r.severity, r.summary})
	}
	out = append(out,
		Rule{ruleCrossTool, SeverityMedium, "one entry tells the model how to use another tool"},
		Rule{ruleHarvestParam, SeverityMedium, "an input parameter asks for the conversation or system prompt"},
		Rule{ruleOversized, SeverityLow, fmt.Sprintf("text longer than %d bytes", api.MCPToolScanDescriptionMaxBytes)})
	return out
}

const (
	ruleInvisible    = "invisible_characters"
	ruleCrossTool    = "cross_tool_steering"
	ruleHarvestParam = "conversation_parameter"
	ruleOversized    = "oversized_text"
)

type textRule struct {
	id       string
	severity string
	summary  string
	re       *regexp.Regexp
}

// textRules run on normalized text: lower case, whitespace collapsed.
var textRules = []textRule{
	{"instruction_override", SeverityHigh, "text that tries to override the model's instructions",
		regexp.MustCompile(`\b(ignore|disregard|forget|override)\s+(all\s+|any\s+|every\s+)?(of\s+)?(the\s+|your\s+|my\s+)?(previous|prior|above|earlier|preceding|other|existing|original|system)\s+(instructions?|prompts?|rules|messages|directions|guidelines)\b|\byou\s+are\s+now\b|\bnew\s+instructions?\s*:|<\s*/?\s*(system|important|instructions?|admin|secret)\s*>|\[/?inst\]|<\|im_start\|>`)},
	{"concealment", SeverityHigh, "text that tells the model to hide something from the user",
		regexp.MustCompile(`\b(do\s+not|don't|never|without)\s+(tell|telling|mention|mentioning|inform|informing|reveal|revealing|notify|notifying|alert|alerting|show|showing|disclose|disclosing)\s+(this\s+|it\s+|that\s+|anything\s+)?(to\s+)?the\s+user\b|\b(hide|conceal|keep)\s+(this|it|that)\s+(secret\s+)?from\s+the\s+user\b|\bthe\s+user\s+(must|should|need)\s+not\s+(know|see|be\s+told)\b`)},
	{"sensitive_file", SeverityHigh, "a reference to credential or key files on the user's machine",
		regexp.MustCompile(`~/\.ssh\b|\bid_(rsa|dsa|ecdsa|ed25519)\b|\.aws/credentials\b|/etc/(passwd|shadow)\b|\.netrc\b|\.git-credentials\b|\.kube/config\b|\.docker/config\.json\b|\bclaude_desktop_config\.json\b|(^|[\s/"'])mcp\.json\b`)},
	{"exfiltration", SeverityHigh, "text that tells the model to send data to a fixed address",
		regexp.MustCompile(`\b(sent|(send|post|upload|forward|transmit|e-?mail|exfiltrat|leak|report)(s|es|e|ed|ing)?|cop(y|ies|ied|ying))\b[^.\n]{0,80}?(https?://\S+|\b[\w.+-]+@[\w-]+\.[\w.-]+\b)`)},
	{"system_prompt_reference", SeverityMedium, "a reference to the model's system prompt or hidden context",
		regexp.MustCompile(`\b(system|developer)\s+(prompt|message|instructions)\b|\bhidden\s+(context|instructions)\b`)},
	{"conversation_request", SeverityMedium, "text that asks for the whole conversation or earlier messages",
		regexp.MustCompile(`\b(entire|full|whole|complete)\s+(conversation|chat(\s+history)?|context(\s+window)?|transcript)\b|\b(include|pass|provide|paste|send)\s+(the\s+|all\s+)?(previous|prior|earlier|full|all)\s+(messages|conversation|turns)\b`)},
	{"credential_request", SeverityMedium, "text that asks for passwords, keys or tokens",
		regexp.MustCompile(`\b(include|pass|provide|send|paste|share|enter)\s+(your\s+|the\s+user'?s\s+|any\s+|all\s+)?(passwords?|secrets?|private\s+keys?|access\s+tokens?|api\s+keys?|credentials|seed\s+phrases?)\b`)},
	{"hidden_markup", SeverityMedium, "comments or markup that a person reading the description would not see",
		regexp.MustCompile(`<!--|\[//\]:\s*#|<\s*(span|div|p)\s+style\s*=\s*"[^"]*display\s*:\s*none`)},
	{"encoded_payload", SeverityMedium, "a long base64 or hex string",
		regexp.MustCompile(`[a-z0-9+/]{120,}={0,2}|[0-9a-f]{128,}`)},
}

var harvestParam = regexp.MustCompile(`^(conversation|chat|message)_?(history|log|context|transcript)$|^(system|developer)_?(prompt|message|instructions)$|^(previous|prior|all|full)_?(messages|conversation|context)$|^(full|entire|whole)_?context$`)

var steeringVerb = regexp.MustCompile(`\b(instead\s+of|rather\s+than|before\s+(calling|using|running)|after\s+(calling|using|running)|always\s+(call|use|run)|never\s+(call|use|run)|do\s+not\s+(call|use|run)|don't\s+(call|use|run)|must\s+(call|use|run|be\s+called)|whenever)\b`)

// Scan runs the rule set over a catalog contract.
func Scan(c mcphosting.Contract) Report {
	s := scanner{seen: map[string]bool{}, names: map[string]string{}}
	for _, t := range c.Tools {
		s.names[strings.ToLower(t.Name)] = t.Name
	}
	for _, t := range c.Tools {
		e := entry{kind: KindTool, name: t.Name}
		s.text(e, "/name", t.Name)
		s.text(e, "/description", t.Description)
		s.walk(e, "/inputSchema", t.InputSchema, true)
		s.walkRaw(e, "/outputSchema", t.OutputSchema)
		s.walkRaw(e, "/annotations", t.Annotations)
	}
	for _, p := range c.Prompts {
		e := entry{kind: KindPrompt, name: p.Name}
		s.text(e, "/name", p.Name)
		s.text(e, "/title", p.Title)
		s.text(e, "/description", p.Description)
		for i, a := range p.Arguments {
			base := fmt.Sprintf("/arguments/%d", i)
			s.text(e, base+"/name", a.Name)
			s.text(e, base+"/title", a.Title)
			s.text(e, base+"/description", a.Description)
			s.param(e, base+"/name", a.Name)
		}
	}
	for _, r := range c.Resources {
		e := entry{kind: KindResource, name: r.Name}
		s.text(e, "/name", r.Name)
		s.text(e, "/title", r.Title)
		s.text(e, "/description", r.Description)
	}
	for _, r := range c.ResourceTemplates {
		e := entry{kind: KindResourceTemplate, name: r.Name}
		s.text(e, "/name", r.Name)
		s.text(e, "/title", r.Title)
		s.text(e, "/description", r.Description)
	}
	return s.report()
}

type entry struct{ kind, name string }

type scanner struct {
	findings []Finding
	seen     map[string]bool
	// names maps lower-cased tool names to their spelling.
	names map[string]string
}

func (s *scanner) add(e entry, rule, severity, path, excerpt string) {
	key := rule + "\x00" + e.kind + "\x00" + e.name + "\x00" + path
	if s.seen[key] {
		return
	}
	s.seen[key] = true
	s.findings = append(s.findings, Finding{Rule: rule, Severity: severity, Kind: e.kind, Name: e.name, Path: path, Excerpt: excerpt})
}

// text runs every text rule over one string.
func (s *scanner) text(e entry, path, raw string) {
	if raw == "" {
		return
	}
	if i := invisibleIndex(raw); i >= 0 {
		_, size := utf8.DecodeRuneInString(raw[i:])
		s.add(e, ruleInvisible, SeverityHigh, path, excerpt(raw, i, i+size))
	}
	if len(raw) > api.MCPToolScanDescriptionMaxBytes {
		s.add(e, ruleOversized, SeverityLow, path, fmt.Sprintf("%d bytes", len(raw)))
	}
	norm := normalize(raw)
	for _, r := range textRules {
		if loc := r.re.FindStringIndex(norm); loc != nil {
			s.add(e, r.id, r.severity, path, excerpt(norm, loc[0], loc[1]))
		}
	}
	if loc := steeringVerb.FindStringIndex(norm); loc != nil {
		if other := s.otherTool(e, norm); other != "" {
			s.add(e, ruleCrossTool, SeverityMedium, path, excerpt(norm, loc[0], loc[1])+" … "+other)
		}
	}
}

// otherTool names a tool other than e that the text mentions as a word.
func (s *scanner) otherTool(e entry, norm string) string {
	keys := make([]string, 0, len(s.names))
	for k := range s.names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, lower := range keys {
		if len(lower) < 4 || e.kind == KindTool && s.names[lower] == e.name {
			continue
		}
		if containsWord(norm, lower) {
			return s.names[lower]
		}
	}
	return ""
}

func (s *scanner) param(e entry, path, name string) {
	if harvestParam.MatchString(strings.ToLower(strings.ReplaceAll(name, "-", "_"))) {
		s.add(e, ruleHarvestParam, SeverityMedium, path, name)
	}
}

func (s *scanner) walkRaw(e entry, path string, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		s.walk(e, path, v, false)
	}
}

// skippedKeys hold identifiers and patterns, not text a model reads.
var skippedKeys = map[string]bool{"$schema": true, "$id": true, "$ref": true, "type": true, "format": true, "pattern": true, "contentEncoding": true, "contentMediaType": true}

// walk scans every string in a JSON value. With input set, property names
// are checked as parameters.
func (s *scanner) walk(e entry, path string, v any, input bool) {
	switch t := v.(type) {
	case string:
		s.text(e, path, t)
	case []any:
		for i, x := range t {
			s.walk(e, fmt.Sprintf("%s/%d", path, i), x, input)
		}
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if skippedKeys[k] {
				continue
			}
			child := path + "/" + pointerEscape(k)
			if k == "properties" {
				if props, ok := t[k].(map[string]any); ok {
					s.properties(e, child, props, input)
					continue
				}
			}
			s.walk(e, child, t[k], input)
		}
	}
}

func (s *scanner) properties(e entry, path string, props map[string]any, input bool) {
	names := make([]string, 0, len(props))
	for k := range props {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		child := path + "/" + pointerEscape(name)
		s.text(e, child, name)
		if input {
			s.param(e, child, name)
		}
		s.walk(e, child, props[name], input)
	}
}

func (s *scanner) report() Report {
	rank := map[string]int{SeverityHigh: 0, SeverityMedium: 1, SeverityLow: 2}
	sort.SliceStable(s.findings, func(i, j int) bool {
		a, b := s.findings[i], s.findings[j]
		if rank[a.Severity] != rank[b.Severity] {
			return rank[a.Severity] < rank[b.Severity]
		}
		return a.Kind+"\x00"+a.Name+"\x00"+a.Path+"\x00"+a.Rule < b.Kind+"\x00"+b.Name+"\x00"+b.Path+"\x00"+b.Rule
	})
	r := Report{RuleSet: RuleSet, Findings: s.findings}
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	if len(r.Findings) > api.MCPToolScanFindingsMax {
		r.Findings, r.Truncated = r.Findings[:api.MCPToolScanFindingsMax], true
	}
	return r
}

// invisibleIndex is the byte index of the first character that hides text
// from a person: zero-width and bidirectional controls, Unicode tag
// characters, and C0/C1 controls other than tab, newline and carriage return.
func invisibleIndex(s string) int {
	for i, r := range s {
		if invisible(r) {
			return i
		}
	}
	return -1
}

func invisible(r rune) bool {
	switch {
	case r == '\t' || r == '\n' || r == '\r':
		return false
	case r < 0x20 || r >= 0x7f && r < 0xa0:
		return true
	case r == 0x00ad || r == 0x034f || r == 0x061c || r == 0x115f || r == 0x1160 || r == 0x17b4 || r == 0x17b5 || r == 0x180e:
		return true
	case r >= 0x200b && r <= 0x200f, r >= 0x202a && r <= 0x202e, r >= 0x2060 && r <= 0x206f:
		return true
	case r == 0x3164 || r == 0xfeff || r == 0xffa0:
		return true
	case r >= 0xfff0 && r <= 0xfffb:
		return true
	case r >= 0xe0000 && r <= 0xe007f:
		return true
	case r == utf8.RuneError:
		return false
	}
	return unicode.Is(unicode.Cf, r)
}

// normalize lower-cases, drops invisible characters (so they cannot split a
// phrase) and collapses whitespace.
func normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if invisible(r) {
			continue
		}
		if unicode.IsSpace(r) {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(unicode.ToLower(r))
	}
	return strings.TrimSpace(b.String())
}

func containsWord(text, word string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], word)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(word)
		if wordBoundary(text, start-1) && wordBoundary(text, end) {
			return true
		}
		from = start + 1
	}
}

func wordBoundary(text string, i int) bool {
	if i < 0 || i >= len(text) {
		return true
	}
	c := rune(text[i])
	return !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' && c != '-'
}

// excerpt quotes text around [start, end), at most
// api.MCPToolScanExcerptMaxRunes runes, invisible characters escaped.
func excerpt(s string, start, end int) string {
	const pad = 24
	from, to := start, end
	for n := 0; from > 0 && n < pad; n++ {
		_, size := utf8.DecodeLastRuneInString(s[:from])
		from -= size
	}
	for n := 0; to < len(s) && n < pad; n++ {
		_, size := utf8.DecodeRuneInString(s[to:])
		to += size
	}
	var b strings.Builder
	runes := 0
	for _, r := range s[from:to] {
		if runes == api.MCPToolScanExcerptMaxRunes {
			b.WriteString("…")
			break
		}
		if invisible(r) {
			fmt.Fprintf(&b, "%U", r)
		} else {
			b.WriteRune(r)
		}
		runes++
	}
	return b.String()
}

func pointerEscape(k string) string {
	return strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
}
