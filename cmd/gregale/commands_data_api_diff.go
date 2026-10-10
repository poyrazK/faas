package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dataapi"
)

type dataAPIColumn = dataapi.Column
type dataAPIArgument = dataapi.Argument
type dataAPITable = dataapi.Table
type dataAPIFunction = dataapi.Function
type dataAPISnapshot = dataapi.Snapshot
type dataAPIContract = dataapi.Contract

type dataAPIChange struct {
	Path     string `json:"path"`
	Breaking bool   `json:"breaking"`
	Reason   string `json:"reason"`
}

func parseDataAPIContract(content []byte) (dataAPIContract, error) { return dataapi.Parse(content) }

func generateDataAPIContract(ctx context.Context, client *api.Client, slug string) (api.AppTaskResponse, error) {
	task, err := client.CreateAppTask(ctx, slug, api.CreateAppTaskRequest{Command: []string{"node", "/app/types.mjs", "--snapshot"}, TimeoutSeconds: 60, MaxOutputBytes: api.AppTaskDefaultMaxOutputBytes})
	if err != nil {
		return task, fmt.Errorf("start schema inspection: %w", err)
	}
	return waitDataAPIContractTask(ctx, client, slug, task, true)
}

func cmdDataAPIDiff(args []string) int {
	fs := newFlagSet("data-api diff", flag.ContinueOnError)
	baseline := fs.String("baseline", "", "saved JSON contract from data-api types --snapshot")
	current := fs.String("current", "", "compare a saved current contract offline instead of inspecting an app")
	check := fs.Bool("check", false, "fail if the database contract contains breaking changes")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum private inspection task wait")
	if err := fs.Parse(normalizeDataAPITypeArgs(args)); err != nil {
		return 1
	}
	validTarget := (*current == "" && fs.NArg() == 1 && api.ValidAppSlug(fs.Arg(0))) || (*current != "" && fs.NArg() == 0)
	if !validTarget || *baseline == "" || *timeout <= 0 || *timeout > time.Hour {
		PrintUsage(os.Stderr, "usage: gregale data-api diff [NAME | --current FILE] --baseline FILE [--check] [--timeout DURATION]", "data-api")
		return 1
	}
	// Read and validate the baseline before starting any remote task.
	before, err := readDataAPIContractFile(*baseline)
	if err != nil {
		return printErr("Invalid baseline", err)
	}
	var after dataAPIContract
	var task api.AppTaskResponse
	if *current != "" {
		after, err = readDataAPIContractFile(*current)
	} else {
		client, clientErr := authedClient()
		if clientErr != nil {
			return printErr("Not logged in", clientErr)
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		task, err = generateDataAPIContract(ctx, client, fs.Arg(0))
		if err != nil {
			return printErr("Schema inspection failed", err)
		}
		after, err = parseDataAPIContract([]byte(task.StdoutTail))
	}
	if err != nil {
		return printErr("Invalid current contract", err)
	}
	changes := diffDataAPISnapshots(before.Parsed, after.Parsed)
	breaking := 0
	for _, change := range changes {
		if change.Breaking {
			breaking++
		}
	}
	if jsonOutput {
		if err := writeJSON(map[string]any{"app": fs.Arg(0), "task_id": task.ID, "deployment_id": task.DeploymentID, "baseline_fingerprint": before.Fingerprint, "current_fingerprint": after.Fingerprint, "breaking": breaking, "changes": changes}); err != nil {
			return 1
		}
	} else {
		for _, change := range changes {
			label := "compatible"
			if change.Breaking {
				label = "breaking"
			}
			if _, err := fmt.Fprintf(osStdout, "%s %q: %s\n", label, change.Path, change.Reason); err != nil {
				return printErr("Could not write diff", err)
			}
		}
		if _, err := fmt.Fprintf(osStdout, "%d changes, %d breaking\n", len(changes), breaking); err != nil {
			return 1
		}
	}
	if *check && breaking > 0 {
		return 1
	}
	return 0
}

func readDataAPIContractFile(path string) (dataAPIContract, error) {
	file, err := openCustomerFile(path)
	if err != nil {
		return dataAPIContract{}, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, api.AppTaskDefaultMaxOutputBytes+1))
	if err != nil {
		return dataAPIContract{}, err
	}
	return parseDataAPIContract(content)
}

func diffDataAPISnapshots(before, after dataAPISnapshot) []dataAPIChange {
	changes := []dataAPIChange{}
	add := func(path, reason string, breaking bool) {
		changes = append(changes, dataAPIChange{path, breaking, reason})
	}
	tables := map[string]dataAPITable{}
	for _, table := range after.Tables {
		tables[table.Schema+"."+table.Name] = table
	}
	for _, old := range before.Tables {
		key := old.Schema + "." + old.Name
		current, ok := tables[key]
		if !ok {
			add(key, "relation removed; update queries and generated types", true)
			continue
		}
		delete(tables, key)
		if old.View != current.View {
			add(key, "relation changed between table and view", true)
		}
		if compactDataAPIJSON(old.Relationships) != compactDataAPIJSON(current.Relationships) {
			add(key, "relationship metadata changed; review embedded queries", true)
		}
		columns := map[string]dataAPIColumn{}
		for _, column := range current.Columns {
			columns[column.Name] = column
		}
		for _, column := range old.Columns {
			next, ok := columns[column.Name]
			path := key + "." + column.Name
			if !ok {
				add(path, "column removed; update projections and writes", true)
				continue
			}
			delete(columns, column.Name)
			if column.Type != next.Type {
				add(path, "field type changed; update readers and writers", true)
			}
			if column.Nullable != next.Nullable {
				add(path, "nullability changed; review readers and writers", true)
			}
			if column.Insertable && !next.Insertable || column.Updatable && !next.Updatable {
				add(path, "write access removed", true)
			}
			if !old.View && next.Insertable && !next.Optional && (!column.Insertable || column.Optional) {
				add(path, "insert now requires this field", true)
			}
			if !column.Insertable && next.Insertable || !column.Updatable && next.Updatable || !column.Optional && next.Optional {
				add(path, "write contract relaxed", false)
			}
		}
		for _, column := range columns {
			breaking := !current.View && column.Insertable && !column.Optional
			reason := "column added"
			if breaking {
				reason = "column added; inserts now require this field"
			}
			add(key+"."+column.Name, reason, breaking)
		}
	}
	for key := range tables {
		add(key, "relation added", false)
	}
	functions := map[string]dataAPIFunction{}
	for _, fn := range after.Functions {
		functions[fn.Schema+"."+fn.Name] = fn
	}
	for _, old := range before.Functions {
		key := old.Schema + "." + old.Name
		current, ok := functions[key]
		if !ok {
			add(key, "RPC removed; update calls", true)
			continue
		}
		delete(functions, key)
		if old.Returns != current.Returns {
			add(key, "RPC return type changed", true)
		}
		args := map[string]dataAPIArgument{}
		for _, arg := range current.Args {
			args[arg.Name] = arg
		}
		for _, arg := range old.Args {
			next, ok := args[arg.Name]
			path := key + "(" + arg.Name + ")"
			if !ok {
				add(path, "RPC argument removed", true)
				continue
			}
			delete(args, arg.Name)
			if arg.Type != next.Type {
				add(path, "RPC argument type changed", true)
			}
			if arg.Optional != next.Optional {
				add(path, "RPC argument optionality changed", !next.Optional)
			}
		}
		for _, arg := range args {
			add(key+"("+arg.Name+")", "RPC argument added", !arg.Optional)
		}
	}
	for key := range functions {
		add(key, "RPC added", false)
	}
	schemas := map[string]bool{}
	for _, schema := range after.Schemas {
		schemas[schema] = true
	}
	for _, schema := range before.Schemas {
		if !schemas[schema] {
			add(schema, "exposed schema removed", true)
		} else {
			delete(schemas, schema)
		}
	}
	for schema := range schemas {
		add(schema, "exposed schema added", false)
	}
	enumKeys := map[string]bool{}
	for key, labels := range before.Enums {
		enumKeys[key] = true
		if !reflect.DeepEqual(labels, after.Enums[key]) {
			add(key, "enum values changed; review readers and writers", true)
		}
	}
	for key := range after.Enums {
		if !enumKeys[key] {
			add(key, "enum added", false)
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path == changes[j].Path {
			return changes[i].Reason < changes[j].Reason
		}
		return changes[i].Path < changes[j].Path
	})
	return changes
}

func compactDataAPIJSON(content json.RawMessage) string {
	var b bytes.Buffer
	_ = json.Compact(&b, content)
	return b.String()
}
