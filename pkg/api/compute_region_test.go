package api

import (
	"strings"
	"testing"
)

func TestValidComputeRegion(t *testing.T) {
	for _, region := range []string{"", "local", "eu-fsn", "us-east-1", "a", strings.Repeat("a", 63)} {
		if !ValidComputeRegion(region) {
			t.Errorf("valid region rejected: %q", region)
		}
	}
	for _, region := range []string{"EU-FSN", " eu-fsn", "eu_fsn", "-fsn", "fsn-", "eu.fsn", strings.Repeat("a", 64)} {
		if ValidComputeRegion(region) {
			t.Errorf("invalid region accepted: %q", region)
		}
	}
}
