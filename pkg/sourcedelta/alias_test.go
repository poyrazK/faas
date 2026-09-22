package sourcedelta

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// ADR-154: source deltas are only a transport optimization: rejecting an invalid
// output must not destroy the complete source archive or its delta input.
func TestOutputAliasDoesNotDestroyInput(t *testing.T) {
	for _, operation := range []string{"create", "apply base", "apply delta"} {
		for _, alias := range []string{"same descriptor", "same path", "hard link"} {
			t.Run(operation+"/"+alias, func(t *testing.T) {
				dir := t.TempDir()
				basePath := filepath.Join(dir, "base.gz")
				targetPath := filepath.Join(dir, "target.gz")
				deltaPath := filepath.Join(dir, "delta.gz")
				writeTestArchive(t, basePath, map[string]string{"same": "same", "changed": "old"})
				writeTestArchive(t, targetPath, map[string]string{"same": "same", "changed": "new"})
				openWritable := func(path string) *os.File {
					t.Helper()
					f, err := os.OpenFile(path, os.O_RDWR, 0)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = f.Close() })
					return f
				}
				base, target := openWritable(basePath), openWritable(targetPath)
				delta := openTestArchive(t, deltaPath, true)
				limits := Limits{MaxEntries: 10, MaxExpandedBytes: 1024}
				baseManifest, err := Inspect(base, limits)
				if err != nil {
					t.Fatal(err)
				}
				created, err := Create(baseManifest, target, delta, limits)
				if err != nil {
					t.Fatal(err)
				}
				input := target
				if operation == "apply base" {
					input = base
				} else if operation == "apply delta" {
					input = delta
				}
				before, err := os.ReadFile(input.Name())
				if err != nil {
					t.Fatal(err)
				}
				output := input
				if alias == "same path" {
					output = openWritable(input.Name())
				} else if alias == "hard link" {
					link := filepath.Join(dir, "alias.gz")
					if err := os.Link(input.Name(), link); err != nil {
						t.Fatal(err)
					}
					output = openWritable(link)
				}
				if operation == "create" {
					_, err = Create(baseManifest, target, output, limits)
				} else {
					_, err = Apply(base, delta, output, baseManifest.Revision, created.Target.Revision, created.Deleted, limits)
				}
				if err == nil {
					t.Error("accepted an output alias of an input")
				}
				after, err := os.ReadFile(input.Name())
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("overwrote the input archive")
				}
			})
		}
	}
}
