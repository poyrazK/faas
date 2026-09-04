package main

import (
	"strings"
	"testing"
)

func TestValidateDeployDiffManifest_RejectsWorkflows(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `workflows:
  - name: process_order
    steps:
      - name: charge
        run: charge_stripe
`)

	err := validateDeployDiffManifest(dir)
	if err == nil || !strings.Contains(err.Error(), "not supported by deploy --diff") {
		t.Fatalf("error = %v, want explicit workflow diff error", err)
	}
}
