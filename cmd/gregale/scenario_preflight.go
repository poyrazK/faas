package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type testPreflightCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

type testPreflightResult struct {
	Scenario           string               `json:"scenario"`
	Plan               string               `json:"plan"`
	Profiles           []string             `json:"profiles"`
	Repeat             int                  `json:"repeat"`
	WorkloadsPerRun    int                  `json:"workloads_per_run"`
	EstimatedDeploys   int                  `json:"estimated_deploys"`
	MaxWorkloadMinutes int                  `json:"max_workload_minutes"`
	Ready              bool                 `json:"ready"`
	Checks             []testPreflightCheck `json:"checks"`
}

type testPreflightClient interface {
	Whoami(context.Context) (api.AccountResponse, error)
	GetCapabilities(context.Context) (api.CapabilitiesResponse, error)
}

func runTestPreflight(ctx context.Context, client testPreflightClient, name string, scenario testScenario, profiles []string, repeat int) int {
	account, err := client.Whoami(ctx)
	if err != nil {
		return printErr("Could not inspect account", err)
	}
	capabilities, err := client.GetCapabilities(ctx)
	if err != nil {
		return printErr("Could not inspect capabilities", err)
	}
	result := assessTestPreflight(name, scenario, profiles, repeat, account, capabilities)
	if jsonOutput {
		if err := writeJSON(result); err != nil {
			return printErr("Could not print preflight", err)
		}
	} else {
		_, _ = fmt.Fprintf(osStdout, "estimate: %d deployments, up to %d workload-minutes during scenario windows if every VM runs throughout\n", result.EstimatedDeploys, result.MaxWorkloadMinutes)
		for _, check := range result.Checks {
			status := "ok"
			if !check.Passed {
				status = "blocked"
			}
			_, _ = fmt.Fprintf(osStdout, "%s: %s — %s\n", check.Name, status, check.Detail)
		}
	}
	if !result.Ready {
		return 1
	}
	return 0
}

func assessTestPreflight(name string, scenario testScenario, profiles []string, repeat int, account api.AccountResponse, capabilities api.CapabilitiesResponse) testPreflightResult {
	workloads := 1 + len(scenario.Services)
	result := testPreflightResult{
		Scenario: name, Plan: account.Plan, Profiles: profiles, Repeat: repeat,
		WorkloadsPerRun: workloads, EstimatedDeploys: workloads * len(profiles) * repeat * testResilienceRuns(scenario),
		MaxWorkloadMinutes: estimateTestWorkloadMinutes(scenario, len(profiles)*repeat), Ready: true,
	}
	add := func(name string, passed bool, detail string) {
		result.Checks = append(result.Checks, testPreflightCheck{Name: name, Passed: passed, Detail: detail})
		if !passed {
			result.Ready = false
		}
	}
	add("account", account.AbuseHold == nil, "account must allow deployments")
	free := account.Limits.DeveloperApps - account.DeveloperAppCount
	add("developer environments", free >= workloads,
		fmt.Sprintf("need %d concurrent workloads; %d of %d developer app slots available", workloads, free, account.Limits.DeveloperApps))
	limits, known := api.LimitsFor(api.Plan(account.Plan))
	add("plan", known, fmt.Sprintf("recognized plan %q", account.Plan))
	if known {
		add("consumer keys", len(scenario.Consumers) <= limits.ConsumerKeysPerApp,
			fmt.Sprintf("need %d keys on the primary app; plan cap %d", len(scenario.Consumers), limits.ConsumerKeysPerApp))
		asyncRoutes := 0
		for _, service := range scenario.Services {
			asyncRoutes += len(service.AsyncRoutes)
		}
		if asyncRoutes > 0 {
			add("async invocations", limits.AsyncInvokeAllowed,
				fmt.Sprintf("%d declared async routes require async invocations", asyncRoutes))
		}
	}
	capabilityEnabled := func(key string) bool {
		for _, capability := range capabilities.Capabilities {
			if capability.Key == key {
				return capability.Enabled
			}
		}
		return false
	}
	if len(scenario.Buckets) > 0 {
		add("object storage", capabilityEnabled("object-storage"),
			fmt.Sprintf("%d managed buckets require object storage", len(scenario.Buckets)))
	}
	needsPostgres := scenario.Postgres
	for _, service := range scenario.Services {
		needsPostgres = needsPostgres || service.Postgres
	}
	if needsPostgres {
		add("managed PostgreSQL", capabilityEnabled("managed-postgres"), "scenario requests an isolated database")
	}
	for _, reference := range testScenarioEnvironmentReferences(scenario) {
		value, ok := os.LookupEnv(reference.name)
		add("environment "+reference.workload+"/"+reference.name, ok && value != "", "required secret reference; value omitted")
	}
	return result
}

func estimateTestWorkloadMinutes(scenario testScenario, runs int) int {
	runs *= testResilienceRuns(scenario)
	timeout := 15 * time.Minute
	if scenario.Timeout != "" {
		timeout, _ = time.ParseDuration(scenario.Timeout) // manifest validation has already run
	}
	workloads := 1 + len(scenario.Services)
	if scenario.CommandExecution != nil {
		workloads++
		timeout += time.Minute
	}
	return int(math.Ceil(float64(workloads*runs) * timeout.Minutes()))
}

type testSecretEnvironmentReference struct{ workload, name string }

func testScenarioEnvironmentReferences(scenario testScenario) []testSecretEnvironmentReference {
	workloads := map[string]map[string]string{"app": scenario.Secrets}
	for name, service := range scenario.Services {
		workloads[name] = service.Secrets
	}
	seen := map[string]testSecretEnvironmentReference{}
	for workload, secrets := range workloads {
		for _, value := range secrets {
			for _, match := range testSecretReferencePattern.FindAllString(value, -1) {
				parts := strings.Split(match[2:len(match)-1], ".")
				if len(parts) == 2 && parts[0] == "env" && testSecretEnvironmentPattern.MatchString(parts[1]) {
					seen[workload+"/"+parts[1]] = testSecretEnvironmentReference{workload: workload, name: parts[1]}
				}
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]testSecretEnvironmentReference, 0, len(keys))
	for _, key := range keys {
		result = append(result, seen[key])
	}
	return result
}
