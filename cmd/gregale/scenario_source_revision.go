package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type testSourceRevision struct {
	SchemaVersion int                             `json:"schema_version"`
	ExpectedWorkloads []string                   `json:"expected_workloads"`
	Workloads map[string]testWorkloadSourceRevision `json:"workloads"`
}

type testWorkloadSourceRevision struct {
	GitCommit      string `json:"git_commit,omitempty"`
	TrackedChanges *bool `json:"tracked_changes"`
	SourceSHA256   string `json:"source_sha256"`
	DeploymentID   string `json:"deployment_id,omitempty"`
	Runtime        string `json:"runtime"`
	Shape          string `json:"shape"`
	Handler        string `json:"handler,omitempty"`
}

func testWorkloadGitRevision(parent context.Context, dir string) (string, *bool) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	args := []string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-C", dir}
	command := exec.CommandContext(ctx, "git", append(args, "rev-parse", "--verify", "HEAD")...)
	body, err := command.Output()
	if err != nil {
		return "", nil
	}
	commit := strings.TrimSpace(string(body))
	decoded, err := hex.DecodeString(commit)
	if err != nil || (len(decoded) != 20 && len(decoded) != 32) {
		return "", nil
	}
	command = exec.CommandContext(ctx, "git", append(args, "diff", "--quiet", "--no-ext-diff", "--no-textconv", "HEAD", "--")...)
	err = command.Run()
	if err == nil {
		dirty := false
		return commit, &dirty
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		dirty := true
		return commit, &dirty
	}
	return commit, nil
}

func testSourceRevisionIdentity(revision *testSourceRevision) (string, error) {
	if revision == nil || revision.SchemaVersion != 1 || len(revision.ExpectedWorkloads) == 0 ||
		len(revision.ExpectedWorkloads) != len(revision.Workloads) {
		return "", fmt.Errorf("complete versioned source metadata is unavailable")
	}
	canonical := *revision
	canonical.ExpectedWorkloads = append([]string{}, revision.ExpectedWorkloads...)
	sort.Strings(canonical.ExpectedWorkloads)
	canonical.Workloads = make(map[string]testWorkloadSourceRevision)
	seen := make(map[string]bool)
	for _, name := range canonical.ExpectedWorkloads {
		workload, exists := revision.Workloads[name]
		if name == "" || !exists || seen[name] {
			return "", fmt.Errorf("source metadata has missing or duplicate workloads")
		}
		seen[name] = true
		digest, err := hex.DecodeString(workload.SourceSHA256)
		if err != nil || len(digest) != 32 || workload.Shape == "" {
			return "", fmt.Errorf("workload %q lacks an uploaded source digest or build shape", name)
		}
		workload.SourceSHA256 = strings.ToLower(workload.SourceSHA256)
		if workload.GitCommit != "" {
			commit, err := hex.DecodeString(workload.GitCommit)
			if err != nil || (len(commit) != 20 && len(commit) != 32) {
				return "", fmt.Errorf("workload %q has an invalid Git revision", name)
			}
			workload.GitCommit = strings.ToLower(workload.GitCommit)
		}
		// Deployment IDs identify individual builds, not equivalent source inputs.
		workload.DeploymentID = ""
		canonical.Workloads[name] = workload
	}
	body, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

type testReportSourceIdentity struct {
	Scenario string `json:"scenario"`
	Engine string `json:"engine"`
	IdentitySHA256 string `json:"identity_sha256"`
	Revision *testSourceRevision `json:"revision"`
	Runs int `json:"executed_runs"`
	ReceiptIndexes []int `json:"supporting_receipt_indexes"`
}

type testReportSourceSummary struct {
	Identities []testReportSourceIdentity `json:"identities"`
	UnknownRuns int `json:"unknown_source_runs"`
	MergeOverride bool `json:"merge_revision_override"`
}

type testReportSourceContext struct {
	Scenario string
	Engine string
}

func summarizeTestReportSources(receipts []testRunReceipt) testReportSourceSummary {
	summary := testReportSourceSummary{Identities: make([]testReportSourceIdentity, 0)}
	type key struct {
		Context testReportSourceContext
		Hash string
	}
	indexes := make(map[key]int)
	for root, receipt := range receipts {
		summary.MergeOverride = summary.MergeOverride || receipt.ReportMergeRevisionOverride
		var leaves []testRunReceipt
		collectTestFailureLeaves(receipt, "", &leaves)
		for _, leaf := range leaves {
			if leaf.Status != "passed" && leaf.Status != "failed" {
				continue
			}
			summary.MergeOverride = summary.MergeOverride || leaf.ReportMergeRevisionOverride
			hash, err := testSourceRevisionIdentity(leaf.SourceRevision)
			if err != nil {
				summary.UnknownRuns++
				continue
			}
			k := key{Context: testReportSourceContext{leaf.Scenario, leaf.Engine}, Hash: hash}
			index, exists := indexes[k]
			if !exists {
				index = len(summary.Identities)
				indexes[k] = index
				summary.Identities = append(summary.Identities, testReportSourceIdentity{
					Scenario: leaf.Scenario, Engine: leaf.Engine, IdentitySHA256: hash, Revision: leaf.SourceRevision,
					ReceiptIndexes: make([]int, 0),
				})
			}
			row := &summary.Identities[index]
			row.Runs++
			if len(row.ReceiptIndexes) == 0 || row.ReceiptIndexes[len(row.ReceiptIndexes)-1] != root {
				row.ReceiptIndexes = append(row.ReceiptIndexes, root)
			}
		}
	}
	sort.Slice(summary.Identities, func(i, j int) bool {
		a, b := summary.Identities[i], summary.Identities[j]
		if a.Scenario != b.Scenario {
			return a.Scenario < b.Scenario
		}
		if a.Engine != b.Engine {
			return a.Engine < b.Engine
		}
		return a.IdentitySHA256 < b.IdentitySHA256
	})
	return summary
}
