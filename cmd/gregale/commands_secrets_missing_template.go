package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type missingSecretTemplateReceipt struct {
	Path           string   `json:"path"`
	Status         string   `json:"status"`
	Keys           []string `json:"keys"`
	PreviewCommand string   `json:"preview_command,omitempty"`
}

type missingSecretTemplateOutput struct {
	path      string
	name      string
	directory *artifactOutputDirectory
}

func prepareMissingSecretTemplate(path string) (*missingSecretTemplateOutput, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("--missing-template requires a nonempty file path")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve template path: %w", err)
	}
	parent, err := inspectArtifactOutputDirectory(filepath.Dir(absolute))
	if err != nil {
		return nil, fmt.Errorf("inspect template parent: %w", err)
	}
	name := filepath.Base(absolute)
	if parent.root == nil {
		parent.Close()
		return nil, errors.New("template parent must be an existing real directory")
	}
	if _, err := parent.root.Lstat(name); err == nil || !errors.Is(err, os.ErrNotExist) {
		parent.Close()
		return nil, errors.New("template destination must be a new file; existing files, directories, and symlinks are refused")
	}
	return &missingSecretTemplateOutput{path: absolute, name: name, directory: parent}, nil
}

func writeMissingSecretTemplate(ctx context.Context, output *missingSecretTemplateOutput, report secretConfigurationReport) (*missingSecretTemplateReceipt, error) {
	receipt := &missingSecretTemplateReceipt{Path: output.path, Status: "not_created_no_missing_keys", Keys: []string{}}
	for _, row := range report.Keys {
		if row.Required && !row.Present {
			receipt.Keys = append(receipt.Keys, row.Key)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("template export stopped: %w", err)
	}
	if len(receipt.Keys) == 0 {
		return receipt, nil
	}
	var content strings.Builder
	content.WriteString("# Missing required secret keys only; no existing values were exported.\n# Fill every value before uploading. Empty assignments are placeholders.\n")
	for _, key := range receipt.Keys {
		content.WriteString(key + "=\n")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("prepare temporary template: %w", err)
	}
	temporary := ".gregale-missing-secrets-" + hex.EncodeToString(nonce[:]) + ".tmp"
	root := output.directory.root
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("create temporary template: %w", err)
	}
	defer func() { _ = file.Close(); _ = root.Remove(temporary) }()
	if _, err := file.WriteString(content.String()); err != nil {
		return nil, fmt.Errorf("write template: %w", err)
	}
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("sync template: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close template: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("template export stopped: %w", err)
	}
	if err := root.Link(temporary, output.name); err != nil {
		return nil, fmt.Errorf("publish template without overwrite: %w", err)
	}
	receipt.Status = "created"
	receipt.PreviewCommand = recoveryCLI() + " env push --app " + recoveryArgument(report.App) + " --scope " + recoveryArgument(report.Scope) + " -f " + recoveryArgument(output.path) + " --dry-run"
	return receipt, nil
}

func printMissingSecretTemplate(receipt *missingSecretTemplateReceipt) {
	if receipt.Status != "created" {
		_, _ = fmt.Fprintln(osStdout, "No missing required keys; no template file was created.")
		return
	}
	_, _ = fmt.Fprintf(osStdout, "Missing-key template: %s (%d keys; mode 0600)\nFill every value locally before uploading. Preview the filled file (POSIX shells):\n  %s\n", oneLine(receipt.Path), len(receipt.Keys), receipt.PreviewCommand)
}
