package main

import (
	"archive/tar"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-985: a repository deploy applies one manifest to one app, so a
// functions manifest must be refused rather than deploying the repo root.
func TestSourceRefManifestRejectsFunctions(t *testing.T) {
	manifest := "functions:\n  - name: thumbs\n    root: functions/thumbs\n    runtime: node24\n"
	archive := operationSchemaArchive(t, []operationSchemaEntry{{"gregale.yaml", manifest, tar.TypeReg}})
	_, problem := loadSourceRefManifest(archive, state.App{Slug: "shop", Type: state.AppTypeApp}, api.PlanPro)
	if problem == nil || problem.Code != CodeAppManifestInvalid || !strings.Contains(problem.Detail, "gregale deploy") {
		t.Fatalf("problem = %+v, want an app_manifest_invalid refusal naming gregale deploy", problem)
	}
	// A function deployed by the CLI fan-out carries the project manifest
	// when it shares the project root; it applies nothing server-side.
	m, problem := loadSourceRefManifest(archive, state.App{Slug: "shop-thumbs", Type: state.AppTypeFunction}, api.PlanPro)
	if problem != nil || m != nil {
		t.Fatalf("function app: manifest = %+v, problem = %+v; want neither", m, problem)
	}
}
