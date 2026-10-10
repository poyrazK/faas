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
)

type Column struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	Optional   bool   `json:"optional"`
	Insertable bool   `json:"insertable"`
	Updatable  bool   `json:"updatable"`
}
type Table struct {
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
}
type Snapshot struct {
	Version   int                 `json:"version"`
	Schemas   []string            `json:"schemas"`
	Tables    []Table             `json:"tables"`
	Functions []Function          `json:"functions"`
	Enums     map[string][]string `json:"enums"`
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
	for _, group := range []struct {
		key         string
		fields      []string
		children    string
		childFields []string
	}{
		{"tables", []string{"schema", "name", "view", "columns", "relationships"}, "columns", []string{"name", "type", "nullable", "optional", "insertable", "updatable"}},
		{"functions", []string{"schema", "name", "args", "returns"}, "args", []string{"name", "type", "optional"}},
	} {
		var entries []json.RawMessage
		if json.Unmarshal(root[group.key], &entries) != nil {
			return errors.New("invalid snapshot entries")
		}
		for _, rawEntry := range entries {
			entry, err := requiredFields(rawEntry, group.fields...)
			if err != nil {
				return err
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
	if snapshot.Version != 1 || len(snapshot.Schemas) == 0 || snapshot.Tables == nil || snapshot.Functions == nil || snapshot.Enums == nil {
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
	for _, table := range snapshot.Tables {
		key := table.Schema + "." + table.Name
		if !schemas[table.Schema] || table.Name == "" || names[key] || len(table.Columns) == 0 || len(table.Relationships) == 0 {
			return invalid
		}
		names[key] = true
		columns := map[string]bool{}
		for _, column := range table.Columns {
			if column.Name == "" || column.Type == "" || columns[column.Name] {
				return invalid
			}
			columns[column.Name] = true
		}
	}
	names = map[string]bool{}
	for _, fn := range snapshot.Functions {
		key := fn.Schema + "." + fn.Name
		if !schemas[fn.Schema] || fn.Name == "" || fn.Returns == "" || names[key] || fn.Args == nil {
			return invalid
		}
		names[key] = true
		args := map[string]bool{}
		for _, arg := range fn.Args {
			if arg.Name == "" || arg.Type == "" || args[arg.Name] {
				return invalid
			}
			args[arg.Name] = true
		}
	}
	return nil
}
