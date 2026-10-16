// Package dataapi validates bounded schema contracts shared by the CLI and owner dashboard.
package dataapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"strings"
)

type Column struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	Optional   bool   `json:"optional"`
	Insertable bool   `json:"insertable"`
	Updatable  bool   `json:"updatable"`
}
type PrimaryKey struct {
	Columns    []string `json:"columns"`
	Filterable bool     `json:"filterable"`
}
type Concurrency struct {
	Column string `json:"column"`
	Kind   string `json:"kind"`
}
type Table struct {
	PrimaryKey    *PrimaryKey     `json:"primary_key,omitempty"`
	Concurrency   *Concurrency    `json:"concurrency,omitempty"`
	Schema        string          `json:"schema"`
	Name          string          `json:"name"`
	View          bool            `json:"view"`
	Columns       []Column        `json:"columns"`
	Relationships json.RawMessage `json:"relationships"`
}
type Argument struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Optional bool   `json:"optional"`
}
type Function struct {
	Schema  string     `json:"schema"`
	Name    string     `json:"name"`
	Args    []Argument `json:"args"`
	Returns string     `json:"returns"`
	Setof   *Setof     `json:"setof,omitempty"`
}
type Setof struct {
	From     string `json:"from"`
	To       string `json:"to"`
	OneToOne bool   `json:"one_to_one"`
}
type CompositeField struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
}
type Composite struct {
	Schema string           `json:"schema"`
	Name   string           `json:"name"`
	Fields []CompositeField `json:"fields"`
}
type Snapshot struct {
	Version    int                 `json:"version"`
	Schemas    []string            `json:"schemas"`
	Tables     []Table             `json:"tables"`
	Functions  []Function          `json:"functions"`
	Enums      map[string][]string `json:"enums"`
	Composites []Composite         `json:"composites,omitempty"`
}

// Version 1 stays byte-compatible when serialized; versions 2 through 4 require the
// composites array even for schemas without any standalone composite types.
func (s Snapshot) MarshalJSON() ([]byte, error) {
	type snapshot Snapshot
	if s.Version >= 2 {
		return json.Marshal(struct {
			snapshot
			Composites []Composite `json:"composites"`
		}{snapshot(s), s.Composites})
	}
	return json.Marshal(snapshot(s))
}

type Contract struct {
	Version     int             `json:"version"`
	Fingerprint string          `json:"fingerprint"`
	Snapshot    json.RawMessage `json:"snapshot"`
	Parsed      Snapshot        `json:"-"`
}

func decodeJSON(content []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid schema snapshot JSON")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("schema snapshot contains trailing data")
	}
	return nil
}

func Parse(content []byte) (Contract, error) {
	var contract Contract
	if len(content) > api.AppTaskDefaultMaxOutputBytes {
		return contract, errors.New("schema snapshot exceeds output limit")
	}
	if err := decodeJSON(content, &contract); err != nil {
		return contract, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, contract.Snapshot); err != nil {
		return contract, errors.New("invalid schema snapshot")
	}
	hash := sha256.Sum256(compact.Bytes())
	if contract.Version != 1 || contract.Fingerprint != fmt.Sprintf("%x", hash) {
		return contract, errors.New("unsupported snapshot version or fingerprint mismatch")
	}
	if err := decodeJSON(contract.Snapshot, &contract.Parsed); err != nil {
		return contract, err
	}
	if err := validateShape(contract.Snapshot); err != nil {
		return contract, err
	}
	if err := validateSnapshot(contract.Parsed); err != nil {
		return contract, err
	}
	return contract, nil
}

// Require all normalized fields: omitted booleans must not silently become
// false and turn an incomplete baseline into a compatible comparison.
func requiredFields(raw json.RawMessage, fields ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, errors.New("invalid normalized snapshot object")
	}
	for _, field := range fields {
		value, ok := object[field]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errors.New("normalized snapshot is missing required fields")
		}
	}
	return object, nil
}

func validateShape(raw json.RawMessage) error {
	root, err := requiredFields(raw, "version", "schemas", "tables", "functions", "enums")
	if err != nil {
		return err
	}
	var version int
	if json.Unmarshal(root["version"], &version) != nil {
		return errors.New("invalid snapshot version")
	}
	switch version {
	case 1:
		if _, present := root["composites"]; present {
			return errors.New("composite metadata requires snapshot version 2")
		}
	case 2, 3, 4:
		if _, err := requiredFields(raw, "composites"); err != nil {
			return err
		}
	}
	for _, group := range []struct {
		key         string
		fields      []string
		children    string
		childFields []string
	}{
		{"tables", []string{"schema", "name", "view", "columns", "relationships"}, "columns", []string{"name", "type", "nullable", "optional", "insertable", "updatable"}},
		{"functions", []string{"schema", "name", "args", "returns"}, "args", []string{"name", "type", "optional"}},
		{"composites", []string{"schema", "name", "fields"}, "fields", []string{"name", "type", "nullable"}},
	} {
		if group.key == "composites" && version == 1 {
			continue
		}
		var entries []json.RawMessage
		if json.Unmarshal(root[group.key], &entries) != nil {
			return errors.New("invalid snapshot entries")
		}
		for _, rawEntry := range entries {
			entry, err := requiredFields(rawEntry, group.fields...)
			if err != nil {
				return err
			}
			if group.key == "functions" {
				if setof, present := entry["setof"]; present {
					if version < 3 {
						return errors.New("result relation metadata requires snapshot version 3")
					}
					if _, err := requiredFields(setof, "from", "to", "one_to_one"); err != nil {
						return err
					}
				}
			}
			var children []json.RawMessage
			if json.Unmarshal(entry[group.children], &children) != nil {
				return errors.New("invalid snapshot fields")
			}
			for _, child := range children {
				if _, err := requiredFields(child, group.childFields...); err != nil {
					return err
				}
			}
			if group.key == "tables" {
				for _, metadata := range []struct {
					name   string
					fields []string
				}{{"primary_key", []string{"columns", "filterable"}}, {"concurrency", []string{"column", "kind"}}} {
					if value, present := entry[metadata.name]; present {
						if version < 4 {
							return errors.New("row metadata requires snapshot version 4")
						}
						if _, err := requiredFields(value, metadata.fields...); err != nil {
							return err
						}
					}
				}
				var relationships []json.RawMessage
				if json.Unmarshal(entry["relationships"], &relationships) != nil {
					return errors.New("invalid relationship metadata")
				}
				for _, relationship := range relationships {
					if _, err := requiredFields(relationship, "foreignKeyName", "columns", "isOneToOne", "referencedRelation", "referencedColumns"); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func validateSnapshot(snapshot Snapshot) error {
	invalid := errors.New("invalid normalized schema snapshot")
	if snapshot.Version < 1 || snapshot.Version > 4 || len(snapshot.Schemas) == 0 || snapshot.Tables == nil || snapshot.Functions == nil || snapshot.Enums == nil || (snapshot.Version >= 2 && snapshot.Composites == nil) {
		return invalid
	}
	schemas := map[string]bool{}
	for _, schema := range snapshot.Schemas {
		if schema == "" || schemas[schema] {
			return invalid
		}
		schemas[schema] = true
	}
	names := map[string]bool{}
	relations := map[string]Table{}
	shadowed := map[string]bool{}
	for _, table := range snapshot.Tables {
		key := table.Schema + "." + table.Name
		if !schemas[table.Schema] || table.Name == "" || names[key] || len(table.Columns) == 0 || len(table.Relationships) == 0 {
			return invalid
		}
		names[key] = true
		relations[key] = table
		shadowed[key] = true
		if snapshot.Version >= 3 {
			var keys []struct {
				Name string `json:"foreignKeyName"`
			}
			if json.Unmarshal(table.Relationships, &keys) != nil {
				return invalid
			}
			for _, constraint := range keys {
				shadowed[table.Schema+"."+constraint.Name] = true
			}
		}
		columns := map[string]bool{}
		for _, column := range table.Columns {
			if column.Name == "" || column.Type == "" || columns[column.Name] {
				return invalid
			}
			columns[column.Name] = true
		}
		if table.PrimaryKey != nil {
			if table.View || len(table.PrimaryKey.Columns) == 0 || len(table.PrimaryKey.Columns) > 16 {
				return invalid
			}
			keyColumns := map[string]bool{}
			for _, name := range table.PrimaryKey.Columns {
				if !columns[name] || keyColumns[name] {
					return invalid
				}
				keyColumns[name] = true
				for _, column := range table.Columns {
					if column.Name == name && column.Nullable {
						return invalid
					}
				}
			}
		}
		if table.Concurrency != nil {
			if table.View || table.PrimaryKey == nil || table.Concurrency.Kind != "integer_version" || !columns[table.Concurrency.Column] {
				return invalid
			}
			for _, name := range table.PrimaryKey.Columns {
				if name == table.Concurrency.Column {
					return invalid
				}
			}
			for _, column := range table.Columns {
				if column.Name == table.Concurrency.Column && (column.Nullable || column.Type != "number") {
					return invalid
				}
			}
		}
	}
	names = map[string]bool{}
	for _, fn := range snapshot.Functions {
		key := fn.Schema + "." + fn.Name
		if !schemas[fn.Schema] || fn.Name == "" || fn.Returns == "" || names[key] || fn.Args == nil {
			return invalid
		}
		names[key] = true
		if fn.Setof != nil {
			if !strings.HasSuffix(fn.Returns, "[]") || fn.Setof.To == "" {
				return invalid
			}
			if _, exists := relations[fn.Schema+"."+fn.Setof.To]; !exists {
				return invalid
			}
			if fn.Setof.From == "" {
				if fn.Setof.OneToOne {
					return invalid
				}
			} else {
				source, exists := relations[fn.Schema+"."+fn.Setof.From]
				if !exists || shadowed[key] || len(fn.Args) != 1 || fn.Args[0].Optional {
					return invalid
				}
				for _, column := range source.Columns {
					if column.Name == fn.Name {
						return invalid
					}
				}
			}
		}
		args := map[string]bool{}
		for _, arg := range fn.Args {
			if arg.Name == "" || arg.Type == "" || args[arg.Name] {
				return invalid
			}
			args[arg.Name] = true
		}
	}
	names = map[string]bool{}
	for _, composite := range snapshot.Composites {
		key := composite.Schema + "." + composite.Name
		if !schemas[composite.Schema] || composite.Name == "" || names[key] || composite.Fields == nil {
			return invalid
		}
		names[key] = true
		fields := map[string]bool{}
		for _, field := range composite.Fields {
			if field.Name == "" || field.Type == "" || fields[field.Name] {
				return invalid
			}
			fields[field.Name] = true
		}
	}
	return nil
}
