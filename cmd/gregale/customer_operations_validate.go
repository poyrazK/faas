package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/operations"
)

type customerOperationValidation struct {
	App         string                                 `json:"app"`
	Plan        api.Plan                               `json:"plan"`
	Definitions []customerOperationValidatedDefinition `json:"definitions"`
}
type customerOperationValidatedDefinition struct {
	Name           string                      `json:"name"`
	Revision       string                      `json:"revision"`
	Spec           api.OperationDefinitionSpec `json:"spec"`
	InputValidated bool                        `json:"input_validated"`
}

func validateCustomerOperationSource(c customerOperationDeveloperCommand) (customerOperationValidation, error) {
	plan := api.Plan(c.plan)
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return customerOperationValidation{}, fmt.Errorf("--plan must be free, hobby, pro or scale")
	}
	root, err := os.OpenRoot(c.dir)
	if err != nil {
		return customerOperationValidation{}, fmt.Errorf("open source directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	manifest, err := readCustomerOperationManifest(root)
	if err != nil {
		return customerOperationValidation{}, err
	}
	if err := manifest.ValidateForPlan(plan); err != nil {
		return customerOperationValidation{}, fmt.Errorf("validate manifest: %w", err)
	}
	if err := manifest.ResolveOperations(c.app, plan, func(name string, limit int) ([]byte, error) {
		return readCustomerOperationSourceFile(root, name, int64(limit))
	}); err != nil {
		return customerOperationValidation{}, err
	}
	if len(manifest.ResolvedOperations) == 0 {
		return customerOperationValidation{}, fmt.Errorf("no Operations definitions select app %q", c.app)
	}
	report := customerOperationValidation{App: c.app, Plan: plan, Definitions: []customerOperationValidatedDefinition{}}
	var sample []byte
	if c.input != "" {
		sample, err = readCustomerOperationFile(c.input, api.OperationSubmissionMaxBytes)
		if err != nil {
			return report, err
		}
	}
	found := c.name == ""
	for _, spec := range manifest.ResolvedOperations {
		if c.name != "" && spec.Name != c.name {
			continue
		}
		found = true
		contract, err := operations.Compile(spec, limits.Operations)
		if err != nil {
			return report, err
		}
		entry := customerOperationValidatedDefinition{Name: spec.Name, Revision: contract.Revision, Spec: contract.Spec}
		if c.input != "" {
			if err := contract.ValidateInput(sample, api.OperationSubmissionMaxBytes); err != nil {
				return report, fmt.Errorf("operation %q sample input: %w", spec.Name, err)
			}
			entry.InputValidated = true
		}
		report.Definitions = append(report.Definitions, entry)
	}
	if !found {
		return report, fmt.Errorf("operation %q is not declared for app %q", c.name, c.app)
	}
	return report, nil
}

func readCustomerOperationManifest(root *os.Root) (*gregalemanifest.Manifest, error) {
	for _, name := range []string{"gregale.yaml", "gregale.yml", "gregale.toml"} {
		body, err := readCustomerOperationSourceFile(root, name, api.SourceManifestMaxBytes)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		var manifest *gregalemanifest.Manifest
		if name == "gregale.toml" {
			manifest, err = gregalemanifest.ParseTOMLBytes(body)
		} else {
			manifest, err = gregalemanifest.ParseBytes(body)
		}
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		if manifest == nil {
			return nil, fmt.Errorf("%s is empty", name)
		}
		return manifest, nil
	}
	return nil, fmt.Errorf("no gregale.yaml, gregale.yml or gregale.toml manifest found")
}

// Root anchors resolution to the source tree even during parent-path swaps.
// Reject symlinks and non-regular files just as the server's archive reader does.
func readCustomerOperationSourceFile(root *os.Root, name string, limit int64) ([]byte, error) {
	if !filepath.IsLocal(name) || filepath.Clean(name) != name || strings.Contains(name, "\\") {
		return nil, fmt.Errorf("schema path must be source-local")
	}
	parts := strings.Split(name, string(filepath.Separator))
	for i := range parts {
		info, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			return nil, fmt.Errorf("source file must be regular, with no symlink components")
		}
	}
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open bounded source file: %w", err)
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	after, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(after, opened) || after.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("source file changed during validation")
	}
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read bounded source file: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("source file exceeds %d bytes", limit)
	}
	return body, nil
}

func renderCustomerOperationValidation(out io.Writer, report customerOperationValidation, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(out).Encode(report)
	}
	for _, d := range report.Definitions {
		if _, err := fmt.Fprintf(out, "Validated %s\trevision=%s\t%s %s\tsample_input=%t\thttp_transaction_version=%d\n", d.Name, d.Revision, d.Spec.Method, d.Spec.Path, d.InputValidated, d.Spec.HTTPTransactionVersion); err != nil {
			return err
		}
	}
	return nil
}
