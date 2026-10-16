package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataAPITransactionPackageRequiresReviewAndPreservesExistingFiles(t *testing.T) {
	output := filepath.Join(t.TempDir(), "package")
	if err := buildDataAPITransaction("place_order", output); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"migration.sql", "fixture.sql", "client.ts", "README.md", "access-checks.json", "data-checks.json"} {
		raw, err := os.ReadFile(filepath.Join(output, file))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "__OP__") {
			t.Fatal("unresolved name placeholder")
		}
		if strings.HasSuffix(file, ".json") && !json.Valid(raw) {
			t.Fatal("invalid generated JSON")
		}
	}
	before, _ := os.ReadFile(filepath.Join(output, "migration.sql"))
	if err := buildDataAPITransaction("other", output); err == nil {
		t.Fatal("existing output replaced")
	}
	after, _ := os.ReadFile(filepath.Join(output, "migration.sql"))
	if string(before) != string(after) {
		t.Fatal("existing migration changed")
	}
	for _, name := range []string{"", "pg_unsafe", "gregale_unsafe", "bad;DROP", "Upper", "../bad", strings.Repeat("a", 33)} {
		if err := buildDataAPITransaction(name, filepath.Join(t.TempDir(), "new")); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	for _, args := range [][]string{nil, {"apply"}, {"init"}, {"init", "--name", "place_order"}, {"init", "--name", "bad;DROP", "--output", output}} {
		if cmdDataAPITransaction(args) == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
}
