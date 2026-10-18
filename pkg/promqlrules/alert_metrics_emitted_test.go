package promqlrules_test

import (
	"encoding/json"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// externalMetricPrefixes are series exported by third-party binaries
// (node_exporter, Prometheus, Alertmanager, Loki, Promtail, the Cloudflare
// Tunnel connector, client_golang's process collector) or synthesized per
// target by Prometheus (scrape_*)
// rather than by Gregale code.
var externalMetricPrefixes = []string{"alertmanager_", "cloudflared_", "loki_", "node_", "process_", "prometheus_", "promtail_", "scrape_"}

var externalMetricNames = map[string]bool{"up": true, "ALERTS": true, "ALERTS_FOR_STATE": true}

// daemonMetricPrefixes are the runtime prefixes passed to wire.NewOpsMetrics
// and the per-package registries, which build names as prefix + "_name".
var daemonMetricPrefixes = []string{
	"gatewayd_internal", "gatewayd_public", "s3_gateway", "outboundd", "realtimed",
	"builderd", "gatewayd", "githubd", "profiled", "imaged", "meterd", "schedd",
	"apid", "vmmd",
}

// knownUnemittedAlertMetrics are dead alert inputs: each maps a metric no
// code emits to the reason. Fix the producer or the rule, then delete the
// entry; the test fails once the metric resolves so the list cannot rot.
var knownUnemittedAlertMetrics = map[string]string{}

var promqlKeywords = map[string]bool{
	"sum": true, "min": true, "max": true, "avg": true, "count": true, "stddev": true,
	"stdvar": true, "topk": true, "bottomk": true, "quantile": true, "count_values": true,
	"group": true, "by": true, "without": true, "on": true, "ignoring": true,
	"group_left": true, "group_right": true, "bool": true, "and": true, "or": true,
	"unless": true, "offset": true, "atan2": true, "inf": true, "nan": true, "Inf": true, "NaN": true,
}

var (
	promqlStringRE   = regexp.MustCompile("\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\.)*'|`[^`]*`")
	promqlMatcherRE  = regexp.MustCompile(`\{[^}]*\}`)
	promqlRangeRE    = regexp.MustCompile(`\[[^\]]*\]`)
	promqlGroupingRE = regexp.MustCompile(`\b(?:by|without|on|ignoring|group_left|group_right)\s*\([^)]*\)`)
	promqlCommentRE  = regexp.MustCompile(`#[^\n]*`)
	metricIdentRE    = regexp.MustCompile(`[A-Za-z_:][A-Za-z0-9_:]*`)
)

// TestAlertMetricsAreEmitted requires every metric a shipped rule reads to be
// produced somewhere: a Go string literal in cmd/ or pkg/, or a textfile
// collector shipped by an Ansible role. A rule over a misspelled or renamed
// metric matches no series and stays silent forever; promtool cannot see
// that, because its fixtures feed the rule the name the rule expects.
func TestAlertMetricsAreEmitted(t *testing.T) {
	rules := loadShippedRules(t)
	readers := map[string][]string{}
	for _, r := range rules {
		name := r.Alert
		if name == "" {
			name = r.Record
		}
		for _, m := range exprMetricNames(r.Expr) {
			readers[m] = append(readers[m], name)
		}
	}
	src := loadMetricSources(t)
	missing := unemittedMetrics(readers, recordedMetrics(rules), src)
	for _, m := range missing {
		if _, known := knownUnemittedAlertMetrics[m]; known {
			continue
		}
		t.Errorf("%s (read by %s) is not emitted by any Go source in cmd/ or pkg/ or any Ansible textfile collector; fix the metric name in the rule or register the metric", m, strings.Join(dedupe(readers[m]), ", "))
	}
	for m := range knownUnemittedAlertMetrics {
		if _, read := readers[m]; !read || src.emits(m) {
			t.Errorf("knownUnemittedAlertMetrics[%q] is stale: the metric is now emitted or no longer read by a rule; delete the entry", m)
		}
	}
}

// TestDashboardMetricsAreEmitted applies the same check to every PromQL
// target in deploy/grafana. A panel over a name nothing exports renders an
// empty graph that reads as "no events" rather than "broken query"; the Safe
// Deploy row queried unprefixed safedeploy_* names for months.
func TestDashboardMetricsAreEmitted(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(repoRootDir, "deploy", "grafana", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob dashboards: %v (found %d)", err, len(paths))
	}
	readers := map[string][]string{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		walkDashboardExprs(doc, "", func(panel, expr string) {
			for _, m := range exprMetricNames(expr) {
				readers[m] = append(readers[m], filepath.Base(path)+" \""+panel+"\"")
			}
		})
	}
	for _, m := range unemittedMetrics(readers, recordedMetrics(loadShippedRules(t)), loadMetricSources(t)) {
		t.Errorf("%s (queried by %s) is not emitted by any Go source in cmd/ or pkg/ or any Ansible textfile collector; the panel can only ever be empty", m, strings.Join(dedupe(readers[m]), ", "))
	}
}

// walkDashboardExprs calls visit with every Prometheus target expression in a
// Grafana dashboard and the title of the panel that holds it. Targets on a
// non-Prometheus datasource (Loki) are skipped.
func walkDashboardExprs(node any, panel string, visit func(panel, expr string)) {
	switch v := node.(type) {
	case map[string]any:
		if title, ok := v["title"].(string); ok {
			panel = title
		}
		if expr, ok := v["expr"].(string); ok && !isLokiTarget(v) {
			visit(panel, expr)
		}
		for _, child := range v {
			walkDashboardExprs(child, panel, visit)
		}
	case []any:
		for _, child := range v {
			walkDashboardExprs(child, panel, visit)
		}
	}
}

func isLokiTarget(target map[string]any) bool {
	ds, _ := target["datasource"].(map[string]any)
	kind, _ := ds["type"].(string)
	return kind == "loki"
}

func recordedMetrics(rules []shippedRule) map[string]bool {
	recorded := map[string]bool{}
	for _, r := range rules {
		if r.Record != "" {
			recorded[r.Record] = true
		}
	}
	return recorded
}

// unemittedMetrics returns the read metrics that are neither recording-rule
// outputs, third-party exporter series, nor produced by Gregale sources.
func unemittedMetrics(readers map[string][]string, recorded map[string]bool, src metricSources) []string {
	var missing []string
	for m := range readers {
		if recorded[m] || externalMetricNames[m] || hasAnyPrefix(m, externalMetricPrefixes) {
			continue
		}
		if !src.emits(m) {
			missing = append(missing, m)
		}
	}
	sort.Strings(missing)
	return missing
}

// exprMetricNames returns the metric names a PromQL expression selects. It
// drops string literals, label matchers, ranges, grouping clauses, function
// and aggregation calls, keywords and numbers, leaving bare selectors.
func exprMetricNames(expr string) []string {
	e := promqlStringRE.ReplaceAllString(expr, `""`)
	e = promqlCommentRE.ReplaceAllString(e, "")
	e = promqlMatcherRE.ReplaceAllString(e, "")
	e = promqlRangeRE.ReplaceAllString(e, "")
	e = promqlGroupingRE.ReplaceAllString(e, "")
	seen := map[string]bool{}
	var names []string
	for _, loc := range metricIdentRE.FindAllStringIndex(e, -1) {
		name := e[loc[0]:loc[1]]
		if promqlKeywords[name] || seen[name] {
			continue
		}
		if loc[0] > 0 && (e[loc[0]-1] >= '0' && e[loc[0]-1] <= '9' || e[loc[0]-1] == '.') {
			continue // exponent or duration suffix of a number
		}
		if loc[0] > 0 && e[loc[0]-1] == '$' {
			continue // Grafana template variable
		}
		if strings.HasPrefix(strings.TrimLeft(e[loc[1]:], " \t\n"), "(") {
			continue // function or aggregation call
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

type metricSources struct {
	literals map[string]bool // exact Go string literal values
	tokens   map[string]bool // identifiers inside Go literals and Ansible collectors
}

func loadMetricSources(t *testing.T) metricSources {
	t.Helper()
	src := metricSources{literals: map[string]bool{}, tokens: map[string]bool{}}
	for _, dir := range []string{"cmd", "pkg"} {
		walkFiles(t, filepath.Join(repoRootDir, dir), func(path string) {
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return
			}
			src.addGoLiterals(t, path)
		})
	}
	// Textfile collectors (shell and Python scripts, systemd templates)
	// ship from role files/ and templates/; the prometheus and grafana roles
	// only read metrics, so they would vouch for the very names under test.
	walkFiles(t, filepath.Join(repoRootDir, "deploy", "ansible", "roles"), func(path string) {
		rel := filepath.ToSlash(path)
		if !strings.Contains(rel, "/files/") && !strings.Contains(rel, "/templates/") {
			return
		}
		if strings.Contains(rel, "/roles/prometheus/") || strings.Contains(rel, "/roles/grafana/") ||
			strings.HasSuffix(rel, ".md") || strings.HasSuffix(rel, ".json") {
			return
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, tok := range metricIdentRE.FindAllString(string(raw), -1) {
			src.tokens[tok] = true
		}
	})
	return src
}

func walkFiles(t *testing.T, root string, visit func(path string)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		visit(path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

func (s metricSources) addGoLiterals(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	fset := token.NewFileSet()
	file := fset.AddFile(path, fset.Base(), len(raw))
	var sc scanner.Scanner
	sc.Init(file, raw, nil, 0)
	for {
		_, tok, lit := sc.Scan()
		if tok == token.EOF {
			return
		}
		if tok != token.STRING {
			continue
		}
		value, err := strconv.Unquote(lit)
		if err != nil {
			continue
		}
		s.literals[value] = true
		for _, ident := range exposedMetricNames(value) {
			s.tokens[ident] = true
		}
	}
}

// exposedMetricNames returns the names a literal writes in exposition
// position: the first token of a line (a textfile sample such as
// "faas_x{a=%q} %d") or the name after "# HELP " / "# TYPE ". Names that only
// appear inside prose, such as a Help string citing a sibling metric, do not
// count as emitted.
func exposedMetricNames(literal string) []string {
	var names []string
	for _, line := range strings.Split(literal, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(strings.TrimPrefix(line, "# HELP "), "# TYPE ")
		loc := metricIdentRE.FindStringIndex(line)
		if loc == nil || loc[0] != 0 {
			continue
		}
		if rest := line[loc[1]:]; rest == "" || rest[0] == '{' || rest[0] == ' ' {
			names = append(names, line[:loc[1]])
		}
	}
	return names
}

// emits reports whether some source produces metric m, either verbatim or
// composed at runtime as prefix + "_rest" or head + tail from two literals.
func (s metricSources) emits(m string) bool {
	forms := metricForms(m)
	for _, f := range forms {
		if s.tokens[f] {
			return true
		}
	}
	for _, p := range daemonMetricPrefixes {
		if !strings.HasPrefix(m, p+"_") {
			continue
		}
		for _, rest := range metricForms(strings.TrimPrefix(m, p+"_")) {
			if s.literals["_"+rest] || s.literals["%s_"+rest] || (s.literals[rest] && wordCount(rest) >= 2) {
				return true
			}
		}
		break
	}
	for _, f := range forms {
		for j := 1; j < len(f); j++ {
			if f[j-1] == '_' && s.literals[f[j:]] && s.composedHead(f[:j]) {
				return true
			}
		}
	}
	return false
}

// composedHead accepts a multi-word head literal ("schedd_commit_relay_"),
// or a daemon prefix followed by one ("apid" + "_durable_entity_health_").
func (s metricSources) composedHead(head string) bool {
	if s.literals[head] && wordCount(head) >= 2 {
		return true
	}
	for _, p := range daemonMetricPrefixes {
		if rest := strings.TrimPrefix(head, p); rest != head && strings.HasPrefix(rest, "_") && s.literals[rest] && wordCount(rest) >= 2 {
			return true
		}
	}
	return false
}

// metricForms returns m with the histogram/summary suffixes stripped and the
// counter _total suffix both present and absent.
func metricForms(m string) []string {
	bases := []string{m}
	for _, suffix := range []string{"_bucket", "_count", "_sum"} {
		if strings.HasSuffix(m, suffix) {
			bases = append(bases, strings.TrimSuffix(m, suffix))
		}
	}
	var forms []string
	for _, b := range bases {
		forms = append(forms, b)
		if strings.HasSuffix(b, "_total") {
			forms = append(forms, strings.TrimSuffix(b, "_total"))
		} else {
			forms = append(forms, b+"_total")
		}
	}
	return forms
}

func wordCount(s string) int {
	n := 0
	for _, w := range strings.Split(s, "_") {
		if w != "" {
			n++
		}
	}
	return n
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
