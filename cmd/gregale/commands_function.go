package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// adr: 20261111150000 — typed function-to-function clients.

const functionUsage = "usage: gregale function types [--path <dir>] [--check]"

// functionTypesProjectSlug names apps while planning types; the generated
// files carry function names only, so any valid project slug works.
const functionTypesProjectSlug = "project"

func cmdFunction(args []string) int {
	if len(args) == 0 || args[0] != "types" {
		PrintUsage(os.Stderr, functionUsage, "functions")
		return 1
	}
	return cmdFunctionTypes(args[1:], os.Stdout)
}

// cmdFunctionTypes writes each Node and Python function's typed client
// (gregale-functions.d.ts / gregale_functions.py) into its root from the
// schemas in gregale.yaml. --check writes nothing and fails when a file is
// missing or stale, for CI.
func cmdFunctionTypes(args []string, out io.Writer) int {
	fs := newFlagSet("function-types", flag.ContinueOnError)
	dir := fs.String("path", ".", "functions project directory (the one holding gregale.yaml)")
	check := fs.Bool("check", false, "write nothing; exit 1 when a generated file is missing or out of date")
	if err := fs.Parse(args); err != nil {
		PrintUsage(os.Stderr, functionUsage, "functions")
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	files, err := functionTypeFiles(*dir)
	if err != nil {
		return printErr("Could not generate function types", err)
	}
	if len(files) == 0 {
		_, _ = fmt.Fprintln(out, "No Node or Python functions; nothing to generate.")
		return 0
	}
	stale := 0
	for _, f := range files {
		path := filepath.Join(*dir, filepath.FromSlash(f.Path))
		current, err := os.ReadFile(path)
		upToDate := err == nil && bytes.Equal(current, []byte(f.Content))
		switch {
		case upToDate:
			_, _ = fmt.Fprintf(out, "%s is up to date\n", path)
		case *check:
			stale++
			_, _ = fmt.Fprintf(out, "%s is out of date\n", path)
		default:
			if err := os.WriteFile(path, []byte(f.Content), 0o644); err != nil {
				return printErr("Could not write function types", err)
			}
			_, _ = fmt.Fprintf(out, "Wrote %s\n", path)
		}
	}
	if stale > 0 {
		return printErr("Function types out of date", fmt.Errorf("%d file(s) differ from the schemas; run gregale function types", stale))
	}
	return 0
}

// functionTypeFiles plans dir's functions project and generates its
// typed-client files, with paths relative to dir.
func functionTypeFiles(dir string) ([]gregalemanifest.FunctionTypesFile, error) {
	m, ok, err := functionSetManifest(dir)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("no functions are declared in gregale.yaml or in code here; run it in a functions project or pass --path")
	}
	resolved, err := m.PlanFunctions(dir, functionTypesProjectSlug)
	if err != nil {
		return nil, err
	}
	typed, err := typedFunctions(dir, resolved, functionTypesProjectSlug)
	if err != nil {
		return nil, err
	}
	return gregalemanifest.GenerateFunctionTypes(typed)
}

func typedFunctions(projectDir string, resolved []gregalemanifest.FunctionPlan, projectSlug string) ([]gregalemanifest.TypedFunction, error) {
	specs := make([]gregalemanifest.FunctionSpec, 0, len(resolved))
	for _, p := range resolved {
		specs = append(specs, p.Spec)
	}
	graph, err := gregalemanifest.FunctionCallGraph(specs, func(name string) string {
		return gregalemanifest.FunctionSlug(projectSlug, name)
	})
	if err != nil {
		return nil, err
	}
	typed := make([]gregalemanifest.TypedFunction, 0, len(resolved))
	for _, p := range resolved {
		typed = append(typed, gregalemanifest.TypedFunction{Name: p.Spec.Name, Root: p.Root, Runtime: p.Runtime,
			Input: p.Spec.InputSchema, Output: p.Spec.OutputSchema, Calls: graph[p.Spec.Name], GoPackage: goHandlerPackage(projectDir, p)})
	}
	return typed, nil
}

// refreshFunctionTypes rewrites the typed-client files a project already has
// when a schema changed since they were generated, so a Python function that
// imports gregale_functions.py ships types matching its siblings. Files the
// author never generated are left alone (ADR-20261111150000 §1).
func refreshFunctionTypes(projectDir string, resolved []gregalemanifest.FunctionPlan, projectSlug string, out io.Writer) error {
	typed, err := typedFunctions(projectDir, resolved, projectSlug)
	if err != nil {
		return err
	}
	files, err := gregalemanifest.GenerateFunctionTypes(typed)
	if err != nil {
		return err
	}
	for _, f := range files {
		path := filepath.Join(projectDir, filepath.FromSlash(f.Path))
		current, err := os.ReadFile(path)
		if err != nil || bytes.Equal(current, []byte(f.Content)) {
			continue
		}
		if err := os.WriteFile(path, []byte(f.Content), 0o644); err != nil {
			return fmt.Errorf("refresh %s: %w", path, err)
		}
		_, _ = fmt.Fprintf(out, "Updated %s from the current schemas\n", path)
	}
	return nil
}

// goHandlerPackage is the package clause of a Go function's handler, so its
// generated gregale_functions.go compiles beside it; empty (main) otherwise.
func goHandlerPackage(projectDir string, p gregalemanifest.FunctionPlan) string {
	if !strings.HasPrefix(p.Runtime, "go") || p.Handler == "" {
		return ""
	}
	src, err := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(p.Root), filepath.FromSlash(p.Handler)))
	if err != nil {
		return ""
	}
	f, err := parser.ParseFile(token.NewFileSet(), p.Handler, src, parser.PackageClauseOnly)
	if err != nil {
		return ""
	}
	return f.Name.Name
}
