package gregalemanifest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// Function config in code (ADR-20261107193000). A Node or Python handler
// file may declare its own `functions:` entry fields; the CLI reads them
// statically and never runs customer code to find them.

// InlineConfigMaxFileBytes bounds the handler file the CLI scans.
const InlineConfigMaxFileBytes = 1 << 20

// FunctionsDiscoveryDir is the directory whose subdirectories are
// discovered as functions when their handler declares a config.
const FunctionsDiscoveryDir = "functions"

var (
	jsConfigRE = regexp.MustCompile(`(?m)^export\s+(?:const|let|var)\s+config\s*(?::\s*[\w.$<>\[\]| ]+?\s*)?=\s*`)
	// jsHelperRE matches the typed helper's call around the object literal.
	jsHelperRE = regexp.MustCompile(`^(?:[A-Za-z_$][\w$]*\.)?defineConfig\s*\(\s*`)
	pyConfigRE = regexp.MustCompile(`(?m)^config\s*(?::\s*[\w.\[\], ]+?\s*)?=\s*`)
	// pyDecoratorRE matches `@function(` or `@<module>.function(`; it counts
	// only when it decorates `handler`.
	pyDecoratorRE   = regexp.MustCompile(`(?m)^@(?:[A-Za-z_]\w*\.)?function\s*\(`)
	pyDecoratesRE   = regexp.MustCompile(`^\s*(?:@[^\n]*\n\s*)*(?:async\s+)?def\s+handler\s*\(`)
	conventionNames = []string{"handler.js", "handler.mjs", "handler.py"}
	// inlineForbidden are entry fields that say where the code is; a file
	// cannot move itself.
	inlineForbidden = map[string]bool{"name": true, "root": true, "handler": true}
)

// InlineConfig is one handler file's declared config.
type InlineConfig struct {
	// File is the handler path relative to the project directory.
	File string
	Spec FunctionSpec
	// Crons are the `cron` shorthand's schedules.
	Crons []string
	// Keys are the entry fields the file set, sorted.
	Keys []string
}

// InlineConfigSource records which settings of one function came from code.
type InlineConfigSource struct {
	Function string
	File     string
	// Keys are the fields the code filled; yaml-set fields are not listed.
	Keys []string
	// Discovered is true when the function has no `functions:` entry.
	Discovered bool
}

// ReadInlineConfig reads the config declared in one handler file. found is
// false when the file declares none or is not a Node or Python file.
func ReadInlineConfig(projectDir, rel string) (InlineConfig, bool, error) {
	lang, ok := inlineLang(rel)
	if !ok {
		return InlineConfig{}, false, nil
	}
	abs := filepath.Join(projectDir, filepath.FromSlash(rel))
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return InlineConfig{}, false, nil
	}
	if info.Size() > InlineConfigMaxFileBytes {
		return InlineConfig{}, false, nil
	}
	src, err := os.ReadFile(abs)
	if err != nil {
		return InlineConfig{}, false, fmt.Errorf("%s: %w", rel, err)
	}
	raw, found, err := extractInlineConfig(string(src), lang)
	if err != nil {
		return InlineConfig{}, false, fmt.Errorf("%s: %w", rel, err)
	}
	if !found {
		return InlineConfig{}, false, nil
	}
	cfg, err := decodeInlineConfig(raw)
	if err != nil {
		return InlineConfig{}, false, fmt.Errorf("%s: config: %w", rel, err)
	}
	cfg.File = rel
	return cfg, true, nil
}

func inlineLang(rel string) (literalLang, bool) {
	switch path.Ext(rel) {
	case ".js", ".mjs":
		return langJS, true
	case ".py":
		return langPython, true
	}
	return 0, false
}

// extractInlineConfig finds and parses the config literal in src.
func extractInlineConfig(src string, lang literalLang) (map[string]any, bool, error) {
	if lang == langJS {
		return extractJSConfig(src)
	}
	return extractPythonConfig(src)
}

func extractJSConfig(src string) (map[string]any, bool, error) {
	locs := jsConfigRE.FindAllStringIndex(src, -1)
	if len(locs) == 0 {
		return nil, false, nil
	}
	if len(locs) > 1 {
		return nil, false, fmt.Errorf("line %d: config is exported more than once", lineOf(src, locs[1][0]))
	}
	pos := locs[0][1]
	helper := jsHelperRE.FindStringIndex(src[pos:])
	if helper != nil {
		pos += helper[1]
	}
	p := &literalParser{src: src, pos: pos, lang: langJS}
	if p.peek() != '{' {
		return nil, false, p.fail("config must be an object literal, like export const config = { cron: \"0 8 * * *\" }")
	}
	m, err := p.object(0)
	if err != nil {
		return nil, false, err
	}
	if helper != nil && p.peek() != ')' {
		return nil, false, p.fail("defineConfig takes one object literal")
	}
	return m, true, nil
}

func extractPythonConfig(src string) (map[string]any, bool, error) {
	var found []map[string]any
	if locs := pyConfigRE.FindAllStringIndex(src, -1); len(locs) > 0 {
		if len(locs) > 1 {
			return nil, false, fmt.Errorf("line %d: config is assigned more than once", lineOf(src, locs[1][0]))
		}
		p := &literalParser{src: src, pos: locs[0][1], lang: langPython}
		if p.peek() != '{' {
			return nil, false, p.fail("config must be a dict literal, like config = {\"cron\": \"0 8 * * *\"}")
		}
		m, err := p.object(0)
		if err != nil {
			return nil, false, err
		}
		found = append(found, m)
	}
	for _, loc := range pyDecoratorRE.FindAllStringIndex(src, -1) {
		p := &literalParser{src: src, pos: loc[1], lang: langPython}
		m, err := p.kwargs()
		if err != nil {
			// Another library's @x.function decorator (Modal's
			// @app.function, say) is not ours unless it decorates handler.
			if decoratesHandler(src, loc[1]) {
				return nil, false, err
			}
			continue
		}
		if !pyDecoratesRE.MatchString(src[p.pos:]) {
			continue
		}
		found = append(found, m)
	}
	switch len(found) {
	case 0:
		return nil, false, nil
	case 1:
		return found[0], true, nil
	}
	return nil, false, errors.New("declare config once: either a module-level config dict or the @function decorator")
}

// decoratesHandler reports whether the decorator call starting at pos sits
// directly above `def handler`, skipping its (unparseable) argument list.
func decoratesHandler(src string, pos int) bool {
	depth := 1
	for i := pos; i < len(src); i++ {
		switch src[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return pyDecoratesRE.MatchString(src[i+1:])
			}
		}
	}
	return false
}

func lineOf(src string, pos int) int { return 1 + strings.Count(src[:pos], "\n") }

// decodeInlineConfig maps a parsed literal onto a FunctionSpec through the
// same strict decoder as a yaml entry.
func decodeInlineConfig(raw map[string]any) (InlineConfig, error) {
	var cfg InlineConfig
	norm := make(map[string]any, len(raw))
	for k, v := range raw {
		key := snakeKey(k)
		if _, dup := norm[key]; dup {
			return cfg, fmt.Errorf("key %q is set twice (as %q and another spelling)", key, k)
		}
		norm[key] = normalizeNested(key, v)
	}
	allowed := yamlKeys(reflect.TypeOf(FunctionSpec{}))
	for key := range norm {
		if inlineForbidden[key] {
			return cfg, fmt.Errorf("%q cannot be set in code; it is the function's location, set by its directory or gregale.yaml", key)
		}
		if key != "cron" && key != "queue" && !slices.Contains(allowed, key) {
			msg := fmt.Sprintf("unknown key %q", key)
			if near := nearestKey(key, append(allowed, "cron", "queue")); near != "" {
				msg += fmt.Sprintf("; did you mean %q?", near)
			}
			return cfg, errors.New(msg)
		}
	}
	if v, ok := norm["cron"]; ok {
		crons, err := stringOrList("cron", v)
		if err != nil {
			return cfg, err
		}
		cfg.Crons = crons
		delete(norm, "cron")
	}
	if v, ok := norm["queue"]; ok {
		queues, err := stringOrList("queue", v)
		if err != nil {
			return cfg, err
		}
		if _, both := norm["queue_bindings"]; both {
			return cfg, errors.New("set either queue or queue_bindings, not both")
		}
		bindings := make([]any, 0, len(queues))
		for _, q := range queues {
			bindings = append(bindings, map[string]any{"name": q, "queue_name": q})
		}
		norm["queue_bindings"] = bindings
		delete(norm, "queue")
	}
	b, err := yaml.Marshal(norm)
	if err != nil {
		return cfg, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg.Spec); err != nil {
		return cfg, inlineDecodeError(err)
	}
	for key := range raw {
		cfg.Keys = append(cfg.Keys, snakeKey(key))
	}
	sort.Strings(cfg.Keys)
	return cfg, nil
}

var inlineYAMLLine = regexp.MustCompile(`^line \d+: `)

// inlineDecodeError drops the line numbers of the generated yaml, which
// mean nothing to the author of the source file.
func inlineDecodeError(err error) error {
	var typeErr *yaml.TypeError
	if !errors.As(err, &typeErr) {
		return err
	}
	msgs := make([]string, 0, len(typeErr.Errors))
	for _, raw := range typeErr.Errors {
		msg := inlineYAMLLine.ReplaceAllString(humanizeYAMLLine(raw), "")
		msgs = append(msgs, msg)
	}
	return errors.New(strings.Join(msgs, "; "))
}

func stringOrList(key string, v any) ([]string, error) {
	switch t := v.(type) {
	case string:
		return []string{t}, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be a string or a list of strings", key)
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s must be a string or a list of strings", key)
}

// normalizeNested converts nested object keys to snake_case, except the
// keys of env, which are the customer's variable names.
func normalizeNested(key string, v any) any {
	if key == "env" {
		return v
	}
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			nk := snakeKey(k)
			out[nk] = normalizeNested(nk, item)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = normalizeNested(key, item)
		}
		return out
	}
	return v
}

// snakeKey converts camelCase to the manifest's snake_case: ramMb and ramMB
// become ram_mb, requestTimeoutS becomes request_timeout_s.
func snakeKey(k string) string {
	if !strings.ContainsFunc(k, unicode.IsUpper) {
		return k
	}
	runes := []rune(k)
	var b strings.Builder
	for i, r := range runes {
		if unicode.IsUpper(r) {
			prevLower := i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]))
			nextLower := i > 0 && i+1 < len(runes) && unicode.IsLower(runes[i+1]) && unicode.IsUpper(runes[i-1])
			if prevLower || nextLower {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ApplyInlineConfig fills each listed function's unset fields from its
// handler's config and, when the manifest lists no functions and holds no
// single-app sections, discovers functions/<name>/ directories whose
// handler declares a config. m may be nil (no gregale.yaml); the returned
// manifest is nil when there is nothing to deploy as a function set.
func ApplyInlineConfig(m *Manifest, projectDir string) (*Manifest, []InlineConfigSource, error) {
	if m != nil && len(m.Functions) > 0 {
		sources, err := m.mergeListedInlineConfig(projectDir)
		return m, sources, err
	}
	if m != nil && (m.Functions != nil || m.rejectSectionsBesideFunctions() != nil) {
		return m, nil, nil
	}
	discovered, sources, err := discoverInlineFunctions(projectDir)
	if err != nil || len(discovered) == 0 {
		return m, nil, err
	}
	if m == nil {
		m = &Manifest{}
	}
	m.Functions = make([]FunctionSpec, 0, len(discovered))
	for _, d := range discovered {
		m.Functions = append(m.Functions, d.Spec)
		m.Triggers = append(m.Triggers, cronTriggers(d.Spec.Name, d.Crons)...)
	}
	return m, sources, nil
}

func (m *Manifest) mergeListedInlineConfig(projectDir string) ([]InlineConfigSource, error) {
	var sources []InlineConfigSource
	for i := range m.Functions {
		fn := &m.Functions[i]
		rel := listedHandlerFile(projectDir, *fn)
		if rel == "" {
			continue
		}
		cfg, found, err := ReadInlineConfig(projectDir, rel)
		if err != nil {
			return nil, fmt.Errorf("function %q: %w", fn.Name, err)
		}
		if !found {
			continue
		}
		keys := fillUnset(fn, cfg.Spec)
		if len(cfg.Crons) > 0 && !m.hasCron(fn.Name) {
			m.Triggers = append(m.Triggers, cronTriggers(fn.Name, cfg.Crons)...)
			keys = append(keys, "cron")
		}
		if len(keys) > 0 {
			sort.Strings(keys)
			sources = append(sources, InlineConfigSource{Function: fn.Name, File: rel, Keys: keys})
		}
	}
	return sources, nil
}

// listedHandlerFile is the project-relative handler file of a listed
// function: its handler: file, else the root's conventional handler.
func listedHandlerFile(projectDir string, fn FunctionSpec) string {
	root := fn.FunctionRoot()
	if fn.Handler != "" {
		return path.Join(root, fn.Handler)
	}
	for _, name := range conventionNames {
		rel := path.Join(root, name)
		if info, err := os.Stat(filepath.Join(projectDir, filepath.FromSlash(rel))); err == nil && !info.IsDir() {
			return rel
		}
	}
	return ""
}

func (m *Manifest) hasCron(function string) bool {
	for _, t := range m.Triggers {
		if t.Kind == TriggerKindCron && t.App == function {
			return true
		}
	}
	return false
}

func cronTriggers(function string, schedules []string) []Trigger {
	out := make([]Trigger, 0, len(schedules))
	for _, s := range schedules {
		out = append(out, Trigger{Kind: TriggerKindCron, App: function, Schedule: s, Path: "/"})
	}
	return out
}

// fillUnset copies each field src sets into dst where dst leaves it unset,
// and returns the yaml keys it filled. gregale.yaml wins.
func fillUnset(dst *FunctionSpec, src FunctionSpec) []string {
	dv := reflect.ValueOf(dst).Elem()
	sv := reflect.ValueOf(src)
	t := dv.Type()
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		key, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if inlineForbidden[key] || sv.Field(i).IsZero() || !dv.Field(i).IsZero() {
			continue
		}
		dv.Field(i).Set(sv.Field(i))
		keys = append(keys, key)
	}
	return keys
}

// discoverInlineFunctions finds functions/<name>/ directories whose
// conventional handler declares a config, in name order.
func discoverInlineFunctions(projectDir string) ([]InlineConfig, []InlineConfigSource, error) {
	entries, err := os.ReadDir(filepath.Join(projectDir, FunctionsDiscoveryDir))
	if err != nil {
		return nil, nil, nil
	}
	var out []InlineConfig
	var sources []InlineConfigSource
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		root := path.Join(FunctionsDiscoveryDir, e.Name())
		var cfg InlineConfig
		found := false
		for _, name := range conventionNames {
			c, ok, err := ReadInlineConfig(projectDir, path.Join(root, name))
			if err != nil {
				return nil, nil, err
			}
			if ok {
				cfg, found = c, true
				break
			}
		}
		if !found {
			continue
		}
		if !functionNameRE.MatchString(e.Name()) {
			return nil, nil, fmt.Errorf("%s declares a config, but %q is not a valid function name: use 1-30 lowercase letters, digits or hyphens, starting with a letter", cfg.File, e.Name())
		}
		cfg.Spec.Name = e.Name()
		cfg.Spec.Root = root
		out = append(out, cfg)
		sources = append(sources, InlineConfigSource{Function: e.Name(), File: cfg.File, Keys: cfg.Keys, Discovered: true})
	}
	return out, sources, nil
}
