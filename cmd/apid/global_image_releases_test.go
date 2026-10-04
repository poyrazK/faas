package main

// adr: 480
import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

type releaseRoundTripFunc func(*http.Request) (*http.Response, error)

func (f releaseRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGlobalImageReleaseTwoCellsResponseLossAndResume(t *testing.T) {
	directory := globalDirectoryHarness(t)
	server := httptest.NewServer(directory.h)
	defer server.Close()
	client := api.NewClient(server.URL, directory.key).SetCompletionCache(nil)
	app, err := client.CreateGlobalApp(t.Context(), api.CreateGlobalAppRequest{Slug: "global-release-shop"})
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	stores := map[string]*state.MemStore{}
	apps := map[string]state.App{}
	var dispatches atomic.Int32
	var lost atomic.Bool
	for _, region := range []string{"eu-fsn", "eu-hel"} {
		regional := setup(t, api.PlanPro)
		configureCellDeploymentTest(t, regional.store, regional.s, region)
		local := seedCellDeploymentApp(t, regional.store, regional.acct, "regional-release-shop", uuid.NewString(), region)
		cellServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+regional.key {
				t.Error("cross-region credential")
			}
			if r.Method == http.MethodPut {
				dispatches.Add(1)
			}
			regional.h.ServeHTTP(w, r)
		}))
		defer cellServer.Close()
		base := cellServer.Client().Transport
		directory.s.globalVerifier.Client = &http.Client{Transport: releaseRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			resp, err := base.RoundTrip(r)
			if err == nil && r.Method == http.MethodPut && strings.Contains(r.URL.Path, "image-releases") && lost.CompareAndSwap(false, true) {
				_ = resp.Body.Close()
				return nil, errors.New("response lost after commit " + r.Header.Get("Authorization"))
			}
			return resp, err
		})}
		registerDirectoryCell(t, directory, globaldirectory.Cell{ID: regional.s.controlPlaneCell.ID, Region: region, APIBase: cellServer.URL, Enabled: true})
		if _, err := client.AttachGlobalAppTarget(t.Context(), app.Slug, region, api.AttachGlobalAppTargetRequest{RegionalAppSlug: local.Slug, RegionalAPIKey: regional.key}); err != nil {
			t.Fatal(err)
		}
		keys[region] = regional.key
		stores[region] = regional.store
		apps[region] = local
	}
	id := uuid.NewString()
	req := api.PutGlobalImageReleaseRequest{Image: cellImageRequest().Image, RegionalAPIKeys: keys}
	first, err := client.PutGlobalImageRelease(t.Context(), app.Slug, id, req)
	if err != nil || first.Status != "pending" || first.Generation != 1 || len(first.Targets) != 2 || dispatches.Load() != 2 {
		t.Fatalf("partial: %+v %v calls=%d", first, err, dispatches.Load())
	}
	pending := 0
	resumeKeys := map[string]string{}
	for _, target := range first.Targets {
		if target.Outcome == "pending" {
			pending++
			resumeKeys[target.Region] = keys[target.Region]
			if target.Diagnostic != "regional_unavailable" {
				t.Fatalf("unsanitized diagnostic: %+v", target)
			}
		}
		deployments, err := stores[target.Region].ListDeploymentsForApp(t.Context(), apps[target.Region].ID, 0, 0)
		if err != nil || len(deployments) != 1 {
			t.Fatalf("regional commit: %+v %v", deployments, err)
		}
	}
	if pending != 1 {
		t.Fatalf("pending %d", pending)
	}
	// Rebuild HTTP wiring; each request creates a fresh stateless reconciler.
	restartServer := httptest.NewServer(directory.s.handler())
	defer restartServer.Close()
	client = api.NewClient(restartServer.URL, directory.key).SetCompletionCache(nil)
	req.RegionalAPIKeys = resumeKeys
	resumed, err := client.PutGlobalImageRelease(t.Context(), app.Slug, id, req)
	if err != nil || resumed.Status != "accepted" || resumed.Generation != 1 || dispatches.Load() != 2 {
		t.Fatalf("resume duplicated work: %+v %v calls=%d", resumed, err, dispatches.Load())
	}
	for _, target := range resumed.Targets {
		if target.DeploymentID == nil || target.Outcome != "accepted" || target.Diagnostic != "" {
			t.Fatalf("receipt: %+v", target)
		}
	}
	read, err := client.GetGlobalImageRelease(t.Context(), app.Slug, id)
	if err != nil || read.Status != "accepted" {
		t.Fatalf("GET: %+v %v", read, err)
	}
	req.RegionalAPIKeys = nil
	next, err := client.PutGlobalImageRelease(t.Context(), app.Slug, uuid.NewString(), req)
	if err != nil || next.Generation != 2 || next.Status != "pending" {
		t.Fatalf("next: %+v %v", next, err)
	}
	req.RegionalAPIKeys = keys
	old, err := client.PutGlobalImageRelease(t.Context(), app.Slug, id, req)
	if err != nil || old.Status != "superseded" || dispatches.Load() != 2 {
		t.Fatalf("old request dispatched: %+v %v", old, err)
	}
	rec := directory.do(t, http.MethodGet, "/v1/global-apps/"+app.Slug+"/image-releases/"+id, nil, nil)
	for _, key := range keys {
		if strings.Contains(rec.Body.String(), key) {
			t.Fatal("credential in global receipt")
		}
	}
	events, err := directory.store.ListEvents(t.Context(), directory.acct.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		for _, key := range keys {
			if strings.Contains(string(event.Data), key) {
				t.Fatal("credential in audit")
			}
		}
	}
}

func TestGlobalImageReleaseInputAndAuthorization(t *testing.T) {
	e := globalDirectoryHarness(t)
	app, err := e.s.globalDirectory.CreateApp(t.Context(), e.acct.ID, "global-release-input", 1)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/global-apps/" + app.Slug + "/image-releases/" + uuid.NewString()
	req := api.PutGlobalImageReleaseRequest{Image: cellImageRequest().Image}
	rec := e.do(t, http.MethodPut, path, req, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), api.CodeGlobalImageReleaseNoTargets) {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body)
	}
	for _, body := range []string{`{"image":"latest"}`, `{"image":"` + req.Image + `","regional_api_keys":{"eu-fsn":"bad"}}`, `{"image":"` + req.Image + `","extra":true}`} {
		r := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+e.key)
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("validation: %d %s", w.Code, w.Body)
		}
	}
	other, err := e.store.CreateAccount(t.Context(), "foreign-release@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.globalDirectory.GetRelease(t.Context(), other.ID, app.Slug, uuid.NewString()); !errors.Is(err, globaldirectory.ErrNotFound) {
		t.Fatalf("owner: %v", err)
	}
	readKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"image":"`+req.Image+`"}`))
	r.Header.Set("Authorization", "Bearer "+readKey)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("scope: %d %s", w.Code, w.Body)
	}
}

func TestGlobalImageReleaseLimitMetadata(t *testing.T) {
	for _, bound := range []*globaldirectory.ReleaseLimitError{
		{Kind: "targets", Limit: api.GlobalImageReleaseTargetsMax, Observed: api.GlobalImageReleaseTargetsMax + 1},
		{Kind: "generation", Limit: api.GlobalImageReleaseGenerationMax, Observed: api.GlobalImageReleaseGenerationMax + 1},
	} {
		t.Run(bound.Kind, func(t *testing.T) {
			w := httptest.NewRecorder()
			(&server{}).globalImageReleaseError(w, httptest.NewRequest(http.MethodPut, "/", nil), state.Account{}, fmt.Errorf("allocate: %w", bound))
			var body struct {
				Code     string
				Limit    int64
				Observed int64
				DocsURL  string `json:"docs_url"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusConflict || body.Code != api.CodeGlobalImageReleaseLimit || body.Limit != bound.Limit || body.Observed != bound.Observed || body.DocsURL != "https://gregale.dev/docs/global-app-directory#global-image-releases" {
				t.Fatalf("limit metadata: %d %s", w.Code, w.Body)
			}
		})
	}
}
