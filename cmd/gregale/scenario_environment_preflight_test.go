package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type environmentPreflightFakeClient struct {
	Apps map[string]api.AppResponse
	Errors map[string]error
	Calls []string
}

func (client *environmentPreflightFakeClient) GetApp(_ context.Context, slug string) (api.AppResponse, error) {
	client.Calls = append(client.Calls, slug)
	if err := client.Errors[slug]; err != nil { return api.AppResponse{}, err }
	app, exists := client.Apps[slug]
	if !exists { return api.AppResponse{}, errors.New("missing app") }
	return app, nil
}

func environmentPreflightTestInputs() (testCampaignPolicy, map[string]testScenario, testEnvironmentPreflightTargets, *environmentPreflightFakeClient) {
	r := environmentTestReceipt("run", true)
	r.Scenario = "checkout"
	policy := requiredEnvironmentTestPolicy(r)
	runtime, memory := "go124", 128
	policy.RequiredEnvironments[0].Expect.Workloads["checkout"] = testWorkloadExpectation{Runtime: &runtime, RAMMB: &memory}
	scenarios := map[string]testScenario{"checkout": {Project: "checkout", Command: []string{"never-execute"}}}
	targets := testEnvironmentPreflightTargets{SchemaVersion: 1, Scenarios: map[string]map[string]string{"checkout": {"checkout": "live-checkout"}}}
	client := &environmentPreflightFakeClient{Apps: map[string]api.AppResponse{"live-checkout": {
		ID: "transient-app-id", Slug: "live-checkout", URL: "https://user:secret-value@host.test",
		Type: "function", Runtime: "go124", RAMMB: 128, VCPU: 1, CPUMillicores: 250,
		MaxConcurrency: 5, ConcurrencyPerVMBound: 5, Status: "active",
	}}}
	return policy, scenarios, targets, client
}

func TestEnvironmentPreflightReadsOnlyMappedResourcesAndCachesTargets(t *testing.T) {
	// adr: 984 — preflight observes existing app metadata without creating execution evidence.
	policy, scenarios, targets, client := environmentPreflightTestInputs()
	second := policy.RequiredEnvironments[0]
	second.Name = "same-target"
	policy.RequiredEnvironments = append(policy.RequiredEnvironments, second)
	report := inspectTestEnvironmentPreflight(context.Background(), client, policy, scenarios, targets, "targets-digest")
	if !report.Ready || report.ReadErrors != 0 || len(client.Calls) != 1 || client.Calls[0] != "live-checkout" ||
		len(report.Requirements) != 2 || report.EvidenceKind != "platform_preflight" || report.ExecutionEvidence {
		t.Fatalf("unexpected preflight: %+v", report)
	}
	if report.Requirements[0].ExecutionContextVerified || report.Requirements[0].Workloads[0].ObservedAt.IsZero() ||
		report.StartedAt.IsZero() || report.FinishedAt.Before(report.StartedAt) {
		t.Fatal("preflight invented execution context or lost observation times")
	}
	body, err := json.Marshal(report)
	if err != nil { t.Fatal(err) }
	for _, forbidden := range []string{"secret-value", "transient-app-id", "host.test", "never-execute"} {
		if strings.Contains(string(body), forbidden) { t.Fatalf("preflight leaked raw metadata %q", forbidden) }
	}
	var receipts []testRunReceipt
	if err := json.Unmarshal(body, &receipts); err == nil { t.Fatal("preflight document was accepted as execution receipts") }
}

func TestEnvironmentPreflightDetectsMismatchAndUnknownVersions(t *testing.T) {
	// adr: 984 — live settings and unavailable binary versions remain distinct.
	policy, scenarios, targets, client := environmentPreflightTestInputs()
	app := client.Apps["live-checkout"]
	app.RAMMB = 256
	client.Apps["live-checkout"] = app
	report := inspectTestEnvironmentPreflight(context.Background(), client, policy, scenarios, targets, "")
	found := false
	for _, check := range report.Requirements[0].Checks {
		found = found || (check.Field == "workloads.checkout.ram_mb" && check.Status == "mismatch" &&
			string(check.Expected) == "128" && string(check.Observed) == "256")
	}
	if report.Ready || !found { t.Fatal("live resource mismatch was hidden") }
	policy.RequiredEnvironments[0].Expect.RequirePlatformVersion = true
	report = inspectTestEnvironmentPreflight(context.Background(), client, policy, scenarios, targets, "")
	found = false
	for _, check := range report.Requirements[0].Checks {
		found = found || (check.Field == "platform_version.known" && check.Status == "unavailable" && string(check.Observed) == "null")
	}
	if !found || report.Ready { t.Fatal("unknown platform version was inferred") }
}

func TestEnvironmentPreflightDoesNotTreatDeclarationsAsLiveDependencyProof(t *testing.T) {
	// adr: 984 — resource API responses do not verify fixture behavior or database attachment.
	policy, scenarios, targets, client := environmentPreflightTestInputs()
	scenario := scenarios["checkout"]
	scenario.Postgres = true
	scenarios["checkout"] = scenario
	yes := true
	want := policy.RequiredEnvironments[0].Expect.Workloads["checkout"]
	want.Postgres = &yes
	policy.RequiredEnvironments[0].Expect.Workloads["checkout"] = want
	report := inspectTestEnvironmentPreflight(context.Background(), client, policy, scenarios, targets, "")
	declared, unavailable := false, false
	for _, check := range report.Requirements[0].Checks {
		if check.Field == "workloads.checkout.postgres" {
			declared = declared || (check.Source == "manifest" && check.Status == "declared")
			unavailable = unavailable || (check.Source == "platform" && check.Status == "unavailable")
		}
	}
	if report.Ready || !declared || !unavailable { t.Fatal("manifest Postgres declaration became live proof") }
	if _, exists := report.Requirements[0].Workloads[0].Fields["postgres"]; exists { t.Fatal("fabricated Postgres observation") }
}

func TestEnvironmentPreflightHandlesReadFailuresWithoutLeakingErrors(t *testing.T) {
	// adr: 984 — unreadable targets do not become absent-workload or readiness claims.
	policy, scenarios, targets, client := environmentPreflightTestInputs()
	client.Errors = map[string]error{"live-checkout": errors.New("secret-value https://user:password@private.test")}
	report := inspectTestEnvironmentPreflight(context.Background(), client, policy, scenarios, targets, "")
	if report.Ready || report.ReadErrors != 1 || report.Requirements[0].Workloads[0].ReadStatus != "read_error" {
		t.Fatal("failed target read was hidden")
	}
	body, err := json.Marshal(report)
	if err != nil || strings.Contains(string(body), "secret-value") || strings.Contains(string(body), "private.test") { t.Fatal("raw API error leaked") }
	policy, scenarios, targets, client = environmentPreflightTestInputs()
	delete(targets.Scenarios["checkout"], "checkout")
	report = inspectTestEnvironmentPreflight(context.Background(), client, policy, scenarios, targets, "")
	if report.Ready || len(client.Calls) != 0 || report.Requirements[0].Workloads[0].ReadStatus != "unmapped" { t.Fatal("unmapped workload was guessed") }
	policy, scenarios, targets, client = environmentPreflightTestInputs()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report = inspectTestEnvironmentPreflight(ctx, client, policy, scenarios, targets, "")
	if report.Ready || len(client.Calls) != 0 { t.Fatal("preflight continued reading after cancellation") }
}

func TestEnvironmentPreflightTargetsAreStrictAndExplicit(t *testing.T) {
	// adr: 984 — explicit slug mappings avoid guessing ephemeral or production workloads.
	_, scenarios, _, _ := environmentPreflightTestInputs()
	path := filepath.Join(t.TempDir(), "targets.yaml")
	valid := "schema_version: 1\nscenarios:\n  checkout:\n    checkout: live-checkout\n"
	for _, body := range []string{
		strings.Replace(valid, "schema_version: 1", "schema_version: 2", 1),
		valid + "---\n{}\n",
		valid + "unknown: true\n",
		strings.Replace(valid, "live-checkout", "../private", 1),
		strings.Replace(valid, "    checkout:", "    unknown-workload:", 1),
		strings.Replace(valid, "  checkout:", "  unknown-scenario:", 1),
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil { t.Fatal(err) }
		if _, _, err := readTestEnvironmentPreflightTargets(path, scenarios); err == nil { t.Fatal("invalid target mapping accepted") }
	}
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil { t.Fatal(err) }
	targets, digest, err := readTestEnvironmentPreflightTargets(path, scenarios)
	if err != nil || len(digest) != 64 || targets.Scenarios["checkout"]["checkout"] != "live-checkout" { t.Fatal("valid target mapping rejected") }
}

func TestEnvironmentPreflightRejectsAliasedTargetsAndMismatchedResponses(t *testing.T) {
	// adr: 984 — logical workloads must remain bound to distinct explicit targets.
	policy, scenarios, targets, client := environmentPreflightTestInputs()
	scenario := scenarios["checkout"]
	scenario.Services = map[string]testService{"database": {Source: "."}}
	scenarios["checkout"] = scenario
	path := filepath.Join(t.TempDir(), "targets.yaml")
	body := "schema_version: 1\nscenarios:\n  checkout:\n    checkout: live-checkout\n    database: live-checkout\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil { t.Fatal(err) }
	if _, _, err := readTestEnvironmentPreflightTargets(path, scenarios); err == nil { t.Fatal("aliased logical targets accepted") }
	app := client.Apps["live-checkout"]
	app.Slug = "different-app"
	client.Apps["live-checkout"] = app
	report := inspectTestEnvironmentPreflight(context.Background(), client, policy, scenarios, targets, "")
	if report.Ready || report.ReadErrors != 1 || report.Requirements[0].Workloads[0].ReadStatus != "identity_mismatch" {
		t.Fatal("response metadata was attributed to the wrong mapped target")
	}
}
