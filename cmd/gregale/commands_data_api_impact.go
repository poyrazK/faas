package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Dependencies are customer-declared contract paths, not inferred SQL lineage.
type dataAPIConsumer struct {
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Dependencies []string `json:"dependencies"`
}
type dataAPIImpact struct {
	Kind    string          `json:"kind"`
	Name    string          `json:"name"`
	Changes []dataAPIChange `json:"changes"`
}

func readDataAPIConsumers(path string) ([]dataAPIConsumer, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, 1048577))
	if err != nil {
		return nil, err
	}
	if len(raw) > 1048576 {
		return nil, errors.New("dependency manifest exceeds 1 MiB")
	}
	var manifest struct {
		Version   int               `json:"version"`
		Consumers []dataAPIConsumer `json:"consumers"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("dependency manifest must contain one JSON object")
	}
	if manifest.Version != 1 || manifest.Consumers == nil {
		return nil, errors.New("expected version 1 and consumers array")
	}
	seen := map[string]bool{}
	for _, consumer := range manifest.Consumers {
		if consumer.Kind != "saved_view" && consumer.Kind != "rpc" {
			return nil, errors.New("consumer kind must be saved_view or rpc")
		}
		if strings.TrimSpace(consumer.Name) == "" || len(consumer.Dependencies) == 0 {
			return nil, errors.New("each consumer requires a name and dependencies")
		}
		key := consumer.Kind + "\x00" + consumer.Name
		if seen[key] {
			return nil, errors.New("duplicate consumer")
		}
		seen[key] = true
		for _, dependency := range consumer.Dependencies {
			if dependency == "" || strings.TrimSpace(dependency) != dependency || strings.ContainsAny(dependency, "\r\n\t") {
				return nil, fmt.Errorf("invalid dependency for %q", consumer.Name)
			}
		}
	}
	return manifest.Consumers, nil
}
func dataAPIPathContains(parent, child string) bool {
	return parent == child || strings.HasPrefix(child, parent+".") || strings.HasPrefix(child, parent+"(") || strings.HasPrefix(child, parent+"[")
}
func dataAPIConsumerImpacts(consumers []dataAPIConsumer, changes []dataAPIChange) []dataAPIImpact {
	impacts := []dataAPIImpact{}
	for _, consumer := range consumers {
		impact := dataAPIImpact{Kind: consumer.Kind, Name: consumer.Name, Changes: []dataAPIChange{}}
		for _, change := range changes {
			for _, dependency := range consumer.Dependencies {
				if dataAPIPathContains(dependency, change.Path) || dataAPIPathContains(change.Path, dependency) {
					impact.Changes = append(impact.Changes, change)
					break
				}
			}
		}
		if len(impact.Changes) > 0 {
			impacts = append(impacts, impact)
		}
	}
	sort.Slice(impacts, func(i, j int) bool {
		if impacts[i].Kind == impacts[j].Kind {
			return impacts[i].Name < impacts[j].Name
		}
		return impacts[i].Kind < impacts[j].Kind
	})
	return impacts
}

func validateDataAPIConsumers(consumers []dataAPIConsumer, snapshots ...dataAPISnapshot) error {
	paths := map[string]bool{}
	for _, snapshot := range snapshots {
		for _, schema := range snapshot.Schemas {
			paths[schema] = true
		}
		for _, table := range snapshot.Tables {
			key := table.Schema + "." + table.Name
			paths[key] = true
			for _, column := range table.Columns {
				paths[key+"."+column.Name] = true
			}
		}
		for _, fn := range snapshot.Functions {
			key := fn.Schema + "." + fn.Name
			paths[key] = true
			for _, arg := range fn.Args {
				paths[key+"("+arg.Name+")"] = true
			}
		}
		for key := range snapshot.Enums {
			paths[key] = true
		}
		for _, composite := range snapshot.Composites {
			key := composite.Schema + "." + composite.Name
			paths[key] = true
			for _, field := range composite.Fields {
				paths[key+"."+field.Name] = true
			}
		}
	}
	for _, consumer := range consumers {
		for _, dependency := range consumer.Dependencies {
			if !paths[dependency] {
				return fmt.Errorf("consumer %q references unknown contract path %q", consumer.Name, dependency)
			}
		}
	}
	return nil
}
