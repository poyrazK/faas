package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomerOperationBindingsGenerationAndDrift(t *testing.T) {
	dir := t.TempDir()
	example := filepath.Join("..", "..", "examples", "customer-operation-orders")
	manifest, err := os.ReadFile(filepath.Join(example, "gregale.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(dir, "schemas"), os.DirFS(filepath.Join(example, "schemas"))); err != nil {
		t.Fatal(err)
	}
	for language, extension := range map[string]string{"typescript": "ts", "javascript": "mjs", "python": "py", "go": "go"} {
		t.Run(language, func(t *testing.T) {
			c, err := parseCustomerOperationBindings([]string{"--app", "orders", "--plan", "pro", "--dir", dir, "--language", language, "--output", filepath.Join(t.TempDir(), "bindings."+extension)})
			if err != nil {
				t.Fatal(err)
			}
			c.check = true
			if err := runCustomerOperationBindings(c, io.Discard); err == nil {
				t.Fatal("check accepted missing bindings")
			}
			c.check = false
			if err := runCustomerOperationBindings(c, io.Discard); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(c.output)
			if err != nil {
				t.Fatal(err)
			}
			c.check = true
			if err := runCustomerOperationBindings(c, io.Discard); err != nil {
				t.Fatal(err)
			}
			updated := bytes.Replace(manifest, []byte("\n    version: 1\n"), []byte("\n    version: 2\n"), 1)
			if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), updated, 0600); err != nil {
				t.Fatal(err)
			}
			if err := runCustomerOperationBindings(c, io.Discard); err == nil || !strings.Contains(err.Error(), "stale") {
				t.Fatalf("contract drift accepted: %v", err)
			}
			unchanged, err := os.ReadFile(c.output)
			if err != nil || !bytes.Equal(original, unchanged) {
				t.Fatal("--check changed the output")
			}
			c.check = false
			if err := runCustomerOperationBindings(c, io.Discard); err != nil {
				t.Fatal(err)
			}
			c.check = true
			if err := runCustomerOperationBindings(c, io.Discard); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), manifest, 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCustomerOperationBindingsProtectExistingSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.py")
	if err := os.WriteFile(path, []byte("print('business code')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeCustomerOperationBindings(path, []byte("# generated\n")); err == nil {
		t.Fatal("overwrote application source")
	}
	if _, err := parseCustomerOperationBindings([]string{"--language", "python"}); err == nil {
		t.Fatal("accepted missing app/plan/output")
	}
}
