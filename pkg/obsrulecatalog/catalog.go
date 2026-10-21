package obsrulecatalog

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

const CoreSetID = "gregale-core-v1"
const FullSetID = "gregale-full-v1"
const FingerprintAlgorithm = "promql_ast_v1"

//go:embed catalog.json
var catalogJSON []byte

var ErrInvalidCatalog = errors.New("expected rule catalog unavailable")

type Rule struct {
	ID         string     `json:"id"`
	FileName   string     `json:"file_name"`
	GroupName  string     `json:"group_name"`
	RuleName   string     `json:"rule_name"`
	Definition Definition `json:"definition"`
}

type Set struct {
	ID       string   `json:"id"`
	Revision string   `json:"revision"`
	Files    []string `json:"files"`
	Rules    []Rule   `json:"rules"`
}

type Catalog struct {
	SchemaVersion        int    `json:"schema_version"`
	FingerprintAlgorithm string `json:"fingerprint_algorithm"`
	Sets                 []Set  `json:"sets"`
}

func KnownSetID(id string) bool { return id == CoreSetID || id == FullSetID }

// Load returns detached data. No source files, configuration or network are read.
func Load() (Catalog, error) { return decode(catalogJSON) }

func decode(raw []byte) (Catalog, error) {
	var out Catalog
	if len(raw) > api.ObsRuleCatalogMaxBytes {
		return out, ErrInvalidCatalog
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || decoder.Decode(&struct{}{}) != io.EOF || out.SchemaVersion != 1 || out.FingerprintAlgorithm != FingerprintAlgorithm || len(out.Sets) != 2 || len(out.Sets) > api.ObsRuleCoverageExpectedSetsMax {
		return Catalog{}, ErrInvalidCatalog
	}
	seen := map[string]bool{}
	for _, set := range out.Sets {
		if !KnownSetID(set.ID) || seen[set.ID] || !validSet(set) {
			return Catalog{}, ErrInvalidCatalog
		}
		seen[set.ID] = true
	}
	return out, nil
}

func validSet(set Set) bool {
	if len(set.Rules) == 0 || len(set.Rules) > api.ObsAlertSnapshotMaxRules || len(set.Files) == 0 || len(set.Files) > api.ObsRuleCoverageExpectedFilesMax || !sort.StringsAreSorted(set.Files) || !safeHash.MatchString(set.Revision) || revision(set) != set.Revision {
		return false
	}
	files := map[string]bool{}
	for _, file := range set.Files {
		if !SafeIdentity(file) || files[file] {
			return false
		}
		files[file] = true
	}
	previous := ""
	for _, r := range set.Rules {
		id, ok := Identity(r.FileName, r.GroupName, r.RuleName)
		d := r.Definition
		if !ok || r.ID != id || r.ID <= previous || !files[r.FileName] || !safeHash.MatchString(d.ExpressionHash) || !safeHash.MatchString(d.LabelsHash) || !validDuration(d.ForSeconds) || !validDuration(d.KeepFiringForSeconds) || d.Fingerprint != definitionFingerprint(d) {
			return false
		}
		previous = r.ID
	}
	return true
}

func revision(set Set) string {
	return digest([]any{1, FingerprintAlgorithm, set.ID, set.Files, set.Rules})
}
