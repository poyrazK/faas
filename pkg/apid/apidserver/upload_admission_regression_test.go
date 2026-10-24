package apidserver_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func stageRegressionUpload(t *testing.T, e apidserver.TestEnv, slug string, files map[string][]byte) string {
	t.Helper()
	if rec := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: slug}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", rec.Code, rec.Body.String())
	}
	headers := make([]tar.Header, 0, len(files))
	for name := range files {
		headers = append(headers, tar.Header{Name: name})
	}
	raw := apidserver.BuildTestTarGz(t, headers, files)
	session := startSession(t, e, slug, int64(len(raw)))
	if rec := appendChunk(t, e, session.UploadID, 0, raw); rec.Code != http.StatusOK {
		t.Fatalf("append source: %d %s", rec.Code, rec.Body.String())
	}
	return session.UploadID
}

// spec: §11 — the shape check refuses an archive by its declared sizes before
// anything inflates it. With source_root set, the commit ran the source_root
// presence scan first: three passes that inflate every entry body, so a small
// compression bomb was decompressed in full on every commit retry.
func TestUploadCommitChecksShapeBeforeSourceRootScan(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := apidserver.Setup(t, api.PlanFree)
	if rec := e.Do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "bomb-upload"}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", rec.Code, rec.Body.String())
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: "service/huge.bin", Mode: 0o644, Typeflag: tar.TypeReg,
		Size: api.SourceArchiveMaxExpandedBytes + 1}); err != nil {
		t.Fatal(err)
	}
	// Stop after the header: its declared size alone must be refused, and
	// any pass that tries to read the body fails differently.
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	start := e.Do(t, http.MethodPost, "/v1/uploads", apidserver.StartUploadRequest{AppSlug: "bomb-upload", TotalSize: int64(buf.Len()),
		DeployOptions: &api.UploadDeployOptions{SourceRoot: "service"}}, nil)
	if start.Code != http.StatusCreated {
		t.Fatalf("start: %d %s", start.Code, start.Body.String())
	}
	var session apidserver.StartUploadResponse
	if err := json.Unmarshal(start.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if rec := appendChunk(t, e, session.UploadID, 0, buf.Bytes()); rec.Code != http.StatusOK {
		t.Fatalf("append: %d %s", rec.Code, rec.Body.String())
	}
	rec := e.Do(t, http.MethodPost, "/v1/uploads/"+session.UploadID+"/commit", nil, nil)
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeSourceInvalid)
	if !strings.Contains(rec.Body.String(), "source expands past") {
		t.Fatalf("commit did not refuse on the declared size first: %s", rec.Body.String())
	}
}

// spec: §11 — resumable source uploads must use the same secret admission
// gate as the direct tarball and multipart deployment routes.
func TestUploadCommitRejectsSourceSecretsBeforeEnqueue(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := apidserver.Setup(t, api.PlanFree)
	id := stageRegressionUpload(t, e, "secret-upload", map[string][]byte{
		"index.js": []byte("console.log('ok')\n"),
		".env":     []byte("STRIPE_SECRET_KEY=" + fakeStripeLiveKey + "\n"),
	})
	rec := e.Do(t, http.MethodPost, "/v1/uploads/"+id+"/commit", nil, nil)
	apidserver.AssertProblem(t, rec, http.StatusUnprocessableEntity, api.CodeSecretScanStrict)
	if strings.Contains(rec.Body.String(), fakeStripeLiveKey) {
		t.Fatal("secret rejection exposed the unredacted fixture")
	}
	if _, err := (*e.Store()).LatestDeployment(t.Context(), findAppID(t, e, "secret-upload")); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rejected source created a deployment: %v", err)
	}
}

// adr: 054
func TestUploadCommitRechecksSecurityPosture(t *testing.T) {
	for _, policy := range []api.AppSecurityPolicy{api.AppSecurityPolicyEnforce, api.AppSecurityPolicyWarn} {
		t.Run(string(policy), func(t *testing.T) {
			t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
			e := apidserver.Setup(t, api.PlanFree)
			id := stageRegressionUpload(t, e, "posture-upload", map[string][]byte{"index.js": []byte("console.log('ok')\n")})
			// Policy can change while a long-running upload is in progress.
			setSecurityPolicyForTest(t, e, "posture-upload", policy)
			rec := e.Do(t, http.MethodPost, "/v1/uploads/"+id+"/commit", nil, nil)
			if policy == api.AppSecurityPolicyWarn {
				if rec.Code != http.StatusCreated {
					t.Fatalf("warn policy blocked upload: %d %s", rec.Code, rec.Body.String())
				}
				return
			}
			apidserver.AssertProblem(t, rec, http.StatusForbidden, api.CodeSecurityPostureBlocked)
			if _, err := (*e.Store()).LatestDeployment(t.Context(), findAppID(t, e, "posture-upload")); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("blocked upload created a deployment: %v", err)
			}
		})
	}
}
