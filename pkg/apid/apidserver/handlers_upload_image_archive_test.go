package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/ociarchive/ociarchivetest"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261127120000

// setupLocalImage is a test env with local image deploys released.
func setupLocalImage(t *testing.T, plan api.Plan) apidserver.TestEnv {
	t.Helper()
	e := apidserver.Setup(t, plan)
	(*e.S()).EnablePlatformFlagForAll(platformflags.LocalImageDeploys)
	return e
}

func TestUploadImageArchive_RefusedWhileFlagOff(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := apidserver.Setup(t, api.PlanFree)
	e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "img"}, nil)
	rec := startImageUpload(t, e, "img", 1024, imageUploadOptions(""))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Local image deploys unavailable") {
		t.Fatalf("flag off: %d %s", rec.Code, rec.Body.String())
	}
}

// Turning the flag off also stops an upload opened while it was on: an
// older builderd must never receive one.
func TestUploadImageArchive_FlagOffAtCommit(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setupLocalImage(t, api.PlanFree)
	e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "img"}, nil)
	archive, _ := ociarchivetest.Canonical(t, 0)
	rec := startImageUpload(t, e, "img", int64(len(archive)), imageUploadOptions(""))
	var started apidserver.StartUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil || rec.Code != http.StatusCreated {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	appendChunk(t, e, started.UploadID, 0, archive)
	(*e.S()).SeedPlatformFlag(platformflags.LocalImageDeploys, platformflags.Definition{})
	if rec := commitUpload(e, started.UploadID); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Local image deploys unavailable") {
		t.Fatalf("commit with flag off: %d %s", rec.Code, rec.Body.String())
	}
}

func imageUploadOptions(name string) *api.UploadDeployOptions {
	return &api.UploadDeployOptions{SourceFormat: api.UploadSourceFormatOCIImageArchive, ImageName: name, Reason: "local image"}
}

func startImageUpload(t *testing.T, e apidserver.TestEnv, slug string, size int64, opts *api.UploadDeployOptions) *httptest.ResponseRecorder {
	t.Helper()
	return e.Do(t, "POST", "/v1/uploads", apidserver.StartUploadRequest{AppSlug: slug, TotalSize: size, DeployOptions: opts}, nil)
}

// uploadAndCommit uploads body as an image archive and returns the commit.
func uploadAndCommit(t *testing.T, e apidserver.TestEnv, slug string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	rec := startImageUpload(t, e, slug, int64(len(body)), imageUploadOptions("local/app:latest"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var started apidserver.StartUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if rec := appendChunk(t, e, started.UploadID, 0, body); rec.Code != http.StatusOK {
		t.Fatalf("append: %d %s", rec.Code, rec.Body.String())
	}
	return commitUpload(e, started.UploadID)
}

func TestUploadImageArchive_CommitsPrebuiltDockerfileDeployment(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setupLocalImage(t, api.PlanFree)
	e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "local-img"}, nil)
	archive, digest := ociarchivetest.Canonical(t, 3000)

	rec := uploadAndCommit(t, e, "local-img", archive)
	if rec.Code != http.StatusCreated {
		t.Fatalf("commit: %d %s", rec.Code, rec.Body.String())
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	dep, err := (*e.Store()).DeploymentByID(t.Context(), out.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dep.Kind != state.DeploymentKindDockerfile || dep.SourceRoot != "" || !strings.HasPrefix(dep.SourceURL, "local-image://upload-session/") {
		t.Fatalf("deployment kind=%s root=%q source_url=%q", dep.Kind, dep.SourceRoot, dep.SourceURL)
	}
	var profile frameworkprofile.Profile
	if err := json.Unmarshal(dep.InferredProfile, &profile); err != nil {
		t.Fatalf("profile: %v (%s)", err, dep.InferredProfile)
	}
	if profile.UploadedImage == nil || profile.UploadedImage.ManifestDigest != digest || profile.UploadedImage.Name != "local/app:latest" ||
		profile.Framework != "docker" || profile.Port != 3000 || !profile.HealthDefaulted {
		t.Fatalf("profile = %+v", profile)
	}
}

func TestUploadImageArchive_Refusals(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setupLocalImage(t, api.PlanFree)
	e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "img"}, nil)
	free := api.MustLimitsFor(api.PlanFree)
	cases := map[string]struct {
		size int64
		opts *api.UploadDeployOptions
		code int
		want string
	}{
		"over app layer cap":          {free.ImageArchiveMaxBytes() + 1, imageUploadOptions(""), http.StatusRequestEntityTooLarge, "uploaded image"},
		"above source cap is allowed": {int64(free.SourceTarballMaxMB)<<20 + 1, imageUploadOptions(""), http.StatusCreated, ""},
		"source build option":         {1024, &api.UploadDeployOptions{SourceFormat: api.UploadSourceFormatOCIImageArchive, Dockerfile: true}, http.StatusBadRequest, "remove dockerfile"},
		"unknown format":              {1024, &api.UploadDeployOptions{SourceFormat: "zip"}, http.StatusBadRequest, "source_format"},
		"name without format":         {1024, &api.UploadDeployOptions{ImageName: "x"}, http.StatusBadRequest, "image_name"},
		"datastore image":             {1024, imageUploadOptions("postgres:16"), http.StatusUnprocessableEntity, "datastore"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := startImageUpload(t, e, "img", tc.size, tc.opts)
			if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("got %d %s, want %d containing %q", rec.Code, rec.Body.String(), tc.code, tc.want)
			}
			if rec.Code == http.StatusCreated {
				var started apidserver.StartUploadResponse
				_ = json.Unmarshal(rec.Body.Bytes(), &started)
				cancelSession(t, e, started.UploadID)
			}
		})
	}
}

func TestUploadImageArchive_CommitRefusesBadArchiveAndSignedApps(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setupLocalImage(t, api.PlanPro) // two apps
	e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "bad-img"}, nil)
	source := apidserver.BuildTestTarGz(t, nil, map[string][]byte{"index.js": []byte("1")})
	if rec := uploadAndCommit(t, e, "bad-img", source); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Invalid image archive") {
		t.Fatalf("source tarball as image: %d %s", rec.Code, rec.Body.String())
	}

	e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "signed-img"}, nil)
	archive, _ := ociarchivetest.Canonical(t, 0)
	rec := startImageUpload(t, e, "signed-img", int64(len(archive)), imageUploadOptions(""))
	var started apidserver.StartUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil || rec.Code != http.StatusCreated {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	appendChunk(t, e, started.UploadID, 0, archive)
	// The policy changes while the bytes upload: commit rechecks it.
	app, err := (*e.Store()).AppBySlug(t.Context(), "signed-img")
	if err != nil {
		t.Fatal(err)
	}
	signed := true
	if _, err := (*e.Store()).UpdateApp(t.Context(), app.ID, state.UpdateAppParams{RequireSigned: &signed, SetRequireSigned: true}); err != nil {
		t.Fatal(err)
	}
	if rec := commitUpload(e, started.UploadID); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "requires signed images") {
		t.Fatalf("signed app commit: %d %s", rec.Code, rec.Body.String())
	}
}
