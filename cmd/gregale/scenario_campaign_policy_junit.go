package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
)

type testCampaignJUnitSuite struct {
	XMLName  xml.Name                `xml:"testsuite"`
	Name     string                  `xml:"name,attr"`
	Tests    int                     `xml:"tests,attr"`
	Failures int                     `xml:"failures,attr"`
	Cases    []testCampaignJUnitCase `xml:"testcase"`
}

type testCampaignJUnitCase struct {
	Name      string                    `xml:"name,attr"`
	Classname string                    `xml:"classname,attr"`
	Failure   *testCampaignJUnitFailure `xml:"failure,omitempty"`
	Output    string                    `xml:"system-out,omitempty"`
}

type testCampaignJUnitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Body    string `xml:",chardata"`
}

// A testcase represents a policy decision, not an application request or run.
// Standard XML encoding escapes receipt strings, URLs and replay commands.
func marshalTestCampaignPolicyJUnit(policy testCampaignPolicy, result testCampaignPolicyResult) ([]byte, error) {
	suite := testCampaignJUnitSuite{Name: "gregale.campaign-policy", Cases: make([]testCampaignJUnitCase, 0)}
	for index, violation := range result.Violations {
		effective := policy
		if violation.EffectivePolicy != nil {
			settings := violation.EffectivePolicy
			effective.FailNewFailures, effective.MaxFrequencyIncrease = settings.FailNewFailures, settings.MaxFrequencyIncrease
			effective.MinExecutedRuns, effective.NotComparable, effective.InsufficientSamples = settings.MinExecutedRuns, settings.NotComparable, settings.InsufficientSamples
		}
		context, err := json.Marshal(violation.Context)
		if err != nil {
			return nil, err
		}
		detail, err := json.MarshalIndent(struct {
			Rule                 string                       `json:"rule"`
			Reason               string                       `json:"reason"`
			FailNewFailures      bool                         `json:"fail_new_failures"`
			MaxFrequencyIncrease *float64                     `json:"max_frequency_increase_percentage_points"`
			MinExecutedRuns      int                          `json:"min_executed_runs"`
			NotComparable        string                       `json:"on_not_comparable"`
			InsufficientSamples  string                       `json:"on_insufficient_samples"`
			BaselineURL          string                       `json:"baseline_evidence_url,omitempty"`
			CurrentURL           string                       `json:"current_evidence_url,omitempty"`
			Violation            testCampaignPolicyViolation `json:"violation"`
		}{
			Rule: violation.Rule, Reason: violation.Reason,
			FailNewFailures: effective.FailNewFailures, MaxFrequencyIncrease: effective.MaxFrequencyIncrease,
			MinExecutedRuns: effective.MinExecutedRuns, NotComparable: effective.NotComparable,
			InsufficientSamples: effective.InsufficientSamples, BaselineURL: result.BaselineURL,
			CurrentURL: result.CurrentURL, Violation: violation,
		}, "", "  ")
		if err != nil {
			return nil, err
		}
		suite.Cases = append(suite.Cases, testCampaignJUnitCase{
			Name: fmt.Sprintf("%s / %s [%d] configuration=%s context=%s", violation.PolicyRule, violation.Rule, index+1, violation.Configuration, context),
			Classname: "gregale.campaign-policy",
			Failure: &testCampaignJUnitFailure{Message: violation.Reason, Type: violation.Rule, Body: string(detail)},
		})
	}
	if len(suite.Cases) == 0 {
		decisions, err := json.Marshal(result.Decisions)
		if err != nil {
			return nil, err
		}
		suite.Cases = append(suite.Cases, testCampaignJUnitCase{
			Name: "campaign policy accepted", Classname: "gregale.campaign-policy",
			Output: "No policy violations. This includes explicitly ignored coverage and sample conditions; it does not assert that all application tests passed. Matched comparison rules: " + string(decisions),
		})
	}
	suite.Tests, suite.Failures = len(suite.Cases), len(result.Violations)
	body, err := xml.MarshalIndent(suite, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}
