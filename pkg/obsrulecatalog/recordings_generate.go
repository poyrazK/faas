package obsrulecatalog

import (
	"encoding/json"
	"path/filepath"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type dependencyNode struct {
	rule      Rule
	names     []string
	uncertain bool
}

// GenerateRecordings binds recording definitions and syntactic alert references
// to the exact selected alert catalog revision. It is never called at runtime.
func GenerateRecordings(root string) ([]byte, error) {
	raw, err := Generate(root)
	if err != nil {
		return nil, err
	}
	alerts, err := decode(raw)
	if err != nil {
		return nil, err
	}
	recordings, alertNodes := []dependencyNode{}, []dependencyNode{}
	for _, rel := range sourceFiles {
		groups, err := readSourceGroups(root, rel)
		if err != nil {
			return nil, err
		}
		for _, group := range groups {
			for _, r := range group.Rules {
				name := r.Alert
				if r.Record != "" {
					name = r.Record
					if r.Alert != "" || r.For != "" || r.KeepFiringFor != "" {
						return nil, ErrInvalidCatalog
					}
				}
				id, ok := Identity(filepath.Base(rel), group.Name, name)
				if !ok {
					return nil, ErrInvalidCatalog
				}
				names, uncertain, err := selectorNames(r.Expr)
				if err != nil {
					return nil, err
				}
				node := dependencyNode{rule: Rule{ID: id, FileName: filepath.Base(rel), GroupName: group.Name, RuleName: name}, names: names, uncertain: uncertain}
				if r.Record != "" {
					node.rule.Definition, err = Normalize(r.Expr, 0, 0, r.Labels)
					if err != nil {
						return nil, err
					}
					recordings = append(recordings, node)
				} else {
					alertNodes = append(alertNodes, node)
				}
				if len(recordings)+len(alertNodes) > api.ObsAlertSnapshotMaxRules {
					return nil, ErrInvalidCatalog
				}
			}
		}
	}
	out := RecordingCatalog{SchemaVersion: 1, FingerprintAlgorithm: FingerprintAlgorithm, Sets: []RecordingSet{}}
	for _, alertSet := range alerts.Sets {
		set, err := buildRecordingSet(alertSet, recordings, alertNodes)
		if err != nil {
			return nil, err
		}
		out.Sets = append(out.Sets, set)
	}
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	encoded = append(encoded, '\n')
	if _, err := decodeRecordings(encoded, alerts); err != nil {
		return nil, err
	}
	return encoded, nil
}

func selectorNames(query string) ([]string, bool, error) {
	if len(query) > api.ObsRuleCoverageExpressionMaxBytes {
		return nil, false, ErrInvalidDefinition
	}
	expr, err := parser.NewParser(parser.Options{}).ParseExpr(query)
	if err != nil {
		return nil, false, ErrInvalidDefinition
	}
	names, uncertain := map[string]bool{}, false
	parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
		if n, ok := node.(*parser.VectorSelector); ok {
			name := n.Name
			conflict := false
			for _, matcher := range n.LabelMatchers {
				if matcher.Name == "__name__" && matcher.Type == labels.MatchEqual && matcher.Value != "" {
					if name != "" && name != matcher.Value {
						conflict = true
					}
					name = matcher.Value
				}
			}
			if name == "" || conflict {
				uncertain = true
			} else {
				names[name] = true
			}
		}
		return nil
	})
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, uncertain, nil
}

func buildRecordingSet(alerts Set, recordings, alertNodes []dependencyNode) (RecordingSet, error) {
	set := RecordingSet{Expected: Set{ID: alerts.ID, Files: append([]string{}, alerts.Files...), Rules: []Rule{}}, AlertSetRevision: alerts.Revision, AlertCount: len(alerts.Rules), Dependents: []RecordingDependents{}, UncertainAlerts: []RuleReference{}}
	files := map[string]bool{}
	for _, file := range alerts.Files {
		files[file] = true
	}
	byName := map[string][]dependencyNode{}
	dependents := map[string][]RuleReference{}
	for _, node := range recordings {
		if !files[node.rule.FileName] {
			continue
		}
		set.Expected.Rules = append(set.Expected.Rules, node.rule)
		byName[node.rule.RuleName] = append(byName[node.rule.RuleName], node)
		dependents[node.rule.ID] = []RuleReference{}
	}
	links := 0
	for _, alert := range alertNodes {
		if !files[alert.rule.FileName] {
			continue
		}
		visited, uncertain := recordingReferences(alert, byName)
		if uncertain {
			set.UncertainAlerts = append(set.UncertainAlerts, Reference(alert.rule))
		}
		for id := range visited {
			links++
			if links > api.ObsRecordingDependencyLinksMax {
				return RecordingSet{}, ErrInvalidCatalog
			}
			dependents[id] = append(dependents[id], Reference(alert.rule))
		}
	}
	sort.Slice(set.Expected.Rules, func(i, j int) bool { return set.Expected.Rules[i].ID < set.Expected.Rules[j].ID })
	set.Expected.Revision = revision(set.Expected)
	sortReferences(set.UncertainAlerts)
	for _, rule := range set.Expected.Rules {
		refs := dependents[rule.ID]
		sortReferences(refs)
		set.Dependents = append(set.Dependents, RecordingDependents{RuleID: rule.ID, Alerts: refs})
	}
	set.Revision = recordingRevision(set)
	if !validRecordingSet(set, alerts) {
		return RecordingSet{}, ErrInvalidCatalog
	}
	return set, nil
}

func recordingReferences(alert dependencyNode, byName map[string][]dependencyNode) (map[string]bool, bool) {
	visited, active := map[string]bool{}, map[string]bool{}
	uncertain := alert.uncertain
	var walk func([]string)
	walk = func(names []string) {
		for _, name := range names {
			producers := byName[name]
			if len(producers) > 1 {
				uncertain = true
				continue
			}
			if len(producers) == 0 {
				continue
			} // Raw or external metrics are outside this catalog.
			node := producers[0]
			if active[node.rule.ID] {
				uncertain = true
				continue
			}
			if visited[node.rule.ID] {
				continue
			}
			visited[node.rule.ID], active[node.rule.ID] = true, true
			uncertain = uncertain || node.uncertain
			walk(node.names)
			delete(active, node.rule.ID)
		}
	}
	walk(alert.names)
	return visited, uncertain
}

func sortReferences(refs []RuleReference) {
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
}
