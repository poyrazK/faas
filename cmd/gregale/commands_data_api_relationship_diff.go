package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

type dataAPIRelationship struct {
	Name               string   `json:"foreignKeyName"`
	Columns            []string `json:"columns"`
	OneToOne           bool     `json:"isOneToOne"`
	ReferencedRelation string   `json:"referencedRelation"`
	ReferencedColumns  []string `json:"referencedColumns"`
}

func dataAPIRelationships(raw json.RawMessage) (map[[2]string]dataAPIRelationship, bool) {
	var relationships []dataAPIRelationship
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&relationships) != nil {
		return nil, false
	}
	result := map[[2]string]dataAPIRelationship{}
	for _, relationship := range relationships {
		key := [2]string{relationship.Name, relationship.ReferencedRelation}
		if _, exists := result[key]; exists || relationship.Name == "" || relationship.ReferencedRelation == "" || len(relationship.Columns) == 0 || len(relationship.Columns) != len(relationship.ReferencedColumns) {
			return nil, false
		}
		for i, column := range relationship.Columns {
			if column == "" || relationship.ReferencedColumns[i] == "" {
				return nil, false
			}
		}
		result[key] = relationship
	}
	return result, true
}

func diffDataAPIRelationships(before dataAPISnapshot, old, current dataAPITable) []dataAPIChange {
	previous, validBefore := dataAPIRelationships(old.Relationships)
	next, validAfter := dataAPIRelationships(current.Relationships)
	key := old.Schema + "." + old.Name
	if !validBefore || !validAfter {
		if comparableDataAPIRelationshipJSON(old.Relationships) != comparableDataAPIRelationshipJSON(current.Relationships) {
			return []dataAPIChange{{Path: key, Reason: "relationship metadata changed; review embedded queries", Breaking: true}}
		}
		return nil
	}
	changes := []dataAPIChange{}
	for identity, relationship := range previous {
		path := key + "[" + relationship.Name + "->" + relationship.ReferencedRelation + "]"
		candidate, exists := next[identity]
		if !exists {
			changes = append(changes, dataAPIChange{Path: path, Reason: "relationship removed; update embedded queries", Breaking: true})
		} else if !reflect.DeepEqual(relationship, candidate) {
			changes = append(changes, dataAPIChange{Path: path, Reason: "relationship keys or cardinality changed; review embedded queries", Breaking: true})
		}
		delete(next, identity)
	}
	for _, relationship := range next {
		breaking := dataAPIRelationshipMayAmbiguate(before, old, relationship)
		reason := "relationship added"
		if breaking {
			reason = "relationship added; existing unqualified embeds or column hints may become ambiguous"
		}
		changes = append(changes, dataAPIChange{Path: key + "[" + relationship.Name + "->" + relationship.ReferencedRelation + "]", Reason: reason, Breaking: breaking})
	}
	return changes
}

// Repeated view aliases cannot be keyed uniquely, but ordering alone must not
// turn an otherwise identical multiset of relationship metadata into a break.
func comparableDataAPIRelationshipJSON(raw json.RawMessage) string {
	var entries []map[string]json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return compactDataAPIJSON(raw)
	}
	canonical := make([]string, 0, len(entries))
	for _, entry := range entries {
		content, err := json.Marshal(entry)
		if err != nil {
			return compactDataAPIJSON(raw)
		}
		canonical = append(canonical, string(content))
	}
	sort.Strings(canonical)
	return strings.Join(canonical, "\n")
}

// A second connection between the same relations can break embeds in either
// direction. Shared source columns/names can also break existing FK hints.
func dataAPIRelationshipMayAmbiguate(before dataAPISnapshot, source dataAPITable, added dataAPIRelationship) bool {
	for _, table := range before.Tables {
		if table.Schema != source.Schema {
			continue
		}
		relationships, valid := dataAPIRelationships(table.Relationships)
		if !valid {
			return true
		}
		for _, relationship := range relationships {
			// Constraint names are also selectors on reverse embeds. Treat
			// reuse conservatively even when the source relations differ.
			if relationship.Name == added.Name {
				return true
			}
			if table.Name == source.Name {
				selectors := append([]string{relationship.Name, relationship.ReferencedRelation}, relationship.Columns...)
				candidates := append([]string{added.Name, added.ReferencedRelation}, added.Columns...)
				for _, selector := range selectors {
					for _, candidate := range candidates {
						if selector == candidate {
							return true
						}
					}
				}
			}
			if table.Name == added.ReferencedRelation && relationship.ReferencedRelation == source.Name {
				return true
			}
		}
	}
	return false
}
