package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

type preparedListOutput struct {
	path      string
	directory *artifactOutputDirectory
	file      *listOutputFile
}

func prepareListOutput(path string, selected, all bool, command string) (*preparedListOutput, error) {
	if !selected {
		return nil, nil
	}
	if violation := listFlagRuleViolation(listOutputRequiresAll, listFlagRuleState{Output: selected, All: all}, command); violation != nil {
		return nil, violation
	}
	if strings.TrimSpace(path) == "" || path == "-" {
		return nil, errors.New("--output requires a nonempty file path; omit it to write to stdout")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve list output path: %w", err)
	}
	directory, err := inspectArtifactOutputDirectory(filepath.Dir(absolute))
	if err != nil {
		return nil, fmt.Errorf("inspect list output parent: %w", err)
	}
	if directory.root == nil {
		directory.Close()
		return nil, errors.New("list output parent must be an existing real directory")
	}
	file, err := newListOutputFile(directory.root, filepath.Base(absolute))
	if err != nil {
		directory.Close()
		return nil, err
	}
	return &preparedListOutput{path: absolute, directory: directory, file: file}, nil
}

func (o *preparedListOutput) Close() {
	_ = o.file.Close()
	o.directory.Close()
}

func (o *preparedListOutput) validateParent() error {
	named, err := os.Stat(filepath.Dir(o.path))
	if err != nil {
		return fmt.Errorf("recheck list output parent: %w", err)
	}
	pinned, err := o.directory.root.Stat(".")
	if err != nil {
		return fmt.Errorf("recheck pinned output directory: %w", err)
	}
	if !os.SameFile(named, pinned) {
		return errors.New("list output parent changed during export")
	}
	return nil
}

func runListFileExport[T any](parent context.Context, title, cursor string, output *preparedListOutput, mode listExportProgressMode, fetch func(context.Context, string) ([]T, string, error)) int {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	reporter := newListExportProgress(osStderr, mode, time.Now)
	var observe func(listStreamProgress)
	if reporter != nil {
		observe = func(p listStreamProgress) { reporter.observe(p, output.file.bytes) }
	}
	progress, err := streamListPagesObserved(ctx, output.file, cursor, fetch, observe)
	err = listReadError(ctx, err)
	if err == nil {
		err = output.validateParent()
	}
	if err != nil {
		reporter.finish(false, progress, output.file.bytes)
		// Private file bytes are not partial stdout. Preserve the underlying
		// API/cancellation classification without stream pagination metadata.
		return printErr(title, err)
	}
	receipt, err := output.file.Publish(ctx, progress)
	if err != nil {
		reporter.finish(false, progress, output.file.bytes)
		return printErr(title, err)
	}
	reporter.finish(true, progress, receipt.BytesWritten)
	return renderListExportReceipt(receipt, output.path)
}

func renderListExportReceipt(receipt *listOutputReceipt, outputPath string) int {
	redact := errorTextRedactor()
	if utf8.ValidString(outputPath) && redact(outputPath) == outputPath {
		receipt.Path = outputPath
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	path := "the requested file"
	if receipt.Path != "" {
		path = fmt.Sprintf("%q", receipt.Path)
	}
	_, err = fmt.Fprintf(osStdout, "Exported %d items from %d pages to %s (%d bytes; SHA-256 %s)\n",
		receipt.ItemsEmitted, receipt.PagesCompleted, path, receipt.BytesWritten, receipt.SHA256)
	if err != nil {
		return printErr("Could not write export receipt", err)
	}
	return 0
}
