package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type executionOutputFileFlags []string

func (f *executionOutputFileFlags) String() string        { return strings.Join(*f, ",") }
func (f *executionOutputFileFlags) Set(name string) error { *f = append(*f, name); return nil }

func cmdRunsArtifacts(args []string) int {
	fs := newFlagSet("runs-artifacts", flag.ContinueOnError)
	dir := fs.String("output-dir", "", "local destination directory")
	flags, positional := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 1 || *dir == "" {
		PrintUsage(osStderr, "usage: gregale runs artifacts ID --output-dir DIR", "runs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	receipt, err := client.GetExecution(context.Background(), positional[0])
	if err != nil {
		return printErr("Run request failed", err)
	}
	if receipt.Status != api.ExecutionStatusSucceeded {
		return printErr("Artifacts unavailable", fmt.Errorf("run has status %s", receipt.Status))
	}
	if err := saveExecutionArtifacts(receipt, *dir); err != nil {
		return printErr("Could not save artifacts", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	PrintOK(osStdout, "Saved %d artifacts to %s.", len(receipt.Artifacts), *dir)
	return 0
}

// Save only on explicit request, validate all bytes before writing, and never
// overwrite a file. os.Root confines directory traversal even during races.
func saveExecutionArtifacts(receipt api.ExecutionResponse, dir string) error {
	if dir == "" || receipt.Status != api.ExecutionStatusSucceeded {
		return nil
	}
	if err := api.ValidateExecutionArtifacts(receipt.Artifacts); err != nil {
		return fmt.Errorf("validate artifacts: %w", err)
	}
	if len(receipt.Artifacts) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open output directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	for _, artifact := range receipt.Artifacts {
		if parent := path.Dir(artifact.Name); parent != "." {
			if err := root.MkdirAll(parent, 0o700); err != nil {
				return fmt.Errorf("create artifact directory: %w", err)
			}
		}
		file, err := root.OpenFile(artifact.Name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create artifact %q: %w", artifact.Name, err)
		}
		_, writeErr := file.Write(artifact.Content)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			_ = root.Remove(artifact.Name)
			if writeErr != nil {
				return fmt.Errorf("write artifact: %w", writeErr)
			}
			return fmt.Errorf("close artifact: %w", closeErr)
		}
	}
	return nil
}
