package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdAutomationsPublishPolicy(args []string) int {
	fs := newFlagSet("automations publish-policy", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	mode := fs.String("mode", "", "set optional, scenarios or coverage; requires admin scope")
	version := fs.Int64("expected-version", -1, "current policy version; required when setting mode")
	if err := fs.Parse(args); err != nil || rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if strings.TrimSpace(*app) == "" || (*mode != "" && (*version < 0 || (*mode != "optional" && *mode != "scenarios" && *mode != "coverage"))) {
		return printErr("Invalid publishing policy options", errors.New("use --app; to set a policy also use --mode optional|scenarios|coverage and --expected-version"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var policy api.AutomationPublishPolicy
	if *mode == "" {
		policy, err = client.GetAutomationPublishPolicy(context.Background(), *app)
	} else {
		policy, err = client.SetAutomationPublishPolicy(context.Background(), *app, api.SetAutomationPublishPolicyRequest{Mode: *mode, ExpectedVersion: *version})
	}
	if err != nil {
		return printErr("Could not read or set publishing policy", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(policy))
	}
	if _, err := fmt.Fprintf(osStdout, "Publishing policy: %s (version %d)\n", policy.Mode, policy.Version); err != nil {
		return printErr("Could not write policy", err)
	}
	return 0
}

func automationServerCheckRequest(version int64, scenarios []loadedAutomationScenario, requireCoverage bool) api.CheckAutomationPublicationRequest {
	request := api.CheckAutomationPublicationRequest{ExpectedVersion: version, RequireCoverage: requireCoverage, Scenarios: []api.AutomationPublishCheckScenario{}, Exclusions: []api.AutomationCheckExclusion{}}
	if len(scenarios) > 0 {
		for _, x := range scenarios[0].coverageExclusions {
			request.Exclusions = append(request.Exclusions, api.AutomationCheckExclusion{Step: x.Step, Loop: x.Loop, Code: x.Code, Reason: x.Reason})
		}
	}
	for _, s := range scenarios {
		scenario := api.AutomationPublishCheckScenario{Name: s.scenario.Name, Simulation: s.request, Expectations: []api.AutomationCheckExpectation{}}
		convert := func(step, loop string, index *int, e automationStepExpectation, output []byte) api.AutomationCheckExpectation {
			result := api.AutomationCheckExpectation{Step: step, Loop: loop, ItemIndex: index, State: e.State, Reason: e.Reason, WhenMatched: e.WhenMatched, Output: output, AttemptCount: e.AttemptCount}
			if e.Attempts != nil {
				attempts := []api.AutomationCheckAttempt{}
				for _, a := range *e.Attempts {
					attempts = append(attempts, api.AutomationCheckAttempt{Outcome: a.Outcome, HTTPStatus: a.HTTPStatus})
				}
				result.Attempts = &attempts
			}
			return result
		}
		names := []string{}
		for name := range s.scenario.Expect {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			scenario.Expectations = append(scenario.Expectations, convert(name, "", nil, s.scenario.Expect[name], s.outputs[name]))
		}
		loops := []string{}
		for loop := range s.scenario.ExpectItems {
			loops = append(loops, loop)
		}
		sort.Strings(loops)
		for _, loop := range loops {
			indexes := []int{}
			for value := range s.scenario.ExpectItems[loop] {
				i, _ := strconv.Atoi(value)
				indexes = append(indexes, i)
			}
			sort.Ints(indexes)
			for _, i := range indexes {
				index := i
				scenario.Expectations = append(scenario.Expectations, convert(loop+"/action", loop, &index, s.scenario.ExpectItems[loop][strconv.Itoa(i)], s.itemOutputs[automationItemExpectationKey{loop, i}]))
			}
		}
		request.Scenarios = append(request.Scenarios, scenario)
	}
	return request
}
