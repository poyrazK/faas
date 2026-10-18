package alerts

import (
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"gopkg.in/yaml.v3"
)

func TestMCPProtocolPromtoolFixtureUsesEvaluatorQuery(t *testing.T) {
	raw, err := os.ReadFile("../promqlrules/testdata/mcp_customer_alerts.test.yml")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Tests []struct {
			Expressions []struct {
				Expr string `yaml:"expr"`
			} `yaml:"promql_expr_test"`
		} `yaml:"tests"`
	}
	if yaml.Unmarshal(raw, &fixture) != nil {
		t.Fatal("invalid fixture")
	}
	for _, metric := range []string{api.MetricMCPProtocolErrorRate, api.MetricMCPProtocolP95} {
		rule := state.AlertRule{AppID: "00000000-0000-4000-8000-000000000001", Metric: state.AlertMetric(metric), WindowSpec: "5m", Comparison: state.AlertGt, Threshold: 10, MCPMinCalls: 20, MCPForMinutes: 10}
		if metric == api.MetricMCPProtocolP95 {
			rule.Threshold = 5000
		}
		value, count := mcpProtocolExpressions(rule)
		query := mcpProtocolSustainedQuery(rule, value, count)
		found := false
		for _, test := range fixture.Tests {
			for _, expr := range test.Expressions {
				if expr.Expr == query {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("promtool fixture does not exercise evaluator query for %s", metric)
		}
	}
}
