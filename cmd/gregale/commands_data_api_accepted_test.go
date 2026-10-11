package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataAPIAcceptedContractRequiresZeroBreaks(t *testing.T) {
	before, after := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for _, change := range []dataAPIChange{{Breaking: true}, {Breaking: true, Approved: true}} {
		if _, err := dataAPIAcceptedContractPolicy(before, after, []dataAPIChange{change}); err == nil {
			t.Fatal("breaking contract accepted")
		}
	}
	if _, err := dataAPIAcceptedContractPolicy(before, before, nil); err == nil {
		t.Fatal("identical contract accepted")
	}
}

func TestDataAPIDiffWritesAcceptedContractAndPreservesInputs(t *testing.T) {
	resetJSONOut(t)
	var output bytes.Buffer
	previous := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = previous })
	root := t.TempDir()
	baseline := filepath.Join(root, "before.json")
	current := filepath.Join(root, "after.json")
	policy := filepath.Join(root, "accepted.json")
	before := dataAPIContractFixture(t, dataAPIDiffFixture())
	after := dataAPIDiffFixture()
	after.Tables[0].Columns = append(after.Tables[0].Columns, dataAPIColumn{Name: "label", Type: "string", Nullable: true, Insertable: true, Optional: true, Updatable: true})
	currentBytes := dataAPIContractFixture(t, after)
	for path, content := range map[string][]byte{baseline: before, current: currentBytes} {
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"diff", "--baseline", baseline, "--current", current, "--accept-contract", policy}
	if cmdDataAPI(args) != 0 {
		t.Fatal("compatible policy failed")
	}
	content, err := os.ReadFile(policy)
	if err != nil {
		t.Fatal(err)
	}
	var parsed dataAPIAcceptedContracts
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatal(err)
	}
	old, _ := parseDataAPIContract(before)
	newContract, _ := parseDataAPIContract(currentBytes)
	if parsed.Version != 1 || parsed.CurrentFingerprint != newContract.Fingerprint || len(parsed.AcceptedFingerprints) != 1 || parsed.AcceptedFingerprints[0] != old.Fingerprint {
		t.Fatalf("policy %s", content)
	}
	// A later breaking schema must leave a reviewed policy untouched.
	after.Tables[0].Columns[0].Type = "number"
	if err := os.WriteFile(current, dataAPIContractFixture(t, after), 0600); err != nil {
		t.Fatal(err)
	}
	if cmdDataAPI(args) == 0 {
		t.Fatal("breaking policy accepted")
	}
	preserved, _ := os.ReadFile(policy)
	if !bytes.Equal(content, preserved) {
		t.Fatal("failed check overwrote policy")
	}
	for _, input := range []string{baseline, current} {
		if cmdDataAPI([]string{"diff", "--baseline", baseline, "--current", current, "--accept-contract", input}) == 0 {
			t.Fatal("input overwritten")
		}
	}
	alias := filepath.Join(root, "alias.json")
	if err := os.Link(baseline, alias); err != nil {
		t.Fatal(err)
	}
	if cmdDataAPI([]string{"diff", "--baseline", baseline, "--current", current, "--accept-contract", alias}) == 0 {
		t.Fatal("hardlink input accepted")
	}
}
