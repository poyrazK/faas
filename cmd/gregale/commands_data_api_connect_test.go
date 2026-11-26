package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataAPIConnectPreparesChecksOfflineWithoutOverwriting(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "matrix.json")
	output := filepath.Join(dir, "checks.json")
	data := `{"version":1,"status":"reviewed","relation":"notes","key_column":"id","identities":["alice","bob"],"fixtures":[{"name":"alice_note","key":101,"visible_to":["alice"]},{"name":"bob_note","key":202,"visible_to":["bob"]}]}`
	if err := os.WriteFile(spec, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	_, stderr, restore := swapIO(t)
	defer restore()
	args := []string{"notes-data", "--prepare-checks", spec, "--collection", output}
	if code := cmdDataAPIConnect(args); code != 0 {
		t.Fatalf("prepare returned %d: %s", code, stderr())
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Version  int               `json:"version"`
		Requests []json.RawMessage `json:"requests"`
	}
	if json.Unmarshal(content, &result) != nil || result.Version != 1 || len(result.Requests) != 4 {
		t.Fatalf("invalid generated collection: %s", content)
	}
	info, err := os.Stat(output)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("collection permissions: %v %v", info, err)
	}
	if code := cmdDataAPIConnect(args); code == 0 {
		t.Fatal("overwrote existing collection")
	}
	unchanged, _ := os.ReadFile(output)
	if string(unchanged) != string(content) {
		t.Fatal("existing collection changed")
	}
	if code := cmdDataAPIConnect(append(args, "--apply")); code == 0 {
		t.Fatal("accepted conflicting apply option")
	}
	if err := os.WriteFile(spec, []byte(strings.Replace(data, `"status":"reviewed"`, `"status":"draft"`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	draftOutput := filepath.Join(dir, "draft.json")
	if code := cmdDataAPIConnect([]string{"notes-data", "--prepare-checks", spec, "--collection", draftOutput}); code == 0 {
		t.Fatal("accepted unreviewed matrix")
	}
	if _, err := os.Lstat(draftOutput); !os.IsNotExist(err) {
		t.Fatalf("unreviewed matrix created an output: %v", err)
	}
}

func TestDataAPIConnectTypesDoNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.types.ts")
	if err := writeDataAPIConnectTypes(path, "original"); err != nil {
		t.Fatal(err)
	}
	if err := writeDataAPIConnectTypes(path, "replacement"); err == nil {
		t.Fatal("overwrote an existing file")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatalf("file = %q, error = %v", data, err)
	}
}

func TestDataAPIConnectRejectsWritesBeforeCreatingAnything(t *testing.T) {
	root := t.TempDir()
	collection, identities := dataAPIHostedReplayFixture(t, root)
	if err := os.WriteFile(collection, []byte(`{"version":1,"requests":[{"name":"alice","method":"GET","path":"/rest/v1/notes","identity":"alice","expect":{"status":200,"json":[]}},{"name":"bob","method":"POST","path":"/rest/v1/notes","identity":"bob","body":{},"expect":{"status":200,"json":[]}}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_API", "https://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "owner-test-token")
	t.Setenv("GREGALE_DATA_API_REFRESH_TOKEN", "")
	_, stderr, restore := swapIO(t)
	defer restore()
	args := []string{"notes", "--database", "notes-db", "--inspect-app", "worker", "--apply", "--issuer", "https://issuer.example", "--jwks-url", "https://issuer.example/jwks", "--audience", "notes", "--output", filepath.Join(root, "types.ts"), "--collection", collection, "--identities", identities}
	if code := cmdDataAPIConnect(args); code == 0 {
		t.Fatal("write scenario accepted")
	}
	if !strings.Contains(stderr(), "Invalid hosted replay") {
		t.Fatalf("expected replay preflight failure, got %s", stderr())
	}
	if _, err := os.Stat(filepath.Join(root, "types.ts")); !os.IsNotExist(err) {
		t.Fatal("invalid collection wrote types")
	}
}

func TestDataAPIConnectInspectionIsBoundedAndReadOnly(t *testing.T) {
	for _, part := range []string{"BEGIN READ ONLY", "statement_timeout", "n.nspname='public'", "LIMIT 1000", "relrowsecurity", "has_table_privilege"} {
		if !strings.Contains(dataAPIConnectInspectionSQL, part) {
			t.Fatalf("inspection missing %s", part)
		}
	}
}

func TestDataAPIConnectVerifyRejectsConflictingModes(t *testing.T) {
	_, _, restore := swapIO(t)
	defer restore()
	for _, args := range [][]string{
		{"notes", "--verify", "--apply"},
		{"notes", "--verify", "--status"},
		{"notes", "--verify", "--resume"},
		{"notes", "--verify", "--database", "db"},
	} {
		if code := cmdDataAPIConnect(args); code == 0 {
			t.Fatalf("accepted conflicting options: %v", args)
		}
	}
}
