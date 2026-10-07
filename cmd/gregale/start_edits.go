package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// startFileEdit keeps the exact reviewed file in memory. Neither file bytes
// nor snippets are ever written to the session record.
type startFileEdit struct {
	path, beforeLabel, afterLabel string
	before, after                 []byte
	mode                          os.FileMode
}

func readStartEditableFile(root, path string) ([]byte, os.FileMode, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, 0, fmt.Errorf("resolve source directory: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, 0, fmt.Errorf("resolve source file: %w", err)
	}
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, 0, errors.New("refusing to edit a file outside the source directory")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, 0, errors.New("guided edits require a regular source file")
	}
	f, err := openCustomerFile(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()
	info, err = f.Stat()
	if err != nil {
		return nil, 0, fmt.Errorf("inspect source file: %w", err)
	}
	if info.Size() > 1<<20 {
		return nil, 0, errors.New("source file is too large for a guided edit")
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, 0, fmt.Errorf("read source file: %w", err)
	}
	if len(data) > 1<<20 {
		return nil, 0, errors.New("source file grew beyond the guided edit limit")
	}
	return data, info.Mode().Perm(), nil
}

func applyStartFileEdit(root string, edit startFileEdit) error {
	current, _, err := readStartEditableFile(root, edit.path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, edit.before) {
		return errors.New("source changed after the preview; recheck it to review a fresh edit")
	}
	tmp, err := os.CreateTemp(filepath.Dir(edit.path), ".gregale-edit-*.tmp")
	if err != nil {
		return fmt.Errorf("create source edit: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	defer func() { _ = tmp.Close() }()
	if err := tmp.Chmod(edit.mode); err != nil {
		return fmt.Errorf("preserve source permissions: %w", err)
	}
	if _, err := tmp.Write(edit.after); err != nil {
		return fmt.Errorf("write source edit: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync source edit: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close source edit: %w", err)
	}
	if err := os.Rename(tmp.Name(), edit.path); err != nil {
		return fmt.Errorf("apply source edit: %w", err)
	}
	return nil
}

// This deliberately supports only simple Node listener calls. More complex
// expressions and Python socket binds remain manual fixes, with recheck in
// the same session. A broad replacement could change a database address.
var startNodeLoopback = regexp.MustCompile(`^[ \t]*app\.listen\s*\(\s*(?:[A-Za-z_$][A-Za-z0-9_$]*|[0-9]+)\s*,\s*['"]127\.0\.0\.1['"]`)

func startLoopbackEdits(root string, report doctorReport) []startFileEdit {
	var edits []startFileEdit
	seen := make(map[string]bool)
	for _, check := range report.Checks {
		if check.Name != "loopback-bind" || check.Status != "error" {
			continue
		}
		for _, source := range check.Sources {
			i := strings.LastIndexByte(source, ':')
			if i < 0 {
				continue
			}
			path := source[:i]
			if seen[path] || (filepath.Ext(path) != ".js" && filepath.Ext(path) != ".ts") {
				continue
			}
			seen[path] = true
			data, mode, err := readStartEditableFile(root, path)
			if err != nil {
				continue
			}
			lines := strings.Split(string(data), "\n")
			changed := false
			for n, line := range lines {
				if envRefCommentOnlyLine(line) {
					continue
				}
				lines[n] = startNodeLoopback.ReplaceAllStringFunc(line, func(match string) string {
					changed = true
					return strings.Replace(match, "127.0.0.1", "0.0.0.0", 1)
				})
			}
			if changed {
				edits = append(edits, startFileEdit{path: path, before: data, after: []byte(strings.Join(lines, "\n")), mode: mode, beforeLabel: "listener host 127.0.0.1", afterLabel: "listener host 0.0.0.0"})
			}
		}
	}
	return edits
}

func renderStartFileEdits(edits []startFileEdit) {
	_, _ = fmt.Fprintln(osStdout, "\nProposed local changes")
	for _, edit := range edits {
		_, _ = fmt.Fprintf(osStdout, "  %s\n    Before: %s\n    After:  %s\n", edit.path, edit.beforeLabel, edit.afterLabel)
	}
}

func (r *startRunner) reviewFileEdits(edits []startFileEdit) (bool, error) {
	renderStartFileEdits(edits)
	ok, err := r.prompt.confirm(r.ctx, "Apply these local changes")
	if err != nil || !ok {
		return false, err
	}
	// Validate every reviewed snapshot before writing any file.
	for _, edit := range edits {
		current, _, err := readStartEditableFile(r.session.SourcePath, edit.path)
		if err != nil {
			return false, err
		}
		if !bytes.Equal(current, edit.before) {
			return false, errors.New("source changed after the preview; recheck before applying the edit")
		}
	}
	for _, edit := range edits {
		if err := applyStartFileEdit(r.session.SourcePath, edit); err != nil {
			return false, err
		}
		PrintOK(osStdout, "Updated %s", edit.path)
	}
	return true, nil
}

func startGreetingEdit(root, template, greeting string) (startFileEdit, error) {
	filename, pattern := "", ""
	switch template {
	case "hello-node":
		filename, pattern = "handler.js", `\bmessage\s*:\s*("(?:[^"\\]|\\.)*")`
	case "hello-python":
		filename, pattern = "handler.py", `\bmessage\s*=\s*("(?:[^"\\]|\\.)*")`
	case "hello-go":
		filename, pattern = "main.go", `"message"\s*:\s*("(?:[^"\\]|\\.)*")`
	default:
		return startFileEdit{}, errors.New("greeting walkthrough is available for the built-in HTTP starters")
	}
	path := filepath.Join(root, filename)
	data, mode, err := readStartEditableFile(root, path)
	if err != nil {
		return startFileEdit{}, err
	}
	re := regexp.MustCompile(pattern)
	matches := re.FindAllSubmatchIndex(data, -1)
	if len(matches) != 1 {
		return startFileEdit{}, errors.New("the starter greeting has changed shape; edit it manually and use redeploy")
	}
	match := matches[0]
	// JSON string quoting also produces valid JavaScript, Python and Go
	// double-quoted literals, including quotes, newlines and Unicode.
	encoded, err := json.Marshal(greeting)
	if err != nil {
		return startFileEdit{}, fmt.Errorf("quote starter greeting: %w", err)
	}
	literal := string(encoded)
	after := append([]byte(nil), data[:match[2]]...)
	after = append(after, literal...)
	after = append(after, data[match[3]:]...)
	return startFileEdit{path: path, before: data, after: after, mode: mode, beforeLabel: startResponseText(string(data[match[2]:match[3]])), afterLabel: literal}, nil
}
