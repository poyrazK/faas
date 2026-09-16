package main

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func scanMultipartWithProjectSlug(t *testing.T, slug string) *api.Problem {
	t.Helper()
	spool := t.TempDir()
	t.Setenv("FAAS_SPOOL_ROOT", spool)
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", spool)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("project_slug", slug); err != nil {
		t.Fatal(err)
	}
	part, err := mw.CreateFormFile("source", "fixture.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(applyProjectOneWorkloadTarGz(t)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/projects/scan", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	parsed, problem := parseScanMultipart(req, state.Account{}, api.MustLimitsFor(api.PlanPro))
	if parsed != nil {
		_ = os.Remove(parsed.SourcePath)
		_ = os.RemoveAll(parsed.ScanDir)
	}
	return problem
}

func TestParseScanMultipartRejectsInvalidProjectSlugsWithoutTruncation(t *testing.T) {
	for _, slug := range []string{
		"", "Bad_Slug", "-bad", "bad-", strings.Repeat("a", 64),
		strings.Repeat("a", 64) + "x", strings.Repeat("a", 64) + "y",
	} {
		problem := scanMultipartWithProjectSlug(t, slug)
		if problem == nil || problem.Status != 422 || problem.Code != api.CodeValidation {
			t.Errorf("slug %q problem = %#v, want 422 validation", slug, problem)
		}
	}
}

func TestParseScanMultipartAcceptsMaximumProjectSlug(t *testing.T) {
	if problem := scanMultipartWithProjectSlug(t, strings.Repeat("a", 63)); problem != nil {
		t.Fatalf("63-character project slug rejected: %#v", problem)
	}
}
