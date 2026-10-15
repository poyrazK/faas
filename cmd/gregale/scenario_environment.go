package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

type testEnvironmentSnapshot struct {
	SchemaVersion int `json:"schema_version"`
	PlatformVersion *string `json:"platform_version"`
	Workloads map[string]testWorkloadEnvironment `json:"workloads"`
}

type testWorkloadEnvironment struct {
	Type string `json:"type"`
	Runtime string `json:"runtime"`
	RuntimeVersion *string `json:"runtime_version"`
	RAMMB int `json:"ram_mb"`
	VCPU int `json:"vcpu"`
	CPUMillicores int `json:"cpu_millicores"`
	MaxConcurrency int `json:"max_concurrency"`
	ConcurrencyPerVM int `json:"concurrency_per_vm"`
	IdleTimeoutSeconds int `json:"idle_timeout_seconds"`
	RequestTimeoutSeconds int `json:"request_timeout_seconds"`
	MinInstances int `json:"min_instances"`
	Postgres bool `json:"postgres"`
	Fixture string `json:"fixture,omitempty"`
	FailFirst int `json:"fail_first"`
	TCPPorts []int `json:"tcp_ports"`
}

func testEnvironmentFromApp(app api.AppResponse, spec testService) testWorkloadEnvironment {
	ports := append([]int{}, spec.TCPPorts...)
	sort.Ints(ports)
	return testWorkloadEnvironment{
		Type: app.Type, Runtime: app.Runtime, RAMMB: app.RAMMB, VCPU: app.VCPU, CPUMillicores: app.CPUMillicores,
		MaxConcurrency: app.MaxConcurrency, ConcurrencyPerVM: app.ConcurrencyPerVMBound,
		IdleTimeoutSeconds: app.IdleTimeoutS, RequestTimeoutSeconds: app.RequestTimeoutS, MinInstances: app.MinInstances,
		Postgres: spec.Postgres, Fixture: spec.Fixture, FailFirst: spec.FailFirst, TCPPorts: ports,
	}
}

type testEnvironmentVariant struct {
	SHA256 string `json:"sha256"`
	Snapshot *testEnvironmentSnapshot `json:"snapshot"`
	Runs int `json:"executed_runs"`
	ReceiptIndexes []int `json:"supporting_receipt_indexes"`
}

type testEnvironmentSide struct {
	ExecutedRuns int `json:"executed_runs"`
	UnknownRuns int `json:"unknown_snapshot_runs"`
	Variants []testEnvironmentVariant `json:"variants"`
}

type testEnvironmentDifference struct {
	Field string `json:"field"`
	Baseline []string `json:"baseline_values"`
	Current []string `json:"current_values"`
}

type testEnvironmentComparison struct {
	Configuration string `json:"configuration_sha256,omitempty"`
	Context testFailureGroupKey `json:"context"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	UnknownFields []string `json:"unknown_fields"`
	Differences []testEnvironmentDifference `json:"differences"`
	Baseline testEnvironmentSide `json:"baseline"`
	Current testEnvironmentSide `json:"current"`
}

func testEnvironmentFields(snapshot *testEnvironmentSnapshot) (map[string]string, []string, bool) {
	fields := make(map[string]string)
	unknown := make([]string, 0)
	if snapshot == nil || snapshot.SchemaVersion != 1 || len(snapshot.Workloads) == 0 {
		return fields, unknown, false
	}
	if snapshot.PlatformVersion == nil || *snapshot.PlatformVersion == "" {
		unknown = append(unknown, "platform_version")
	} else {
		value, _ := json.Marshal(snapshot.PlatformVersion)
		fields["platform_version"] = string(value)
	}
	for name, workload := range snapshot.Workloads {
		if workload.Type == "" || workload.RAMMB <= 0 || workload.VCPU <= 0 || workload.CPUMillicores <= 0 ||
			workload.MaxConcurrency <= 0 || workload.ConcurrencyPerVM <= 0 {
			unknown = append(unknown, "workloads."+name+".resources")
			return fields, unknown, false
		}
		if workload.RuntimeVersion == nil || *workload.RuntimeVersion == "" {
			unknown = append(unknown, "workloads."+name+".runtime_version")
		}
		body, _ := json.Marshal(workload)
		var values map[string]json.RawMessage
		_ = json.Unmarshal(body, &values)
		fields["workloads."+name+".present"] = "true"
		for field, value := range values {
			if field == "runtime_version" && (workload.RuntimeVersion == nil || *workload.RuntimeVersion == "") {
				continue
			}
			fields["workloads."+name+"."+field] = string(value)
		}
	}
	sort.Strings(unknown)
	return fields, unknown, true
}

func compareTestEnvironments(before, after []testRunReceipt) []testEnvironmentComparison {
	collect := func(receipts []testRunReceipt) map[testCampaignCoverageKey]testEnvironmentSide {
		rows := make(map[testCampaignCoverageKey]testEnvironmentSide)
		for root, receipt := range receipts {
			var leaves []testRunReceipt
			collectTestFailureLeaves(receipt, "", &leaves)
			for _, leaf := range leaves {
				if leaf.Status != "passed" && leaf.Status != "failed" {
					continue
				}
				key := testCampaignCoverageKey{Hash: testDigestScopeFingerprint([]testRunReceipt{leaf}), Scope: testFailureScope(leaf)}
				row := rows[key]
				if row.Variants == nil {
					row.Variants = make([]testEnvironmentVariant, 0)
				}
				row.ExecutedRuns++
				if _, _, valid := testEnvironmentFields(leaf.Environment); !valid {
					row.UnknownRuns++
					rows[key] = row
					continue
				}
				body, _ := json.Marshal(leaf.Environment)
				digest := sha256.Sum256(body)
				hash := hex.EncodeToString(digest[:])
				index := -1
				for i := range row.Variants {
					if row.Variants[i].SHA256 == hash {
						index = i
						break
					}
				}
				if index < 0 {
					index = len(row.Variants)
					row.Variants = append(row.Variants, testEnvironmentVariant{SHA256: hash, Snapshot: leaf.Environment, ReceiptIndexes: make([]int, 0)})
				}
				variant := &row.Variants[index]
				variant.Runs++
				if len(variant.ReceiptIndexes) == 0 || variant.ReceiptIndexes[len(variant.ReceiptIndexes)-1] != root {
					variant.ReceiptIndexes = append(variant.ReceiptIndexes, root)
				}
				rows[key] = row
			}
		}
		return rows
	}
	old, current := collect(before), collect(after)
	keys := make([]testCampaignCoverageKey, 0)
	seen := make(map[testCampaignCoverageKey]bool)
	for _, side := range []map[testCampaignCoverageKey]testEnvironmentSide{old, current} {
		for key := range side {
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, _ := json.Marshal([]string{keys[i].Hash, keys[i].Scope.scenario, keys[i].Scope.profile, keys[i].Scope.engine, keys[i].Scope.caseName})
		b, _ := json.Marshal([]string{keys[j].Hash, keys[j].Scope.scenario, keys[j].Scope.profile, keys[j].Scope.engine, keys[j].Scope.caseName})
		return string(a) < string(b)
	})
	results := make([]testEnvironmentComparison, 0, len(keys))
	for _, key := range keys {
		row := testEnvironmentComparison{Configuration: key.Hash,
			Context: testFailureGroupKey{Scenario: key.Scope.scenario, Profile: key.Scope.profile, Engine: key.Scope.engine, Case: key.Scope.caseName},
			Baseline: old[key], Current: current[key], UnknownFields: make([]string, 0), Differences: make([]testEnvironmentDifference, 0)}
		values := [2]map[string]map[string]bool{make(map[string]map[string]bool), make(map[string]map[string]bool)}
		unknown := make(map[string]bool)
		allFields := make(map[string]bool)
		for side, recorded := range []testEnvironmentSide{row.Baseline, row.Current} {
			for _, variant := range recorded.Variants {
				fields, absent, _ := testEnvironmentFields(variant.Snapshot)
				for _, field := range absent {
					unknown[field] = true
				}
				for field, value := range fields {
					allFields[field] = true
					if values[side][field] == nil {
						values[side][field] = make(map[string]bool)
					}
					values[side][field][value] = true
				}
			}
		}
		for field := range unknown {
			row.UnknownFields = append(row.UnknownFields, field)
		}
		sort.Strings(row.UnknownFields)
		for field := range allFields {
			if unknown[field] {
				continue
			}
			sets := [2][]string{make([]string, 0), make([]string, 0)}
			for side := range values {
				for value := range values[side][field] {
					sets[side] = append(sets[side], value)
				}
				sort.Strings(sets[side])
			}
			left, _ := json.Marshal(sets[0])
			right, _ := json.Marshal(sets[1])
			if string(left) != string(right) || len(sets[0]) > 1 || len(sets[1]) > 1 {
				row.Differences = append(row.Differences, testEnvironmentDifference{Field: field, Baseline: sets[0], Current: sets[1]})
			}
		}
		sort.Slice(row.Differences, func(i, j int) bool { return row.Differences[i].Field < row.Differences[j].Field })
		row.Status = "compatible"
		if len(row.Baseline.Variants) > 0 && len(row.Current.Variants) > 0 &&
			(len(row.Differences) > 0 || len(row.Baseline.Variants) > 1 || len(row.Current.Variants) > 1) {
			row.Status, row.Reason = "incompatible", "recorded environment fields differ or vary within a report"
		} else if row.Baseline.ExecutedRuns == 0 || row.Current.ExecutedRuns == 0 || row.Baseline.UnknownRuns > 0 || row.Current.UnknownRuns > 0 || key.Hash == "" {
			row.Status, row.Reason = "unknown", "matching configuration/context or complete environment snapshots are unavailable"
		} else if len(row.UnknownFields) > 0 {
			row.Status, row.Reason = "unknown", "recorded fields agree but version or resource metadata is incomplete"
		}
		results = append(results, row)
	}
	return results
}

func findTestEnvironmentComparison(rows []testEnvironmentComparison, key testFailureGroupKey, hash string) *testEnvironmentComparison {
	for i := range rows {
		r := &rows[i]
		if r.Configuration == hash && r.Context.Scenario == key.Scenario && r.Context.Profile == key.Profile &&
			r.Context.Engine == key.Engine && r.Context.Case == key.Case {
			return r
		}
	}
	return nil
}

type testCampaignEnvironmentPolicy struct {
	OnMismatch string `yaml:"on_mismatch" json:"on_mismatch"`
	OnUnknown string `yaml:"on_unknown" json:"on_unknown"`
}

func validateTestCampaignEnvironmentPolicy(policy *testCampaignEnvironmentPolicy) error {
	if policy == nil {
		return nil
	}
	for _, action := range []string{policy.OnMismatch, policy.OnUnknown} {
		if action != "fail" && action != "not_comparable" && action != "ignore" {
			return fmt.Errorf("environment actions must explicitly be fail, not_comparable or ignore")
		}
	}
	return nil
}

func testCampaignEnvironmentSettings(policy testCampaignPolicy) testCampaignEnvironmentPolicy {
	if policy.Environment == nil {
		return testCampaignEnvironmentPolicy{OnMismatch: "ignore", OnUnknown: "ignore"}
	}
	return *policy.Environment
}

func testCampaignEnvironmentAction(policy testCampaignPolicy, row *testEnvironmentComparison) string {
	if row == nil {
		return testCampaignEnvironmentSettings(policy).OnUnknown
	}
	switch row.Status {
	case "incompatible":
		return testCampaignEnvironmentSettings(policy).OnMismatch
	case "unknown":
		return testCampaignEnvironmentSettings(policy).OnUnknown
	}
	return "ignore"
}
