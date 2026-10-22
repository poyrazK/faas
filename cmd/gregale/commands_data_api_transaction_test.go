package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

func TestDataAPITransactionPackageRequiresReviewAndPreservesExistingFiles(t *testing.T) {
	output := filepath.Join(t.TempDir(), "package")
	if err := main.BuildDataAPITransaction("place_order", output); err != nil {
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
	if err := main.BuildDataAPITransaction("other", output); err == nil {
		t.Fatal("existing output replaced")
	}
	after, _ := os.ReadFile(filepath.Join(output, "migration.sql"))
	if string(before) != string(after) {
		t.Fatal("existing migration changed")
	}
	for _, name := range []string{"", "pg_unsafe", "gregale_unsafe", "bad;DROP", "Upper", "../bad", strings.Repeat("a", 33)} {
		if err := main.BuildDataAPITransaction(name, filepath.Join(t.TempDir(), "new")); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	for _, args := range [][]string{nil, {"apply"}, {"init"}, {"init", "--name", "place_order"}, {"init", "--name", "bad;DROP", "--output", output}} {
		if main.CmdDataAPITransaction(args) == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestDataAPITransactionContracts(t *testing.T) {
	for _, contract := range []string{"v1", "v2"} {
		t.Run(contract, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "package")
			if err := main.BuildDataAPITransactionContract("place_order", output, contract); err != nil {
				t.Fatal(err)
			}
			source := "data-api/transactions/"
			files := []string{"migration.sql", "client.ts", "README.md", "fixture.sql", "access-checks.json", "data-checks.json"}
			if contract == "v2" {
				source += "v2/"
				files = append(files, "cleanup-receipts.mjs", "receipt-diagnostics.mjs", "receipt-diagnostics-core.mjs", "package.json")
			}
			for _, file := range files {
				raw, err := os.ReadFile(filepath.Join(output, file))
				if err != nil {
					t.Fatal(err)
				}
				path := source + file
				if file == "receipt-diagnostics-core.mjs" {
					path = "data-api-starter/migrations/" + file
				}
				original, err := templates.FS.ReadFile(path)
				if err != nil || string(raw) != strings.ReplaceAll(string(original), "__OP__", "place_order") {
					t.Fatalf("generated file differs from reviewed %s contract: %s", contract, file)
				}
				if strings.Contains(string(raw), "__OP__") || (strings.HasSuffix(file, ".json") && !json.Valid(raw)) {
					t.Fatalf("unresolved placeholder or invalid JSON: %s", file)
				}
			}
			if err := main.BuildDataAPITransactionContract("other", output, contract); err == nil {
				t.Fatal("existing output replaced")
			}
		})
	}
	output := filepath.Join(t.TempDir(), "invalid")
	if err := main.BuildDataAPITransactionContract("place_order", output, "v3"); err == nil {
		t.Fatal("unknown contract accepted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("invalid contract created output")
	}
}

func TestDataAPITransactionV2CommandReport(t *testing.T) {
	main.ResetJSONOut(t)
	(*main.JsonOutput) = true
	var output bytes.Buffer
	previous := (*main.OsStdout)
	(*main.OsStdout) = &output
	t.Cleanup(func() { (*main.OsStdout) = previous })
	dir := filepath.Join(t.TempDir(), "package")
	if main.CmdDataAPITransaction([]string{"init", "--name", "place_order", "--output", dir, "--contract", "v2"}) != 0 {
		t.Fatal("v2 generation failed")
	}
	var report map[string]any
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report["contract"] != "v2" || report["status"] != "needs_review" || report["function"] != "api.place_order_v2" || report["receipt_function"] != "api.read_place_order_receipt_v2" {
		t.Fatalf("incorrect v2 report: %v", report)
	}
}
