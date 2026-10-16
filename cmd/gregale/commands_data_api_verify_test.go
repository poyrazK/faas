package main

import "testing"

func TestDataAPIVerifyRejectsInvalidOptions(t *testing.T) {
	for _, args := range [][]string{nil, {"unexpected"}, {"--checks", "checks.json", "--directory", ""}, {"--unknown"}} {
		if cmdDataAPIVerify(args) == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
}
