package apidserver_test

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func uploadSourceArchive(t *testing.T) []byte {
	t.Helper()
	return apidserver.BuildTestTarGz(t, []tar.Header{{Name: "index.js"}}, map[string][]byte{"index.js": []byte("console.log('ok')\n")})
}

// startUploadAs opens a session with explicit headers and returns the
// response; the session ID is empty unless it was created.
func startUploadAs(t *testing.T, e apidserver.TestEnv, slug string, size int, headers map[string]string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	rec := e.Do(t, http.MethodPost, "/v1/uploads", apidserver.StartUploadRequest{AppSlug: slug, TotalSize: int64(size)}, headers)
	if rec.Code != http.StatusCreated {
		return rec, ""
	}
	var session apidserver.StartUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	return rec, session.UploadID
}

func appendChunkAs(t *testing.T, e apidserver.TestEnv, id string, body []byte, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/v1/uploads/"+id, bytes.NewReader(body))
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Upload-Offset", strconv.Itoa(0))
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	return rec
}

// Deploy tokens were refused (403) on the resumable upload routes, which only
// accepted /v1/apps/* paths, and the CLI falls back only on 404: `gregale
// deploy` with a deploy token failed. A token may now use them for its own
// app, and no other: the binding is enforced once the handler resolves the
// app from the body or the session.
func TestUploadRoutesAcceptAppBoundDeployToken(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "token-upload-a")
	apidserver.MustSeedApp(t, e, "token-upload-b")
	minted := e.Do(t, http.MethodPost, "/v1/apps/token-upload-a/deploy-tokens", api.CreateDeployTokenRequest{Label: "ci"}, nil)
	if minted.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", minted.Code, minted.Body.String())
	}
	var token api.DeployTokenResponse
	if err := json.Unmarshal(minted.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	bearer := map[string]string{"Authorization": "Bearer " + token.Plaintext}
	raw := uploadSourceArchive(t)

	if rec, _ := startUploadAs(t, e, "token-upload-b", len(raw), bearer); rec.Code != http.StatusNotFound {
		t.Fatalf("token opened a session on another app: %d %s", rec.Code, rec.Body.String())
	}
	rec, own := startUploadAs(t, e, "token-upload-a", len(raw), bearer)
	if own == "" {
		t.Fatalf("token could not open a session on its app: %d %s", rec.Code, rec.Body.String())
	}
	if rec := appendChunkAs(t, e, own, raw, bearer["Authorization"]); rec.Code != http.StatusOK {
		t.Fatalf("token append: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.Do(t, http.MethodGet, "/v1/uploads/"+own, nil, bearer); rec.Code != http.StatusOK {
		t.Fatalf("token read: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.Do(t, http.MethodPost, "/v1/uploads/"+own+"/commit", nil, bearer); rec.Code != http.StatusCreated {
		t.Fatalf("token commit: %d %s", rec.Code, rec.Body.String())
	}

	// A session of another app in the same account stays out of reach.
	_, other := startUploadAs(t, e, "token-upload-b", len(raw), nil)
	if other == "" {
		t.Fatal("account key could not open a session on app B")
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/v1/uploads/" + other},
		{http.MethodPost, "/v1/uploads/" + other + "/commit"},
		{http.MethodDelete, "/v1/uploads/" + other},
	} {
		if rec := e.Do(t, tc.method, tc.path, nil, bearer); rec.Code != http.StatusNotFound {
			t.Fatalf("token %s %s = %d, want 404 (%s)", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	if rec := appendChunkAs(t, e, other, raw, bearer["Authorization"]); rec.Code != http.StatusNotFound {
		t.Fatalf("token appended to another app's session: %d %s", rec.Code, rec.Body.String())
	}
}

// Organization collaborators who had not created the app got 404 from the
// upload routes, and the CLI quietly fell back to the legacy endpoint. They
// now get the same access the /v1/apps deployment routes give them: a
// developer deploys and is recorded as the actor, a viewer is refused, and a
// removed member or another organization's member cannot reach the app.
func TestSharedAppCollaboratorsUseUploadRoutes(t *testing.T) {
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	e := apidserver.Setup(t, api.PlanPro)
	org := apidserver.SeedSharedOrgWithOwner(t, e, "team-upload-access", "Team Upload Access", api.PlanPro)
	if rec := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "team-upload-api"}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	store := *e.Store()
	developer, developerKey := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, viewerKey := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	raw := uploadSourceArchive(t)

	rec, session := startUploadAs(t, e, "team-upload-api", len(raw), developerKey)
	if session == "" {
		t.Fatalf("developer could not open an upload: %d %s", rec.Code, rec.Body.String())
	}
	if rec := appendChunkAs(t, e, session, raw, developerKey["Authorization"]); rec.Code != http.StatusOK {
		t.Fatalf("developer append: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.Do(t, http.MethodGet, "/v1/uploads/"+session, nil, viewerKey); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer read a deploy session: %d %s", rec.Code, rec.Body.String())
	}
	committed := e.Do(t, http.MethodPost, "/v1/uploads/"+session+"/commit", nil, developerKey)
	if committed.Code != http.StatusCreated {
		t.Fatalf("developer commit: %d %s", committed.Code, committed.Body.String())
	}
	var deployment api.DeploymentResponse
	if err := json.Unmarshal(committed.Body.Bytes(), &deployment); err != nil {
		t.Fatal(err)
	}
	stored, err := store.DeploymentByID(t.Context(), deployment.ID)
	if err != nil || stored.DeployedByUserID != developer.ID {
		t.Fatalf("deployment actor = %q, want the developer %q (err=%v)", stored.DeployedByUserID, developer.ID, err)
	}

	if rec, _ := startUploadAs(t, e, "team-upload-api", len(raw), viewerKey); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer opened an upload: %d %s", rec.Code, rec.Body.String())
	}
	if rec, id := startUploadAs(t, e, "team-upload-api", len(raw), nil); id == "" {
		t.Fatalf("creator could not open an upload: %d %s", rec.Code, rec.Body.String())
	}
	foreignOrg := apidserver.SeedSharedOrgWithOwner(t, e, "foreign-upload-access", "Foreign Upload Access", api.PlanPro)
	_, foreignKey := sharedAppMember(t, store, foreignOrg, state.OrgRoleAdmin, api.ScopesAdminOnly)
	if rec, _ := startUploadAs(t, e, "team-upload-api", len(raw), foreignKey); rec.Code != http.StatusNotFound {
		t.Fatalf("another organization's member opened an upload: %d %s", rec.Code, rec.Body.String())
	}
	if err := store.RemoveOrgMember(t.Context(), org.ID, developer.ID); err != nil {
		t.Fatal(err)
	}
	if rec, id := startUploadAs(t, e, "team-upload-api", len(raw), developerKey); id != "" || rec.Code < 400 {
		t.Fatalf("removed member opened an upload: %d %s", rec.Code, rec.Body.String())
	}
}
