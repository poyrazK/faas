package main

import (
	"archive/tar"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-976: a repository deploy applies one manifest to one app, so a
// functions manifest must be refused rather than deploying the repo root.
func TestSourceRefManifestRejectsFunctions(t *testing.T) {
	manifest := "functions:\n  - name: thumbs\n    root: functions/thumbs\n    runtime: node24\n"
	archive := operationSchemaArchive(t, []operationSchemaEntry{{"gregale.yaml", manifest, tar.TypeReg}})
	_, problem := loadSourceRefManifest(archive, state.App{Slug: "shop"}, api.PlanPro)
	if problem == nil || problem.Code != CodeAppManifestInvalid || !strings.Contains(problem.Detail, "gregale deploy") {
		t.Fatalf("problem = %+v, want an app_manifest_invalid refusal naming gregale deploy", problem)
	}
}
