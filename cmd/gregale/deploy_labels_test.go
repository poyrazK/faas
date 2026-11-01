package main_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type fakeLabelClient struct {
	status  int
	changes []api.LabelChange
}

func (f *fakeLabelClient) UpdateAppLabels(_ context.Context, _ string, change api.LabelChange) (api.LabelsResponse, error) {
	f.changes = append(f.changes, change)
	if f.status != 0 {
		return api.LabelsResponse{}, &api.APIError{Problem: api.Problem{Status: f.status}}
	}
	return api.LabelsResponse{Labels: change.Set}, nil
}

// A CLI deploy sets the manifest's labels and removes none; where labels are
// not available yet (a 404) the deploy goes on with a note
// (ADR-20261031100025 §7).
func TestDeployManifestLabels(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gregale.yaml"), []byte("labels:\n  team: billing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &fakeLabelClient{}
	if err := main.DeployManifestLabels(t.Context(), client, "app", dir); err != nil {
		t.Fatal(err)
	}
	if len(client.changes) != 1 || client.changes[0].Set["team"] != "billing" || len(client.changes[0].Remove) != 0 {
		t.Fatalf("label writes = %+v", client.changes)
	}

	_, stderr := captureOutput(t)
	gated := &fakeLabelClient{status: http.StatusNotFound}
	if err := main.DeployManifestLabels(t.Context(), gated, "app", dir); err != nil {
		t.Fatalf("a 404 failed the deploy: %v", err)
	}
	if !strings.Contains(stderr.String(), "not applied") {
		t.Errorf("no note for unapplied labels: %q", stderr)
	}
	if err := main.DeployManifestLabels(t.Context(), &fakeLabelClient{status: http.StatusBadRequest}, "app", dir); err == nil {
		t.Error("a refused write did not fail the deploy")
	}

	empty := &fakeLabelClient{}
	if err := main.DeployManifestLabels(t.Context(), empty, "app", t.TempDir()); err != nil || len(empty.changes) != 0 {
		t.Fatalf("no manifest = %v, %d writes", err, len(empty.changes))
	}
}
