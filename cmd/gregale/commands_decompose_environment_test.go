package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"strings"
	"testing"
)

func TestValidateProjectEnvironmentFlag(t *testing.T) {
	for _, environment := range []string{"", "staging", "qa", "a", "1", "ab", "12", "1a", "a-1", strings.Repeat("a", 33)} {
		if err := main.ValidateProjectEnvironmentFlag(environment); err != nil {
			t.Errorf("accepted catalog environment %q rejected: %v", environment, err)
		}
	}
	for _, environment := range []string{"default", "__all__", "Bad_Env", "-a", "a-", strings.Repeat("a", 34)} {
		if err := main.ValidateProjectEnvironmentFlag(environment); err == nil {
			t.Errorf("invalid or reserved catalog environment %q accepted", environment)
		}
	}
}
