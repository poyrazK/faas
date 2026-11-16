package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Only object field names, kinds (object versus atomic), and digests survive
// record parsing. Scalar values and array contents never enter this index.
type exportDiffField struct {
	digest  [sha256.Size]byte
	members map[string]exportDiffField
}

type exportDiffFieldChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type exportDiffExplanation struct {
	ID      json.RawMessage         `json:"id"`
	Changes []exportDiffFieldChange `json:"changes"`
}

func exportDiffFields(value exportDiffValue) exportDiffField {
	field := exportDiffField{digest: value.digest}
	if value.kind == 'o' {
		field.members = make(map[string]exportDiffField, len(value.members))
		for name, child := range value.members {
			field.members[name] = exportDiffFields(child)
		}
	}
	return field
}

func compareExportDiffIndexesExplained(before, after map[string]exportDiffEntry, key string, explain bool) exportDiffReport {
	report := compareExportDiffIndexes(before, after, key)
	if !explain {
		return report
	}
	explanations := make([]exportDiffExplanation, 0, report.Summary.Changed)
	ids := make([]string, 0, report.Summary.Changed)
	for id, current := range after {
		if old, found := before[id]; found && old.digest != current.digest {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		changes := []exportDiffFieldChange{}
		collectExportDiffFieldChanges(*before[id].fields, *after[id].fields, "", &changes)
		sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
		explanations = append(explanations, exportDiffExplanation{ID: after[id].key, Changes: changes})
	}
	report.Explanations = &explanations
	return report
}

func collectExportDiffFieldChanges(before, after exportDiffField, path string, changes *[]exportDiffFieldChange) {
	if before.digest == after.digest {
		return
	}
	if before.members == nil || after.members == nil {
		*changes = append(*changes, exportDiffFieldChange{Path: path, Kind: "changed"})
		return
	}
	for name, old := range before.members {
		childPath := exportDiffFieldPointer(path, name)
		current, found := after.members[name]
		if !found {
			*changes = append(*changes, exportDiffFieldChange{Path: childPath, Kind: "removed"})
		} else {
			collectExportDiffFieldChanges(old, current, childPath, changes)
		}
	}
	for name := range after.members {
		if _, found := before.members[name]; !found {
			*changes = append(*changes, exportDiffFieldChange{Path: exportDiffFieldPointer(path, name), Kind: "added"})
		}
	}
}

func exportDiffFieldPointer(parent, name string) string {
	return parent + "/" + strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
}

func appendExportDiffExplanations(output *bytes.Buffer, explanations *[]exportDiffExplanation) {
	if explanations == nil {
		return
	}
	for _, explanation := range *explanations {
		_, _ = fmt.Fprintf(output, "Fields for %s:\n", explanation.ID)
		for _, change := range explanation.Changes {
			path, _ := json.Marshal(change.Path)
			_, _ = fmt.Fprintf(output, "  %s: %s\n", change.Kind, path)
		}
	}
}
