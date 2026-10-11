package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataAPIDevRejectsInvalidInputsBeforeStartingServices(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "migrations", "sql"), 0o755); err != nil {
		t.Fatal(err)
	}
	directoryOutput := filepath.Join(project, "directory-output")
	if err := os.Mkdir(directoryOutput, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--directory", t.TempDir()},
		{"--directory", project, "--port", "-1"},
		{"--directory", project, "--port", "65536"},
		{"--directory", project, "--output", ""},
		{"--directory", project, "--output", directoryOutput},
		{"--directory", project, "unexpected"},
		{"--directory", project, "--scenario", "notes-rls"},
		{"--directory", project, "--replay", "missing.json"},
		{"--directory", project, "--once", "--replay", "missing.json"},
		{"--directory", project, "--check-breaking"},
		{"--directory", project, "--check-types"},
		{"--directory", project, "--baseline", "missing.json"},
		{"--directory", project, "--approval", "approval.json"},
		{"--directory", project, "--baseline", "schema.json", "--approval", "approval.json"},
		{"--directory", project, "--output", ".gregale/data-api-contract.json"},
	} {
		if code := cmdDataAPIDev(args); code == 0 {
			t.Fatalf("invalid args accepted: %v", args)
		}
	}
}

func TestDataAPIDevCannotReplaceApprovalThroughOutputAlias(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "migrations", "sql"), 0o755); err != nil {
		t.Fatal(err)
	}
	baseline := filepath.Join(project, "schema.json")
	approval := filepath.Join(project, "approval.json")
	content := dataAPIContractFixture(t, dataAPIDiffFixture())
	if err := os.WriteFile(baseline, content, 0o600); err != nil {
		t.Fatal(err)
	}
	writeDataAPIApprovalFixture(t, approval, content, content, []dataAPIApprovedChange{{"api.notes", "relation removed; update queries and generated types", "Retired."}})
	original, err := os.ReadFile(approval)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(project, "approval-alias.json")
	if err := os.Link(approval, alias); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{approval, alias} {
		if code := cmdDataAPIDev([]string{"--directory", project, "--baseline", "schema.json", "--check-breaking", "--approval", "approval.json", "--output", output}); code == 0 {
			t.Fatal("accepted approval as types output")
		}
	}
	actual, err := os.ReadFile(approval)
	if err != nil || string(actual) != string(original) {
		t.Fatal("approval changed during preflight")
	}
}

func TestDataAPIDevCannotReplaceTheBaselineThroughAnOutputAlias(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "migrations", "sql"), 0o755); err != nil {
		t.Fatal(err)
	}
	baseline := filepath.Join(project, "schema.json")
	content := dataAPIContractFixture(t, dataAPIDiffFixture())
	if err := os.WriteFile(baseline, content, 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(project, "schema-alias.json")
	if err := os.Link(baseline, alias); err != nil {
		t.Fatal(err)
	}
	if code := cmdDataAPIDev([]string{"--directory", project, "--baseline", baseline, "--output", alias}); code == 0 {
		t.Fatal("accepted baseline as types output")
	}
	actual, err := os.ReadFile(baseline)
	if err != nil || string(actual) != string(content) {
		t.Fatal("baseline changed during preflight")
	}
}

func TestDataAPIDevCannotReplaceReplayCollectionThroughOutputAlias(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "migrations", "sql"), 0o755); err != nil {
		t.Fatal(err)
	}
	collection := filepath.Join(project, "data-api.requests.json")
	content := []byte(`{"version":1,"requests":[]}`)
	if err := os.WriteFile(collection, content, 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(project, "alias.json")
	if err := os.Link(collection, alias); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{collection, alias} {
		if code := cmdDataAPIDev([]string{"--directory", project, "--once", "--replay", "data-api.requests.json", "--output", output}); code == 0 {
			t.Fatal("accepted replay collection as types output")
		}
	}
	actual, err := os.ReadFile(collection)
	if err != nil || string(actual) != string(content) {
		t.Fatal("replay collection changed during preflight")
	}
}

func TestDataAPIDevAcceptedContractsPreflight(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "migrations", "sql"), 0755); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(project, "accepted.json")
	for _, content := range []string{"{}", `{"version":1,"current_fingerprint":"bad","accepted_fingerprints":[]}`} {
		if err := os.WriteFile(policy, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if cmdDataAPIDev([]string{"--directory", project, "--accepted-contracts", "accepted.json"}) == 0 {
			t.Fatal("invalid policy accepted")
		}
	}
	valid := `{"version":1,"current_fingerprint":"` + strings.Repeat("a", 64) + `","accepted_fingerprints":["` + strings.Repeat("b", 64) + `"]}`
	if err := os.WriteFile(policy, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(project, "alias.json")
	if err := os.Link(policy, alias); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{"accepted.json", "alias.json"} {
		if cmdDataAPIDev([]string{"--directory", project, "--accepted-contracts", "accepted.json", "--output", output}) == 0 {
			t.Fatal("policy output alias accepted")
		}
	}
	actual, _ := os.ReadFile(policy)
	if string(actual) != valid {
		t.Fatal("policy overwritten")
	}
}
