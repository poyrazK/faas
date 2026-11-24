package main_test

// adr: 20261123192254 — --tool-weight, --tool-limit and --route-limit.

import (
	"reflect"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestParseToolWeights(t *testing.T) {
	got, err := main.ParseToolWeights(map[string]int64{"POST /v1/x": 2}, []string{"generate=20", "search = 1"})
	want := map[string]int64{"POST /v1/x": 2, "MCP /tools/generate": 20, "MCP /tools/search": 1}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v err=%v", got, err)
	}
	for _, bad := range [][]string{{"generate"}, {"bad name=2"}, {"generate=x"}, {"generate=2", "generate=3"}} {
		if _, err := main.ParseToolWeights(nil, bad); err == nil {
			t.Errorf("%v must be refused", bad)
		}
	}
}

func TestRouteLimitEdits(t *testing.T) {
	current := map[string]api.APIConsumerPlanRouteLimit{
		"GET /v1/old":       {MaxRequestsPerMinute: 1},
		"MCP /tools/keep":   {MaxRequestsPerMonth: 5},
		"MCP /tools/search": {MaxRequestsPerMinute: 9},
	}
	got, err := main.ApplyRouteLimitSpecs(current, []string{"POST /v1/generate=10/minute,500/month", "GET /v1/old=none"}, []string{"search=1000/month"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]api.APIConsumerPlanRouteLimit{
		"POST /v1/generate": {MaxRequestsPerMinute: 10, MaxRequestsPerMonth: 500},
		"MCP /tools/keep":   {MaxRequestsPerMonth: 5},
		"MCP /tools/search": {MaxRequestsPerMonth: 1000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	if kept, err := main.ApplyRouteLimitSpecs(current, nil, nil); err != nil || kept != nil {
		t.Fatalf("no edits must keep the plan's route limits (nil): %v %v", kept, err)
	}
	for _, bad := range []struct{ routes, tools []string }{
		{[]string{"POST /x"}, nil},
		{[]string{"POST /x=10/hour"}, nil},
		{[]string{"POST /x=0/minute"}, nil},
		{nil, []string{"bad name=1/month"}},
		{nil, []string{"search=1/month", "search=none"}},
	} {
		if _, err := main.ApplyRouteLimitSpecs(nil, bad.routes, bad.tools); err == nil {
			t.Errorf("%v must be refused", bad)
		}
	}
}
