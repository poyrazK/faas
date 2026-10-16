package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type dataAPIGuideChange struct {
	dataAPIChange
	Before   any      `json:"before"`
	After    any      `json:"after"`
	Actions  []string `json:"actions"`
	Examples []string `json:"examples"`
}
type dataAPIGuideRequest struct {
	Scenario string          `json:"scenario"`
	Name     string          `json:"name"`
	Relation string          `json:"relation"`
	Changes  []dataAPIChange `json:"changes"`
}
type dataAPIGuideReview struct {
	Relation string                   `json:"relation"`
	Reason   string                   `json:"reason"`
	Checks   []dataAPICoverageMapping `json:"checks"`
}
type dataAPIMigrationGuide struct {
	Version      int                   `json:"version"`
	Command      string                `json:"command"`
	Baseline     string                `json:"baseline_fingerprint"`
	Current      string                `json:"current_fingerprint"`
	Status       string                `json:"status"`
	Changes      []dataAPIGuideChange  `json:"changes"`
	Consumers    []dataAPIImpact       `json:"consumers"`
	Requests     []dataAPIGuideRequest `json:"requests"`
	Verification []dataAPIGuideReview  `json:"verification_review"`
	Coverage     map[string]string     `json:"analysis_coverage"`
	Notes        []string              `json:"notes"`
}

func dataAPIGuideShape(snapshot dataAPISnapshot, path string) any {
	for _, table := range snapshot.Tables {
		key := table.Schema + "." + table.Name
		if path == key {
			return table
		}
		if relationships, valid := dataAPIRelationships(table.Relationships); valid {
			for _, relationship := range relationships {
				if path == key+"["+relationship.Name+"->"+relationship.ReferencedRelation+"]" {
					return relationship
				}
			}
		}
		for _, column := range table.Columns {
			if path == key+"."+column.Name {
				return column
			}
		}
	}
	for _, fn := range snapshot.Functions {
		key := fn.Schema + "." + fn.Name
		if path == key {
			return fn
		}
		for _, arg := range fn.Args {
			if path == key+"("+arg.Name+")" {
				return arg
			}
		}
	}
	for key, values := range snapshot.Enums {
		if path == key {
			return values
		}
	}
	for _, composite := range snapshot.Composites {
		key := composite.Schema + "." + composite.Name
		if path == key {
			return composite
		}
		for _, field := range composite.Fields {
			if path == key+"."+field.Name {
				return field
			}
		}
	}
	for _, schema := range snapshot.Schemas {
		if path == schema {
			return schema
		}
	}
	return nil
}
func dataAPIGuideActions(change dataAPIChange, before, after any) ([]string, []string) {
	actions := []string{"Regenerate database types from the candidate schema and typecheck consumers", "Review fixture assertions and rerun the local release checks"}
	examples := []string{}
	oldColumn, wasColumn := before.(dataAPIColumn)
	newColumn, isColumn := after.(dataAPIColumn)
	if wasColumn || isColumn {
		if wasColumn {
			examples = append(examples, fmt.Sprintf("// Before: field type %s; nullable=%t; insert optional=%t", oldColumn.Type, oldColumn.Nullable, oldColumn.Optional))
		}
		if isColumn {
			examples = append(examples, fmt.Sprintf("// After: field type %s; nullable=%t; insert optional=%t", newColumn.Type, newColumn.Nullable, newColumn.Optional))
		}
		if !isColumn {
			actions = append(actions, "Remove this field from select, filter, order and mutation payloads; choose a replacement only after reviewing the application")
		}
		if isColumn && newColumn.Insertable && !newColumn.Optional && (!wasColumn || oldColumn.Optional || !oldColumn.Insertable) {
			actions = append(actions, "Supply this required field in insert payloads; choose a domain-valid fixture/application value")
			examples = append(examples, "// Before: payload omits this field", "// After: const payload = { ...existingPayload, ["+fmt.Sprintf("%q", newColumn.Name)+"]: reviewedValue }")
		}
		if isColumn && wasColumn && oldColumn.Nullable && !newColumn.Nullable {
			actions = append(actions, "Stop sending null for this field and review fixture backfills")
		}
		if isColumn && newColumn.Nullable && (!wasColumn || !oldColumn.Nullable) {
			actions = append(actions, "Handle null before using the returned field")
			examples = append(examples, "// Before: consume(row["+fmt.Sprintf("%q", newColumn.Name)+"])", "// After: const value = row["+fmt.Sprintf("%q", newColumn.Name)+"]; if (value !== null) consume(value)")
		}
		if wasColumn && isColumn && oldColumn.Type != newColumn.Type {
			actions = append(actions, "Update field readers, serializers and validation for the candidate TypeScript type; do not infer an automatic conversion")
		}
		if wasColumn && isColumn && ((oldColumn.Insertable && !newColumn.Insertable) || (oldColumn.Updatable && !newColumn.Updatable)) {
			actions = append(actions, "Omit this field from writes that are no longer supported")
		}
	}
	if oldArg, ok := before.(dataAPIArgument); ok {
		examples = append(examples, fmt.Sprintf("// Before RPC argument: %s; optional=%t", oldArg.Type, oldArg.Optional))
	}
	if newArg, ok := after.(dataAPIArgument); ok {
		examples = append(examples, fmt.Sprintf("// After RPC argument: %s; optional=%t", newArg.Type, newArg.Optional))
		if !newArg.Optional {
			actions = append(actions, "Supply the required named RPC argument and update call-site validation")
		}
	}
	if _, ok := before.(dataAPIFunction); ok {
		actions = append(actions, "Review RPC payloads, result handling and embedded projections")
	}
	if _, ok := before.([]string); ok {
		actions = append(actions, "Review exhaustive enum handlers and validation; do not guess replacement values")
	}
	if _, ok := after.(dataAPICompositeField); ok {
		actions = append(actions, "Update nested composite objects and RPC serializers for the candidate field shape")
	}
	if after == nil && before != nil {
		actions = append(actions, "Remove or replace uses of the removed contract element; renames are not inferred")
	}
	if strings.Contains(change.Reason, "relationship") {
		actions = append(actions, "Review embedded select expressions, relationship hints and cardinality assumptions")
	}
	return actions, examples
}

type dataAPIGuideSavedRequest struct {
	Name   string `json:"name"`
	Method string `json:"method"`
	Path   string `json:"path"`
}
type dataAPIGuideScenario struct {
	Name     string                     `json:"name"`
	Requests []dataAPIGuideSavedRequest `json:"requests"`
}

func readDataAPIGuideRequests(path string) ([]dataAPIGuideScenario, error) {
	var collection struct {
		Version   int                        `json:"version"`
		Requests  []dataAPIGuideSavedRequest `json:"requests"`
		Scenarios []dataAPIGuideScenario     `json:"scenarios"`
	}
	if err := verificationJSON(path, &collection, 65536); err != nil {
		return nil, err
	}
	scenarios := collection.Scenarios
	if collection.Version == 1 {
		scenarios = []dataAPIGuideScenario{{Name: "default", Requests: collection.Requests}}
	} else if collection.Version != 2 || len(scenarios) == 0 {
		return nil, errors.New("use a version 1 or 2 saved request collection")
	}
	count := 0
	seenScenarios := map[string]bool{}
	for _, scenario := range scenarios {
		if scenario.Name == "" || len(scenario.Name) > 64 || seenScenarios[scenario.Name] {
			return nil, errors.New("invalid or duplicate scenario")
		}
		seenScenarios[scenario.Name] = true
		seen := map[string]bool{}
		for _, request := range scenario.Requests {
			count++
			target, err := url.ParseRequestURI(request.Path)
			if count > 100 || request.Name == "" || len(request.Name) > 100 || seen[request.Name] || !containsString([]string{"GET", "POST", "PATCH", "DELETE"}, request.Method) || err != nil || len(request.Path) > 4096 || !strings.HasPrefix(request.Path, "/rest/v1/") || target.Host != "" || target.Scheme != "" || target.Fragment != "" || strings.ContainsAny(request.Path, "\\\r\n\t #") {
				return nil, errors.New("invalid bounded saved request metadata")
			}
			for _, part := range strings.Split(target.Path, "/") {
				if part == "." || part == ".." {
					return nil, errors.New("request paths must not traverse directories")
				}
			}
			seen[request.Name] = true
		}
	}
	return scenarios, nil
}
func dataAPIGuideRoot(path string) string {
	target, err := url.ParseRequestURI(path)
	if err != nil {
		return ""
	}
	name := strings.TrimPrefix(target.Path, "/rest/v1/")
	name = strings.TrimPrefix(name, "rpc/")
	if name == "" || strings.Contains(name, "/") {
		return ""
	}
	return "api." + name
}
func buildDataAPIMigrationGuide(before, after dataAPIContract, consumers []dataAPIConsumer, scenarios []dataAPIGuideScenario, policy *dataAPICoveragePolicy) dataAPIMigrationGuide {
	changes := diffDataAPISnapshots(before.Parsed, after.Parsed)
	guide := dataAPIMigrationGuide{Version: 1, Command: "data-api migration guide", Baseline: before.Fingerprint, Current: after.Fingerprint, Status: "no_changes", Changes: []dataAPIGuideChange{}, Consumers: dataAPIConsumerImpacts(consumers, changes), Requests: []dataAPIGuideRequest{}, Verification: []dataAPIGuideReview{}, Coverage: map[string]string{}, Notes: []string{"Suggestions require review; application files are never rewritten", "Examples are illustrative: replace existingPayload, reviewedValue and consume with application code", "Saved-request matching covers root API relations/RPCs conservatively, not embedded or computed SQL lineage", "Enum, composite and schema changes may affect additional consumers; declare their dependencies", "A matching schema does not prove that fixtures, permissions or query budgets remain valid"}}
	if len(changes) > 0 {
		guide.Status = "needs_review"
	}
	for _, change := range changes {
		old, next := dataAPIGuideShape(before.Parsed, change.Path), dataAPIGuideShape(after.Parsed, change.Path)
		actions, examples := dataAPIGuideActions(change, old, next)
		guide.Changes = append(guide.Changes, dataAPIGuideChange{dataAPIChange: change, Before: old, After: next, Actions: actions, Examples: examples})
	}
	for _, scenario := range scenarios {
		for _, request := range scenario.Requests {
			root := dataAPIGuideRoot(request.Path)
			matched := []dataAPIChange{}
			for _, change := range changes {
				if root != "" && (dataAPIPathContains(root, change.Path) || dataAPIPathContains(change.Path, root)) {
					matched = append(matched, change)
				}
			}
			if len(matched) > 0 {
				guide.Requests = append(guide.Requests, dataAPIGuideRequest{Scenario: scenario.Name, Name: request.Name, Relation: root, Changes: matched})
			}
		}
	}
	if policy != nil {
		relations := map[string]bool{}
		for _, snapshot := range []dataAPISnapshot{before.Parsed, after.Parsed} {
			for _, table := range snapshot.Tables {
				relations[table.Schema+"."+table.Name] = true
			}
		}
		mapped := map[string]dataAPICoverageRule{}
		for _, rule := range policy.Relations {
			mapped[rule.Relation] = rule
		}
		keys := []string{}
		for relation := range relations {
			keys = append(keys, relation)
		}
		sort.Strings(keys)
		sharedTypes := false
		for _, change := range changes {
			for key := range before.Parsed.Enums {
				sharedTypes = sharedTypes || dataAPIPathContains(key, change.Path)
			}
			for key := range after.Parsed.Enums {
				sharedTypes = sharedTypes || dataAPIPathContains(key, change.Path)
			}
			for _, snapshot := range []dataAPISnapshot{before.Parsed, after.Parsed} {
				for _, composite := range snapshot.Composites {
					sharedTypes = sharedTypes || dataAPIPathContains(composite.Schema+"."+composite.Name, change.Path)
				}
			}
		}
		for _, relation := range keys {
			affected := sharedTypes
			for _, change := range changes {
				if dataAPIPathContains(relation, change.Path) || dataAPIPathContains(change.Path, relation) {
					affected = true
				}
			}
			if !affected {
				continue
			}
			rule, declared := mapped[relation]
			reason := "Review mapped fixtures, integrity/access assertions and query budgets"
			checks := rule.Checks
			if checks == nil {
				checks = []dataAPICoverageMapping{}
			}
			if sharedTypes {
				reason = "Review shared enum/composite changes across mapped relation assertions"
			}
			if !declared {
				reason = "Review default coverage requirements and add explicit mappings for this changed relation"
			}
			guide.Verification = append(guide.Verification, dataAPIGuideReview{Relation: relation, Reason: reason, Checks: checks})
		}
	}
	return guide
}
func cmdDataAPIMigrationGuide(args []string) int {
	fs := newFlagSet("data-api migration guide", flag.ContinueOnError)
	baseline := fs.String("baseline", "", "previous schema contract")
	current := fs.String("current", "", "candidate schema contract")
	dependencies := fs.String("dependencies", "", "consumer dependencies")
	requests := fs.String("requests", "", "saved request collection")
	policyPath := fs.String("coverage-policy", "", "verification mappings")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	if fs.NArg() != 0 || *baseline == "" || *current == "" {
		return printErr("Invalid migration guide", errors.New("provide --baseline FILE --current FILE"))
	}
	before, err := readDataAPIContractFile(*baseline)
	if err != nil {
		return printErr("Invalid baseline contract", err)
	}
	after, err := readDataAPIContractFile(*current)
	if err != nil {
		return printErr("Invalid current contract", err)
	}
	var consumers []dataAPIConsumer
	if *dependencies != "" {
		consumers, err = readDataAPIConsumers(*dependencies)
		if err != nil {
			return printErr("Invalid consumer dependencies", errors.New("use a bounded version 1 dependency manifest"))
		}
	}
	var scenarios []dataAPIGuideScenario
	if *requests != "" {
		scenarios, err = readDataAPIGuideRequests(*requests)
		if err != nil {
			return printErr("Invalid saved requests", errors.New("use a bounded valid version 1 or 2 request collection"))
		}
	}
	var policy *dataAPICoveragePolicy
	if *policyPath != "" {
		value, err := coveragePolicy(*policyPath)
		if err != nil {
			return printErr("Invalid coverage policy", err)
		}
		policy = &value
	}
	if len(consumers) > 100 {
		return printErr("Guide input too large", errors.New("use at most 100 declared consumers"))
	}
	dependenciesCount := 0
	for _, consumer := range consumers {
		dependenciesCount += len(consumer.Dependencies)
	}
	if dependenciesCount > 1000 || len(diffDataAPISnapshots(before.Parsed, after.Parsed)) > 1000 {
		return printErr("Guide input too large", errors.New("use at most 1000 declared dependencies and 1000 contract changes"))
	}
	guide := buildDataAPIMigrationGuide(before, after, consumers, scenarios, policy)
	for key, path := range map[string]string{"declared_consumers": *dependencies, "saved_queries": *requests, "verification_mappings": *policyPath} {
		guide.Coverage[key] = "not_provided"
		if path != "" {
			guide.Coverage[key] = "provided"
		}
	}
	if jsonOutput {
		raw, _ := json.Marshal(guide)
		if _, err := fmt.Fprintln(osStdout, string(raw)); err != nil {
			return printErr("Output failed", err)
		}
	} else {
		if _, err := fmt.Fprintf(osStdout, "Data API migration guide: %s\n", guide.Status); err != nil {
			return printErr("Output failed", err)
		}
		for _, change := range guide.Changes {
			if _, err := fmt.Fprintf(osStdout, "%s: %s (breaking=%t)\n", change.Path, change.Reason, change.Breaking); err != nil {
				return printErr("Output failed", err)
			}
			old, _ := json.Marshal(change.Before)
			next, _ := json.Marshal(change.After)
			if _, err := fmt.Fprintf(osStdout, "  Before: %s\n  After: %s\n", old, next); err != nil {
				return printErr("Output failed", err)
			}
			for _, action := range change.Actions {
				if _, err := fmt.Fprintf(osStdout, "  %s\n", action); err != nil {
					return printErr("Output failed", err)
				}
			}
			for _, example := range change.Examples {
				if _, err := fmt.Fprintf(osStdout, "  %s\n", example); err != nil {
					return printErr("Output failed", err)
				}
			}
		}
		for _, consumer := range guide.Consumers {
			if _, err := fmt.Fprintf(osStdout, "Review %s %q: %d matching changes\n", consumer.Kind, consumer.Name, len(consumer.Changes)); err != nil {
				return printErr("Output failed", err)
			}
		}
		for _, request := range guide.Requests {
			if _, err := fmt.Fprintf(osStdout, "Review saved request %q/%q (%s)\n", request.Scenario, request.Name, request.Relation); err != nil {
				return printErr("Output failed", err)
			}
		}
		for _, review := range guide.Verification {
			if _, err := fmt.Fprintf(osStdout, "Verification %s: %s\n", review.Relation, review.Reason); err != nil {
				return printErr("Output failed", err)
			}
		}
		for _, key := range []string{"declared_consumers", "saved_queries", "verification_mappings"} {
			if _, err := fmt.Fprintf(osStdout, "%s: %s\n", key, guide.Coverage[key]); err != nil {
				return printErr("Output failed", err)
			}
		}
		for _, note := range guide.Notes {
			if _, err := fmt.Fprintln(osStdout, note); err != nil {
				return printErr("Output failed", err)
			}
		}
	}
	return 0
}
