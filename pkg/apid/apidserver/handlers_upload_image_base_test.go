package apidserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/ociarchive"
	"github.com/onebox-faas/faas/pkg/ociarchive/ociarchivetest"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// adr: 20261127120000 (amendment 1)

// waitImageBase polls until the app's image base is stored: commit saves it
// in the background.
func waitImageBase(t *testing.T, e apidserver.TestEnv, slug string) api.ImageBase {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := e.Do(t, "GET", "/v1/apps/"+slug+"/image-base", nil, nil)
		if rec.Code == http.StatusOK {
			var base api.ImageBase
			if err := json.Unmarshal(rec.Body.Bytes(), &base); err != nil {
				t.Fatal(err)
			}
			return base
		}
		if rec.Code != http.StatusNotFound || time.Now().After(deadline) {
			t.Fatalf("image base: %d %s", rec.Code, rec.Body.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// thinUploadAndCommit uploads full without the layers base holds and commits
// it with reuse_image_base.
func thinUploadAndCommit(t *testing.T, e apidserver.TestEnv, slug string, full []byte, base api.ImageBase) *httptest.ResponseRecorder {
	t.Helper()
	reuse := map[string]int64{}
	for _, l := range base.Layers {
		reuse[l.Digest] = l.Size
	}
	var thin bytes.Buffer
	if omitted, _, err := ociarchive.Thin(bytes.NewReader(full), &thin, reuse); err != nil || omitted == 0 {
		t.Fatalf("thin: omitted=%d err=%v", omitted, err)
	}
	opts := imageUploadOptions("local/app:latest")
	opts.ReuseImageBase = true
	rec := startImageUpload(t, e, slug, int64(thin.Len()), opts)
	var started apidserver.StartUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &started); err != nil || rec.Code != http.StatusCreated {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	if rec := appendChunk(t, e, started.UploadID, 0, thin.Bytes()); rec.Code != http.StatusOK {
		t.Fatalf("append: %d %s", rec.Code, rec.Body.String())
	}
	return commitUpload(e, started.UploadID)
}

func TestImageBase_ThinRedeployRebuildsFromPreviousImage(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setupLocalImage(t, api.PlanFree)
	e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "img"}, nil)
	if rec := e.Do(t, "GET", "/v1/apps/img/image-base", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("before any image: %d %s", rec.Code, rec.Body.String())
	}

	v1, v1Digest := ociarchivetest.CanonicalLayers(t, "os", "runtime", "app v1")
	if rec := uploadAndCommit(t, e, "img", v1); rec.Code != http.StatusCreated {
		t.Fatalf("first commit: %d %s", rec.Code, rec.Body.String())
	}
	base := waitImageBase(t, e, "img")
	if base.ManifestDigest != v1Digest || len(base.Layers) != 3 {
		t.Fatalf("base = %+v", base)
	}

	v2, v2Digest := ociarchivetest.CanonicalLayers(t, "os", "runtime", "app v2")
	rec := thinUploadAndCommit(t, e, "img", v2, base)
	if rec.Code != http.StatusCreated {
		t.Fatalf("thin commit: %d %s", rec.Code, rec.Body.String())
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	dep, err := (*e.Store()).DeploymentByID(t.Context(), out.ID)
	if err != nil {
		t.Fatal(err)
	}
	var profile frameworkprofile.Profile
	if err := json.Unmarshal(dep.InferredProfile, &profile); err != nil || profile.UploadedImage == nil || profile.UploadedImage.ManifestDigest != v2Digest {
		t.Fatalf("deployed image = %+v (%v)", profile.UploadedImage, err)
	}
	// The rebuilt image becomes the next base.
	deadline := time.Now().Add(10 * time.Second)
	for waitImageBase(t, e, "img").ManifestDigest != v2Digest {
		if time.Now().After(deadline) {
			t.Fatal("the rebuilt image did not replace the base")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestImageBase_ThinUploadRefusedWhenBaseLacksLayers(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setupLocalImage(t, api.PlanPro) // two apps
	e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "img"}, nil)
	e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "other"}, nil)
	v1, _ := ociarchivetest.CanonicalLayers(t, "os", "app v1")
	if rec := uploadAndCommit(t, e, "img", v1); rec.Code != http.StatusCreated {
		t.Fatalf("first commit: %d %s", rec.Code, rec.Body.String())
	}
	base := waitImageBase(t, e, "img")
	v2, _ := ociarchivetest.CanonicalLayers(t, "os", "app v2")
	// Same app only: another app has no base to fill the thin upload from.
	rec := thinUploadAndCommit(t, e, "other", v2, base)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), api.CodeImageBaseChanged) {
		t.Fatalf("thin commit without base: %d %s", rec.Code, rec.Body.String())
	}
}

func TestImageBase_RefusedWhileFlagOffAndForUnknownApp(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setupLocalImage(t, api.PlanFree)
	e.Do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "img"}, nil)
	if rec := e.Do(t, "GET", "/v1/apps/nope/image-base", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown app: %d %s", rec.Code, rec.Body.String())
	}
	(*e.S()).SeedPlatformFlag(platformflags.LocalImageDeploys, platformflags.Definition{})
	if rec := e.Do(t, "GET", "/v1/apps/img/image-base", nil, nil); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Local image deploys unavailable") {
		t.Fatalf("flag off: %d %s", rec.Code, rec.Body.String())
	}
	(*e.S()).EnablePlatformFlagForAll(platformflags.LocalImageDeploys)
	if rec := startImageUpload(t, e, "img", 1024, &api.UploadDeployOptions{ReuseImageBase: true}); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "reuse_image_base") {
		t.Fatalf("reuse without image format: %d %s", rec.Code, rec.Body.String())
	}
}
