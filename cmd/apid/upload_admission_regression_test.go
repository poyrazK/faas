package main

import (
	"archive/tar"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func stageRegressionUpload(t *testing.T, e testEnv, slug string, files map[string][]byte) string {
	t.Helper()
	if rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: slug}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create app: %d %s", rec.Code, rec.Body.String())
	}
	headers := make([]tar.Header, 0, len(files))
	for name := range files {
		headers = append(headers, tar.Header{Name: name})
	}
	raw := buildTestTarGz(t, headers, files)
	session := startSession(t, e, slug, int64(len(raw)))
	if rec := appendChunk(t, e, session.UploadID, 0, raw); rec.Code != http.StatusOK {
		t.Fatalf("append source: %d %s", rec.Code, rec.Body.String())
	}
	return session.UploadID
}

// spec: §11 — resumable source uploads must use the same secret admission
// gate as the direct tarball and multipart deployment routes.
func TestUploadCommitRejectsSourceSecretsBeforeEnqueue(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := setup(t, api.PlanFree)
	id := stageRegressionUpload(t, e, "secret-upload", map[string][]byte{
		"index.js": []byte("console.log('ok')\n"),
		".env":     []byte("STRIPE_SECRET_KEY=" + fakeStripeLiveKey + "\n"),
	})
	rec := e.do(t, http.MethodPost, "/v1/uploads/"+id+"/commit", nil, nil)
	assertProblem(t, rec, http.StatusUnprocessableEntity, api.CodeSecretScanStrict)
	if strings.Contains(rec.Body.String(), fakeStripeLiveKey) {
		t.Fatal("secret rejection exposed the unredacted fixture")
	}
	if _, err := e.store.LatestDeployment(t.Context(), findAppID(t, e, "secret-upload")); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rejected source created a deployment: %v", err)
	}
}

// adr: 054
func TestUploadCommitRechecksSecurityPosture(t *testing.T) {
	for _, policy := range []api.AppSecurityPolicy{api.AppSecurityPolicyEnforce, api.AppSecurityPolicyWarn} {
		t.Run(string(policy), func(t *testing.T) {
			t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
			e := setup(t, api.PlanFree)
			id := stageRegressionUpload(t, e, "posture-upload", map[string][]byte{"index.js": []byte("console.log('ok')\n")})
			// Policy can change while a long-running upload is in progress.
			setSecurityPolicyForTest(t, e, "posture-upload", policy)
			rec := e.do(t, http.MethodPost, "/v1/uploads/"+id+"/commit", nil, nil)
			if policy == api.AppSecurityPolicyWarn {
				if rec.Code != http.StatusCreated {
					t.Fatalf("warn policy blocked upload: %d %s", rec.Code, rec.Body.String())
				}
				return
			}
			assertProblem(t, rec, http.StatusForbidden, api.CodeSecurityPostureBlocked)
			if _, err := e.store.LatestDeployment(t.Context(), findAppID(t, e, "posture-upload")); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("blocked upload created a deployment: %v", err)
			}
		})
	}
}
