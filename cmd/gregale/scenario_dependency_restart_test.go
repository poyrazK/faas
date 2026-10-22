package main_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestHTTPDependencyRestartExampleManifest(t *testing.T) {
	t.Setenv("GREGALE_DEPENDENCY_DATABASE_URL", "")
	t.Setenv("GREGALE_DEPENDENCY_DATABASE_DISPOSABLE", "")
	prepared, err := main.PrepareTestSuite("../../examples/scenario-http-write/gregale-dependency-restart-test.yaml", "http-dependency-restart", main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 2 {
		t.Fatal("missing dependency restart checkpoint")
	}
	for _, p := range prepared {
		if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
			t.Fatal(err)
		}
		restart := p.Scenario.Steps[1].Restart
		if restart.Workload != "payment" || restart.InFlight == nil || p.Scenario.Services["payment"].Secrets["LEDGER_NAMESPACE"] != "${run.id}" || len(p.Scenario.Cleanup) != 1 {
			t.Fatal("dependency restart isolation or cleanup missing")
		}
		if restart.Readiness.Expect.JSON["/held"] != 0 {
			t.Fatal("old held provider can satisfy replacement readiness")
		}
	}
}

func TestScenarioSecretEnvironmentReferencesValidateWithoutCredentialsAndFailClosed(t *testing.T) {
	const name = "GREGALE_DEPENDENCY_DATABASE_URL"
	t.Setenv(name, "")
	scenario := main.TestScenario{Project: "caller", Services: map[string]main.TestService{"payment": {Secrets: map[string]string{"DATABASE_URL": "${env." + name + "}"}}}}
	if err := main.ValidateTestSecrets("payment", scenario.Services["payment"].Secrets, scenario); err != nil {
		t.Fatal(err)
	}
	if _, err := main.ExpandTestSecretValue("${env."+name+"}", nil, nil, nil, "run", "secret"); err == nil {
		t.Fatal("empty required environment reference accepted")
	}
	value := "postgres://runtime:private-password@example.invalid/database?sslmode=require"
	t.Setenv(name, value)
	got, err := main.ExpandTestSecretValue("${env."+name+"}", nil, nil, nil, "run", "secret")
	if err != nil || got != value {
		t.Fatal("environment reference not resolved")
	}
	for _, invalid := range []string{"${env.lowercase}", "${env.MISSING_EXTRA.part}"} {
		if _, err := main.ExpandTestSecretValue(invalid, nil, nil, nil, "run", "secret"); err == nil {
			t.Fatal("invalid reference accepted")
		}
	}
	account := api.AccountResponse{Plan: "pro"}
	result := main.AssessTestPreflight("dependency", scenario, []string{"warm"}, 1, account, api.CapabilitiesResponse{})
	found := false
	for _, check := range result.Checks {
		if strings.HasPrefix(check.Name, "environment ") {
			found = true
			if !check.Passed {
				t.Fatal("present reference blocked")
			}
		}
	}
	if !found {
		t.Fatal("preflight omitted environment binding check")
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "private-password") {
		t.Fatal("preflight exposed credentials")
	}
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	result = main.AssessTestPreflight("dependency", scenario, []string{"warm"}, 1, account, api.CapabilitiesResponse{})
	for _, check := range result.Checks {
		if strings.HasPrefix(check.Name, "environment ") && check.Passed {
			t.Fatal("missing reference passed preflight")
		}
	}
}

func TestHTTPDependencyRestartNativeAuthenticationManifest(t *testing.T) {
	prepared, err := main.PrepareTestSuite("../../examples/scenario-http-write/gregale-native-auth-test.yaml", "http-auth-stall", main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("native authentication scenario: %v", err)
	}
	p := prepared[0]
	if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
		t.Fatal(err)
	}
	if p.Scenario.Services["payment"].Secrets["GREGALE_NATIVE_AUTH_STALL"] != "1" || len(p.Scenario.Command) != 3 || len(p.Scenario.Steps) != 0 || len(p.Scenario.Cleanup) != 1 {
		t.Fatal("native authentication isolation or cleanup missing")
	}
}

func TestHTTPDependencyRestartNativeCommitLossManifest(t *testing.T) {
	prepared, err := main.PrepareTestSuite("../../examples/scenario-http-write/gregale-native-commit-test.yaml", "http-commit-loss", main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("native COMMIT scenario: %v", err)
	}
	p := prepared[0]
	if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
		t.Fatal(err)
	}
	if p.Scenario.Services["payment"].Secrets["GREGALE_NATIVE_COMMIT_LOSS"] != "1" || len(p.Scenario.Command) != 3 || len(p.Scenario.Steps) != 0 || len(p.Scenario.Cleanup) != 1 {
		t.Fatal("native COMMIT isolation or cleanup missing")
	}
}

func TestHTTPDependencyRestartNativeCommitDeadlineManifests(t *testing.T) {
	for _, direction := range []string{"request", "response"} {
		t.Run(direction, func(t *testing.T) {
			prepared, err := main.PrepareTestSuite("../../examples/scenario-http-write/gregale-native-commit-"+direction+"-test.yaml", "http-commit-"+direction, main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
			if err != nil || len(prepared) != 1 {
				t.Fatalf("native COMMIT deadline scenario: %v", err)
			}
			p := prepared[0]
			if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
				t.Fatal(err)
			}
			if p.Scenario.Secrets["CHECKOUT_DEADLINE_MS"] != "1500" || p.Scenario.Services["payment"].Secrets["GREGALE_NATIVE_COMMIT_MODE"] != direction+"-deadline" || len(p.Scenario.Command) != 4 || len(p.Scenario.Steps) != 0 || len(p.Scenario.Cleanup) != 1 {
				t.Fatal("native COMMIT deadline, fault direction or cleanup missing")
			}
		})
	}
}

func TestHTTPDependencyRestartNativeBandwidthManifest(t *testing.T) {
	prepared, err := main.PrepareTestSuite("../../examples/scenario-http-write/gregale-native-bandwidth-test.yaml", "http-bandwidth", main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("native bandwidth scenario: %v", err)
	}
	p := prepared[0]
	if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
		t.Fatal(err)
	}
	if p.Scenario.Services["payment"].Secrets["GREGALE_NATIVE_BANDWIDTH"] != "1" || len(p.Scenario.Command) != 3 || len(p.Scenario.Steps) != 0 || len(p.Scenario.Cleanup) != 1 {
		t.Fatal("native bandwidth opt-in or cleanup missing")
	}
}

func TestHTTPDependencyRestartNativeReadCutManifest(t *testing.T) {
	prepared, err := main.PrepareTestSuite("../../examples/scenario-http-write/gregale-native-read-cut-test.yaml", "http-read-cut", main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("native read-cut scenario: %v", err)
	}
	p := prepared[0]
	if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
		t.Fatal(err)
	}
	if p.Scenario.Services["payment"].Secrets["GREGALE_NATIVE_READ_CUT"] != "1" || len(p.Scenario.Command) != 3 || len(p.Scenario.Steps) != 0 || len(p.Scenario.Cleanup) != 1 {
		t.Fatal("native read-cut opt-in or cleanup missing")
	}
}

func TestHTTPDependencyRestartNativeSSLRequestManifest(t *testing.T) {
	prepared, err := main.PrepareTestSuite("../../examples/scenario-http-write/gregale-native-ssl-request-test.yaml", "http-ssl-request", main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("native SSLRequest scenario: %v", err)
	}
	p := prepared[0]
	if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
		t.Fatal(err)
	}
	if p.Scenario.Services["payment"].Secrets["GREGALE_NATIVE_SSL_REQUEST"] != "1" || len(p.Scenario.Command) != 3 || len(p.Scenario.Steps) != 0 || len(p.Scenario.Cleanup) != 1 {
		t.Fatal("native SSLRequest opt-in or cleanup missing")
	}
}

func TestHTTPDependencyRestartNativeRetryBudgetManifest(t *testing.T) {
	prepared, err := main.PrepareTestSuite("../../examples/scenario-http-write/gregale-native-retry-budget-test.yaml", "http-retry-budget", main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("native retry budget scenario: %v", err)
	}
	p := prepared[0]
	if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
		t.Fatal(err)
	}
	if p.Scenario.Secrets["GREGALE_RETRY_BUDGET_FIXTURE"] != "1" || p.Scenario.Services["payment"].Secrets["GREGALE_NATIVE_SSL_REQUEST"] != "1" || len(p.Scenario.Command) != 3 || len(p.Scenario.Steps) != 0 || len(p.Scenario.Cleanup) != 1 {
		t.Fatal("native retry budget opt-in, dependency fault or cleanup missing")
	}
}

func TestHTTPDependencyRestartNativeCircuitBreakerManifest(t *testing.T) {
	prepared, err := main.PrepareTestSuite("../../examples/scenario-http-write/gregale-native-circuit-breaker-test.yaml", "http-circuit-breaker", main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("native circuit breaker scenario: %v", err)
	}
	p := prepared[0]
	if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
		t.Fatal(err)
	}
	if p.Scenario.Secrets["GREGALE_BREAKER_FIXTURE"] != "1" || p.Scenario.Services["payment"].Secrets["GREGALE_NATIVE_SSL_REQUEST"] != "1" || len(p.Scenario.Command) != 3 || len(p.Scenario.Steps) != 0 || len(p.Scenario.Cleanup) != 1 {
		t.Fatal("native circuit breaker opt-in, dependency fault or cleanup missing")
	}
}

func TestHTTPDependencyRestartNativeBulkheadManifest(t *testing.T) {
	prepared, err := main.PrepareTestSuite("../../examples/scenario-http-write/gregale-native-bulkhead-test.yaml", "http-bulkhead", main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("native bulkhead scenario: %v", err)
	}
	p := prepared[0]
	if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
		t.Fatal(err)
	}
	if p.Scenario.Secrets["GREGALE_BULKHEAD_FIXTURE"] != "1" || p.Scenario.Services["payment"].Secrets["GREGALE_NATIVE_SSL_REQUEST"] != "1" || len(p.Scenario.Command) != 3 || len(p.Scenario.Steps) != 0 || len(p.Scenario.Cleanup) != 1 {
		t.Fatal("native bulkhead opt-in, dependency fault or cleanup missing")
	}
}

func TestHTTPDependencyRestartNativeCombinedManifest(t *testing.T) {
	prepared, err := main.PrepareTestSuite("../../examples/scenario-http-write/gregale-native-combined-test.yaml", "http-combined", main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("native combined scenario: %v", err)
	}
	p := prepared[0]
	if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
		t.Fatal(err)
	}
	if p.Scenario.Secrets["GREGALE_COMBINED_FIXTURE"] != "1" || p.Scenario.Services["payment"].Secrets["GREGALE_NATIVE_SSL_REQUEST"] != "1" || len(p.Scenario.Command) != 3 || len(p.Scenario.Steps) != 0 || len(p.Scenario.Cleanup) != 1 {
		t.Fatal("native combined opt-in, dependency fault or cleanup missing")
	}
}

func TestHTTPDependencyRestartNativeRepeatedManifest(t *testing.T) {
	prepared, err := main.PrepareTestSuite("../../examples/scenario-http-write/gregale-native-repeated-test.yaml", "http-repeated", main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("native repeated scenario: %v", err)
	}
	p := prepared[0]
	if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
		t.Fatal(err)
	}
	if p.Scenario.Secrets["GREGALE_COMBINED_FIXTURE"] != "1" || p.Scenario.Services["payment"].Secrets["GREGALE_NATIVE_SSL_REQUEST"] != "1" || len(p.Scenario.Command) != 3 || len(p.Scenario.Steps) != 0 || len(p.Scenario.Cleanup) != 1 {
		t.Fatal("native repeated opt-in, dependency fault or cleanup missing")
	}
}

func TestHTTPDependencyRestartNativeIsolationManifest(t *testing.T) {
	prepared, err := main.PrepareTestSuite("../../examples/scenario-http-write/gregale-native-isolation-test.yaml", "http-isolation", main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("native isolation scenario: %v", err)
	}
	p := prepared[0]
	if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
		t.Fatal(err)
	}
	if p.Scenario.Secrets["GREGALE_ISOLATION_FIXTURE"] != "1" || p.Scenario.Services["payment"].Secrets["GREGALE_NATIVE_SSL_REQUEST"] != "1" || len(p.Scenario.Command) != 3 || len(p.Scenario.Steps) != 0 || len(p.Scenario.Cleanup) != 1 || len(p.Scenario.Services) != 2 || p.Scenario.Services["inventory"].Secrets["GREGALE_NATIVE_SSL_REQUEST"] != "1" {
		t.Fatal("native isolation opt-in, dependency fault or cleanup missing")
	}
}
