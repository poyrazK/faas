package obsrulecatalog

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"io"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

//go:embed recordings.json
var recordingJSON []byte

// RuleReference identifies expected catalog syntax, not a historical episode or
// a proven runtime dependency. Raw queries and label values are never retained.
type RuleReference struct {
	ID        string `json:"id"`
	FileName  string `json:"file_name"`
	GroupName string `json:"group_name"`
	RuleName  string `json:"rule_name"`
}

type RecordingDependents struct {
	RuleID string          `json:"rule_id"`
	Alerts []RuleReference `json:"alerts"`
}

type RecordingSet struct {
	Revision         string                `json:"revision"`
	Expected         Set                   `json:"expected"`
	AlertSetRevision string                `json:"alert_set_revision"`
	AlertCount       int                   `json:"alert_count"`
	Dependents       []RecordingDependents `json:"dependents"`
	UncertainAlerts  []RuleReference       `json:"uncertain_alerts"`
}

type RecordingCatalog struct {
	SchemaVersion        int            `json:"schema_version"`
	FingerprintAlgorithm string         `json:"fingerprint_algorithm"`
	Sets                 []RecordingSet `json:"sets"`
}

func Reference(rule Rule) RuleReference {
	return RuleReference{ID: rule.ID, FileName: rule.FileName, GroupName: rule.GroupName, RuleName: rule.RuleName}
}

func LoadRecordings() (RecordingCatalog, error) {
	alerts, err := Load()
	if err != nil {
		return RecordingCatalog{}, err
	}
	return decodeRecordings(recordingJSON, alerts)
}

func decodeRecordings(raw []byte, alerts Catalog) (RecordingCatalog, error) {
	var out RecordingCatalog
	if len(raw) > api.ObsRuleCatalogMaxBytes {
		return out, ErrInvalidCatalog
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(&struct{}{}) != io.EOF || out.SchemaVersion != 1 || out.FingerprintAlgorithm != FingerprintAlgorithm || len(out.Sets) != len(alerts.Sets) {
		return RecordingCatalog{}, ErrInvalidCatalog
	}
	seen := map[string]bool{}
	for _, set := range out.Sets {
		i := slices.IndexFunc(alerts.Sets, func(a Set) bool { return a.ID == set.Expected.ID })
		if i < 0 || seen[set.Expected.ID] || !validRecordingSet(set, alerts.Sets[i]) {
			return RecordingCatalog{}, ErrInvalidCatalog
		}
		seen[set.Expected.ID] = true
	}
	return out, nil
}

func validRecordingSet(set RecordingSet, alerts Set) bool {
	if !validSet(set.Expected) || set.Expected.ID != alerts.ID || !slices.Equal(set.Expected.Files, alerts.Files) || set.AlertSetRevision != alerts.Revision || set.AlertCount != len(alerts.Rules) || set.Revision != recordingRevision(set) || len(set.Dependents) != len(set.Expected.Rules) || set.UncertainAlerts == nil {
		return false
	}
	known := map[string]RuleReference{}
	for _, rule := range alerts.Rules {
		known[rule.ID] = Reference(rule)
	}
	if !validReferences(set.UncertainAlerts, known) {
		return false
	}
	links := 0
	for i, rule := range set.Expected.Rules {
		dep := set.Dependents[i]
		if rule.Definition.ForSeconds != 0 || rule.Definition.KeepFiringForSeconds != 0 || dep.RuleID != rule.ID || !validReferences(dep.Alerts, known) {
			return false
		}
		links += len(dep.Alerts)
		if links > api.ObsRecordingDependencyLinksMax {
			return false
		}
	}
	return true
}

func validReferences(refs []RuleReference, known map[string]RuleReference) bool {
	if refs == nil || len(refs) > len(known) {
		return false
	}
	previous := ""
	for _, ref := range refs {
		if ref.ID <= previous || ref != known[ref.ID] {
			return false
		}
		previous = ref.ID
	}
	return true
}

func recordingRevision(set RecordingSet) string {
	return digest([]any{1, FingerprintAlgorithm, set.Expected, set.AlertSetRevision, set.AlertCount, set.Dependents, set.UncertainAlerts})
}
