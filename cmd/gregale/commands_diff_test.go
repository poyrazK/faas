package main

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeDeployPreviewFlags(t *testing.T) {
	tests := []struct {
		name    string
		dryRun  bool
		diff    bool
		want    bool
		wantErr error
	}{
		{name: "neither", want: false},
		{name: "diff compatibility", diff: true, want: true},
		{name: "dry run", dryRun: true, want: true},
		{name: "aliases are mutually exclusive", dryRun: true, diff: true, wantErr: errors.New("--dry-run and --diff are aliases; use only one")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeDeployPreviewFlags(tt.dryRun, tt.diff)
			if got != tt.want {
				t.Fatalf("normalizeDeployPreviewFlags() = %t, want %t", got, tt.want)
			}
			if (err == nil) != (tt.wantErr == nil) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil && err.Error() != tt.wantErr.Error() {
				t.Fatalf("error = %q, want %q", err, tt.wantErr)
			}
		})
	}
}

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
