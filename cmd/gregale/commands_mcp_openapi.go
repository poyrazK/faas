package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcpopenapi"
)

func cmdMCPImportOpenAPI(args []string) int {
	fs := newFlagSet("mcp-import", flag.ContinueOnError)
	spec := fs.String("spec", "", "OpenAPI JSON/YAML file, or - for stdin")
	dest := fs.String("path", "", "empty destination for the generated Node project")
	server := fs.String("server-url", "", "explicit public HTTPS API base URL")
	operations := fs.String("operations", "", "comma-separated exact operation IDs")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *spec == "" || *dest == "" || *server == "" || *operations == "" {
		return printErr("Import OpenAPI", errors.New("--spec, --path, --server-url and --operations are required; positional arguments are not accepted"))
	}
	data, err := readOpenAPIDocument(*spec)
	if err != nil {
		return printErr("Read OpenAPI", err)
	}
	project, err := mcpopenapi.Generate(data, mcpopenapi.Options{ServerURL: *server, OperationIDs: strings.Split(*operations, ",")})
	if err != nil {
		return printErr("Generate API tools", err)
	}
	if err := materializeOpenAPIProject(*dest, project); err != nil {
		return printErr("Create API tool project", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"status": "created", "runtime": "node", "path": *dest,
			"document_sha256": project.Manifest.DocumentSHA256, "server_url": project.Manifest.ServerURL,
			"operation_ids": strings.Split(*operations, ","), "fixtures_require_review": true}))
	}
	PrintOK(osStdout, "Created Node API tool project at %s", *dest)
	_, _ = fmt.Fprintln(osStdout, "Read README.md. Complete the example fixtures before reviewed publication; generation made no API calls.")
	return 0
}

func readOpenAPIDocument(path string) ([]byte, error) {
	var input io.Reader = os.Stdin
	if path != "-" {
		file, err := openCustomerFile(path)
		if err != nil {
			return nil, fmt.Errorf("open specification: %w", err)
		}
		defer func() { _ = file.Close() }()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("specification must be a regular file")
		}
		input = file
	}
	data, err := io.ReadAll(io.LimitReader(input, api.ToolOpenAPIDocumentMaxBytes+1))
	if err != nil || len(data) > api.ToolOpenAPIDocumentMaxBytes {
		return nil, fmt.Errorf("specification exceeds the %d-byte read limit", api.ToolOpenAPIDocumentMaxBytes)
	}
	return data, nil
}

func materializeOpenAPIProject(destination string, project mcpopenapi.Project) error {
	dest, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(dest); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("destination must not be a symlink")
	}
	if err := checkDestEmpty(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".gregale-openapi-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := templates.Materialize("mcp-node", stage); err != nil {
		return err
	}
	// Starter tests exercise the example catalog. This generated application
	// supplies its own catalog checks; live behavioral fixtures remain separate.
	if err := os.RemoveAll(filepath.Join(stage, "test")); err != nil {
		return err
	}
	for name, content := range project.Files {
		target := filepath.Join(stage, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return err
		}
	}
	if err := os.Chmod(stage, 0o755); err != nil {
		return err
	}
	return os.Rename(stage, dest)
}
