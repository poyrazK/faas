package obsrulecatalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/common/model"
	"gopkg.in/yaml.v3"
)

var sourceFiles = []string{
	"deploy/ansible/roles/prometheus/files/faas.rules.yml",
	"deploy/ansible/roles/prometheus/files/bridge.rules.yml",
	"deploy/ansible/roles/prometheus/files/pg_backup.rules.yml",
	"pkg/promqlrules/data_placement.yaml",
	"deploy/ansible/roles/prometheus/files/data_repair.rules.yml",
}

type sourceGroup struct {
	Name   string            `yaml:"name"`
	Labels map[string]string `yaml:"labels"`
	Rules  []struct {
		Alert         string            `yaml:"alert"`
		Record        string            `yaml:"record"`
		Expr          string            `yaml:"expr"`
		For           string            `yaml:"for"`
		KeepFiringFor string            `yaml:"keep_firing_for"`
		Labels        map[string]string `yaml:"labels"`
	} `yaml:"rules"`
}

// Generate uses the repository-owned alert files. Runtime never calls it.
func Generate(root string) ([]byte, error) {
	core := Set{ID: CoreSetID, Files: []string{"faas.rules.yml"}, Rules: []Rule{}}
	full := Set{ID: FullSetID, Files: []string{}, Rules: []Rule{}}
	for _, rel := range sourceFiles {
		groups, err := readSourceGroups(root, rel)
		if err != nil {
			return nil, err
		}
		file := filepath.Base(rel)
		full.Files = append(full.Files, file)
		for _, group := range groups {
			for _, r := range group.Rules {
				if r.Record != "" {
					if r.Alert != "" {
						return nil, ErrInvalidCatalog
					}
					continue
				}
				id, ok := Identity(file, group.Name, r.Alert)
				if !ok {
					return nil, ErrInvalidCatalog
				}
				hold, err := durationSeconds(r.For)
				if err != nil {
					return nil, err
				}
				keep, err := durationSeconds(r.KeepFiringFor)
				if err != nil {
					return nil, err
				}
				d, err := Normalize(r.Expr, hold, keep, r.Labels)
				if err != nil {
					return nil, fmt.Errorf("catalog rule %s/%s: %w", file, r.Alert, err)
				}
				item := Rule{ID: id, FileName: file, GroupName: group.Name, RuleName: r.Alert, Definition: d}
				full.Rules = append(full.Rules, item)
				if file == "faas.rules.yml" {
					core.Rules = append(core.Rules, item)
				}
			}
		}
	}
	for _, set := range []*Set{&core, &full} {
		sort.Strings(set.Files)
		sort.Slice(set.Rules, func(i, j int) bool { return set.Rules[i].ID < set.Rules[j].ID })
		set.Revision = revision(*set)
		if !validSet(*set) {
			return nil, ErrInvalidCatalog
		}
	}
	out := Catalog{SchemaVersion: 1, FingerprintAlgorithm: FingerprintAlgorithm, Sets: []Set{core, full}}
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode expected catalog: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := decode(encoded); err != nil {
		return nil, err
	}
	return encoded, nil
}

func readSourceGroups(root, rel string) ([]sourceGroup, error) {
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return nil, fmt.Errorf("read catalog source: %w", err)
	}
	if len(raw) > api.ObsRuleCatalogMaxBytes {
		return nil, ErrInvalidCatalog
	}
	var doc struct {
		Groups []sourceGroup `yaml:"groups"`
	}
	if yaml.Unmarshal(raw, &doc) != nil || doc.Groups == nil {
		return nil, ErrInvalidCatalog
	}
	seen := map[string]bool{}
	for _, group := range doc.Groups {
		// Group label inheritance is outside this v1 comparison contract.
		if !SafeIdentity(group.Name) || seen[group.Name] || len(group.Labels) > 0 || group.Rules == nil {
			return nil, ErrInvalidCatalog
		}
		seen[group.Name] = true
	}
	return doc.Groups, nil
}

func durationSeconds(raw string) (float64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := model.ParseDuration(raw)
	if err != nil || value < 0 {
		return 0, ErrInvalidDefinition
	}
	return float64(value) / 1e9, nil
}
