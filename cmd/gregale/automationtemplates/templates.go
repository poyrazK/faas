// Package automationtemplates embeds automation definitions and their samples.
package automationtemplates

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed webhook-enrichment scheduled-report contact-sync approval-flow
var assets embed.FS

var Names = []string{"webhook-enrichment", "scheduled-report", "contact-sync", "approval-flow"}

// Materialize writes a starter into a new directory, refusing existing paths.
func Materialize(name, destination string) (files []string, err error) {
	known := false
	for _, candidate := range Names {
		known = known || name == candidate
	}
	if !known {
		return nil, fmt.Errorf("unknown automation template %q", name)
	}
	source, err := fs.Sub(assets, name)
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(source, ".")
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return nil, fmt.Errorf("create a new destination directory: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(destination)
		}
	}()
	for _, entry := range entries {
		data, readErr := fs.ReadFile(source, entry.Name())
		if readErr != nil {
			return nil, readErr
		}
		file, writeErr := os.OpenFile(filepath.Join(destination, entry.Name()), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if writeErr != nil {
			return nil, writeErr
		}
		_, writeErr = file.Write(data)
		closeErr := file.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		files = append(files, entry.Name())
	}
	return files, nil
}
