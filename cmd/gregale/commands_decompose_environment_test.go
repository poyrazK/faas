package main

import (
	"strings"
	"testing"
)

func TestValidateProjectEnvironmentFlag(t *testing.T) {
	for _, environment := range []string{"", "staging", "qa", "a", "1", "ab", "12", "1a", "a-1", strings.Repeat("a", 33)} {
		if err := validateProjectEnvironmentFlag(environment); err != nil {
			t.Errorf("accepted catalog environment %q rejected: %v", environment, err)
		}
	}
	for _, environment := range []string{"default", "__all__", "Bad_Env", "-a", "a-", strings.Repeat("a", 34)} {
		if err := validateProjectEnvironmentFlag(environment); err == nil {
			t.Errorf("invalid or reserved catalog environment %q accepted", environment)
		}
	}
}
