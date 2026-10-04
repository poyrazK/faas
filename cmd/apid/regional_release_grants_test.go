package main

// adr: 481
import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func grantHTTP(t *testing.T, e testEnv, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}
func TestRegionalReleaseGrantHTTPIsolationAndSDK(t *testing.T) {
	e, app := cellDeploymentHarness(t)
	regional := httptest.NewServer(e.h)
	defer regional.Close()
	admin := api.NewClient(regional.URL, e.key).SetCompletionCache(nil)
	token, hash, err := api.GenerateRegionalReleaseToken()
	if err != nil {
		t.Fatal(err)
	}
	id, global := uuid.NewString(), uuid.NewString()
	req := api.PutRegionalReleaseGrantRequest{GlobalAppID: global, TokenSHA256: hex.EncodeToString(hash), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	created, err := admin.PutRegionalReleaseGrant(t.Context(), e.s.controlPlaneCell.ID, app.ID, id, req)
	if err != nil || created.GrantID != id || created.Status != "active" {
		t.Fatalf("registration: %+v %v", created, err)
	}
	again, err := admin.PutRegionalReleaseGrant(t.Context(), e.s.controlPlaneCell.ID, app.ID, id, req)
	if err != nil || !again.ExpiresAt.Equal(created.ExpiresAt) {
		t.Fatalf("SDK replay: %+v %v", again, err)
	}
	delegated := api.NewClient(regional.URL, token).SetCompletionCache(nil)
	grant, err := delegated.GetRegionalReleaseGrant(t.Context(), e.s.controlPlaneCell.ID, app.ID, id)
	if err != nil || grant.GlobalAppID != global {
		t.Fatalf("own grant receipt: %+v %v", grant, err)
	}
	proof := grantHTTP(t, e, token, http.MethodGet, "/v1/cell/apps/"+app.Slug+"/binding", nil)
	if proof.Code != http.StatusOK {
		t.Fatalf("proof: %d %s", proof.Code, proof.Body)
	}
	for _, path := range []string{"/v1/account", "/v1/apps", "/v1/apps/" + app.Slug, "/v1/cell/apps/unknown-app/binding"} {
		response := grantHTTP(t, e, token, http.MethodGet, path, nil)
		if response.Code < 400 {
			t.Fatalf("escaped purpose: %s %d %s", path, response.Code, response.Body)
		}
	}
	for _, path := range []string{cellDeploymentURL(e.s.controlPlaneCell.ID, app.ID), "/v1/apps/" + app.Slug + "/deployments"} {
		response := grantHTTP(t, e, token, http.MethodPost, path, cellImageRequest())
		if response.Code != http.StatusForbidden {
			t.Fatalf("ordinary deployment: %d %s", response.Code, response.Body)
		}
	}
	releaseID := uuid.NewString()
	releasePath := cellImageReleaseURL(e.s.controlPlaneCell.ID, app.ID, releaseID)
	body := api.PutCellImageReleaseRequest{Image: cellImageRequest().Image}
	for _, ordering := range []*api.CellImageReleaseOrdering{nil, {GlobalAppID: uuid.NewString(), Generation: 1}} {
		body.Ordering = ordering
		response := grantHTTP(t, e, token, http.MethodPut, releasePath, body)
		if response.Code != http.StatusForbidden {
			t.Fatalf("escaped stream: %d %s", response.Code, response.Body)
		}
	}
	body.Ordering = &api.CellImageReleaseOrdering{GlobalAppID: global, Generation: 1}
	accepted, err := delegated.PutCellImageRelease(t.Context(), e.s.controlPlaneCell.ID, app.ID, releaseID, body)
	if err != nil || accepted.DeploymentID == nil || accepted.Ordering == nil || accepted.Ordering.GlobalAppID != global {
		t.Fatalf("delegated acceptance: %+v %v", accepted, err)
	}
	replay, err := delegated.PutCellImageRelease(t.Context(), e.s.controlPlaneCell.ID, app.ID, releaseID, body)
	if err != nil || !replay.Replayed || replay.DeploymentID == nil || *replay.DeploymentID != *accepted.DeploymentID {
		t.Fatalf("delegated retry: %+v %v", replay, err)
	}
	for _, path := range []string{cellImageReleaseURL(uuid.NewString(), app.ID, releaseID), cellImageReleaseURL(e.s.controlPlaneCell.ID, uuid.NewString(), releaseID)} {
		response := grantHTTP(t, e, token, http.MethodGet, path, nil)
		if response.Code != http.StatusForbidden {
			t.Fatalf("wrong identity: %d %s", response.Code, response.Body)
		}
	}
	grantPath := "/v1/cells/" + e.s.controlPlaneCell.ID + "/apps/" + app.ID + "/release-grants/" + id
	response := grantHTTP(t, e, token, http.MethodPut, grantPath, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("delegation could issue itself: %d %s", response.Code, response.Body)
	}
	sibling := strings.Replace(grantPath, id, uuid.NewString(), 1)
	response = grantHTTP(t, e, token, http.MethodDelete, sibling, nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("sibling revocation: %d %s", response.Code, response.Body)
	}
	revoked, err := delegated.RevokeRegionalReleaseGrant(t.Context(), e.s.controlPlaneCell.ID, app.ID, id)
	if err != nil || revoked.Status != "revoked" {
		t.Fatalf("self-revoke: %+v %v", revoked, err)
	}
	response = grantHTTP(t, e, token, http.MethodGet, releasePath, nil)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), api.CodeAPIKeyRevoked) {
		t.Fatalf("revoked bearer: %d %s", response.Code, response.Body)
	}
	retained, err := admin.PutRegionalReleaseGrant(t.Context(), e.s.controlPlaneCell.ID, app.ID, id, req)
	if err != nil || retained.Status != "revoked" {
		t.Fatalf("late registration revived grant: %+v %v", retained, err)
	}
	events, err := e.store.ListEvents(t.Context(), e.acct.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if strings.Contains(string(event.Data), token) || strings.Contains(string(event.Data), req.TokenSHA256) {
			t.Fatal("grant secret/hash in audit")
		}
	}
}
func TestRegionalReleaseGrantReceiptStreamIsolation(t *testing.T) {
	e, app := cellDeploymentHarness(t)
	id := uuid.NewString()
	path := cellImageReleaseURL(e.s.controlPlaneCell.ID, app.ID, id)
	if response := e.do(t, http.MethodPut, path, api.PutCellImageReleaseRequest{Image: cellImageRequest().Image}, nil); response.Code != http.StatusAccepted {
		t.Fatal(response.Body)
	}
	token, hash, err := api.GenerateRegionalReleaseToken()
	if err != nil {
		t.Fatal(err)
	}
	req := api.PutRegionalReleaseGrantRequest{GlobalAppID: uuid.NewString(), TokenSHA256: hex.EncodeToString(hash), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	grantPath := "/v1/cells/" + e.s.controlPlaneCell.ID + "/apps/" + app.ID + "/release-grants/" + uuid.NewString()
	if response := e.do(t, http.MethodPut, grantPath, req, nil); response.Code != http.StatusOK {
		t.Fatal(response.Body)
	}
	response := grantHTTP(t, e, token, http.MethodGet, path, nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unordered receipt disclosed: %d %s", response.Code, response.Body)
	}
}

type revokeGrantAtAcceptance struct {
	state.Store
	state.RegionalReleaseGrantStore
	state.RegionalImageReleaseStore
	calls int
}

func (s *revokeGrantAtAcceptance) CreateRegionalImageRelease(ctx context.Context, i state.RegionalImageReleaseIntent, d state.Deployment, a *state.OrgActivity) (state.RegionalImageReleaseResult, error) {
	s.calls++
	if _, err := s.RevokeRegionalReleaseGrant(ctx, i.AccountID, i.CellID, i.AppID, i.GrantID); err != nil {
		return state.RegionalImageReleaseResult{}, err
	}
	return s.RegionalImageReleaseStore.CreateRegionalImageRelease(ctx, i, d, a)
}
func TestRegionalReleaseGrantRevokedAfterAuthentication(t *testing.T) {
	e, app := cellDeploymentHarness(t)
	token, hash, err := api.GenerateRegionalReleaseToken()
	if err != nil {
		t.Fatal(err)
	}
	global, id := uuid.NewString(), uuid.NewString()
	grants := state.RegionalReleaseGrantStore(e.store)
	_, err = grants.PutRegionalReleaseGrant(t.Context(), state.RegionalReleaseGrant{ID: id, CellID: e.s.controlPlaneCell.ID, AppID: app.ID, AccountID: e.acct.ID, OrgID: app.OrgID, GlobalAppID: global, Hash: hash, ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := &revokeGrantAtAcceptance{Store: e.store, RegionalReleaseGrantStore: grants, RegionalImageReleaseStore: state.RegionalImageReleaseStore(e.store)}
	e.s.store = wrapped
	e.s.authMw.Authn = storeAsAuthenticator(wrapped)
	e.h = e.s.handler()
	releaseID := uuid.NewString()
	response := grantHTTP(t, e, token, http.MethodPut, cellImageReleaseURL(e.s.controlPlaneCell.ID, app.ID, releaseID), api.PutCellImageReleaseRequest{Image: cellImageRequest().Image, Ordering: &api.CellImageReleaseOrdering{GlobalAppID: global, Generation: 1}})
	if wrapped.calls != 1 || response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), api.CodeAPIKeyRevoked) {
		t.Fatalf("revocation race: calls=%d %d %s", wrapped.calls, response.Code, response.Body)
	}
	if _, err := wrapped.RegionalImageReleaseReceipt(t.Context(), e.s.controlPlaneCell.ID, app.ID, releaseID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("revoked grant wrote intent", err)
	}
}
