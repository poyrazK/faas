package api_test

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
)

func TestScenarioRequestCorrelationWireConversion(t *testing.T) {
	source := `{"from":"caller","to":"payment","kind":"http_status","status_code":503,"percent":100,"seed":1,"correlation_scope":"1234567890abcdef"}`
	var wire api.ScenarioTestChaosRule
	if err := json.Unmarshal([]byte(source), &wire); err != nil {
		t.Fatal(err)
	}
	rule := chaos.Rule(wire)
	if rule.CorrelationScope != "1234567890abcdef" {
		t.Fatal("wire conversion dropped correlation scope")
	}
	if err := (chaos.Plan{DurationMS: 1000, Rules: []chaos.Rule{rule}}).Validate(); err != nil {
		t.Fatal(err)
	}
	back := api.ScenarioTestChaosRule(rule)
	data, err := json.Marshal(back)
	if err != nil {
		t.Fatal(err)
	}
	var decoded api.ScenarioTestChaosRule
	if err := json.Unmarshal(data, &decoded); err != nil || decoded != wire {
		t.Fatal("round trip changed rule")
	}
}
