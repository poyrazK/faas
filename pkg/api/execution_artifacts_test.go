package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestExecutionOutputFilesAdmission(t *testing.T) {
	for _, names := range [][]string{{"../secret"}, {"/secret"}, {"a/../b"}, {"a//b"}, {"a\\b"}, {"a", "a"}, {"C:secret"}, {strings.Repeat("a", ExecutionArtifactMaxPathBytes+1)}, {"a\n"}, make([]string, ExecutionArtifactMaxFiles+1)} {
		request := CreateExecutionRequest{Runtime: ExecutionRuntimeNode22, Source: "export default () => 42", OutputFiles: names}
		if _, problem := request.Resolve(PlanPro); problem == nil {
			t.Fatalf("admitted invalid output files %q", names)
		}
	}
	request := CreateExecutionRequest{Runtime: ExecutionRuntimeNode22, Source: "export default () => 42", OutputFiles: []string{"reports/data.csv", "patch.diff"}}
	resolved, problem := request.Resolve(PlanPro)
	if problem != nil {
		t.Fatal(problem)
	}
	request.OutputFiles[0] = "changed"
	if resolved.OutputFiles[0] != "reports/data.csv" {
		t.Fatal("resolved paths alias caller memory")
	}
}

func TestExecutionArtifactEncodedBudgetAndIntegrity(t *testing.T) {
	content := []byte{0, 1, 255}
	hash := sha256.Sum256(content)
	artifacts := []ExecutionArtifact{{Name: "a.bin", SizeBytes: len(content), SHA256: "sha256:" + hex.EncodeToString(hash[:]), Content: content}}
	if err := ValidateExecutionArtifacts(artifacts); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if ExecutionArtifactsOutputBytes(artifacts) != len(encoded) || len(encoded) <= len(content) {
		t.Fatal("encoded contents and metadata not charged")
	}
	copy := CloneExecutionArtifacts(artifacts)
	copy[0].Content[0]++
	if err := ValidateExecutionArtifacts(copy); err == nil {
		t.Fatal("accepted corrupt bytes")
	}
	if err := ValidateExecutionArtifacts(artifacts); err != nil {
		t.Fatal("copy mutated original")
	}
	if ExecutionArtifactsOutputBytes(nil) != 0 {
		t.Fatal("legacy runs charged for empty artifacts")
	}
}
