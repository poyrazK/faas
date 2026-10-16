package main

import "testing"

func TestDataAPIExplainRejectsInvalidOptions(t *testing.T) {
	for _, args := range [][]string{nil, {"unexpected"}, {"--collection", "requests.json", "--directory", ""}, {"--unknown"}, {"--collection", "requests.json", "--check"}, {"--collection", "requests.json", "--budget", "budgets.json"}, {"--collection", "requests.json", "--check", "--baseline", "plans.json"}} {
		if cmdDataAPIExplain(args) == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
}
