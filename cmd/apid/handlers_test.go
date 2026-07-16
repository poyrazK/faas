// Negative-path tests for cmd/apid handlers. The happy paths are covered by
// server_test.go; this file targets the error branches that the matrix
// doesn't hit.

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCreateApp_DuplicateSlug409(t *testing.T) {
	e := setup(t, api.PlanPro)
	if rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dupe"}, nil); rec.Code != 201 {
		t.Fatalf("first create: %d %s", rec.Code, rec.Body)
	}
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dupe"}, nil)
	if rec.Code != 409 {
		t.Errorf("duplicate slug: %d %s", rec.Code, rec.Body)
	}
	assertProblem(t, rec, 409, api.CodeValidation)
}

func TestCreateApp_BadJSONBody(t *testing.T) {
	e := setup(t, api.PlanPro)
	req := newRawRequest(t, "POST", "/v1/apps", "not json {{{", map[string]string{
		"Authorization": "Bearer " + e.key,
	})
	rec := serveRaw(e.h, req)
	if rec.Code != 400 {
		t.Errorf("bad json: %d %s", rec.Code, rec.Body)
	}
}

func TestCreateApp_InvalidType(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "weird", Type: "widget"}, nil)
	if rec.Code != 400 {
		t.Errorf("invalid type: %d %s", rec.Code, rec.Body)
	}
}

func TestCreateApp_FunctionMissingRuntime(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "fn-no-rt", Type: "function"}, nil)
	if rec.Code != 400 {
		t.Errorf("function without runtime: %d %s", rec.Code, rec.Body)
	}
}

func TestCreateApp_FunctionBadRuntime(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps",
		api.CreateAppRequest{Slug: "fn-bad-rt", Type: "function", Runtime: "ruby33"}, nil)
	if rec.Code != 400 {
		t.Errorf("function bad runtime: %d %s", rec.Code, rec.Body)
	}
}

func TestCreateApp_AppliesDefaults(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "defaults-app"}, nil)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.RAMMB != 512 {
		t.Errorf("RAM default = %d, want 512 (pro)", out.RAMMB)
	}
	if out.MaxConcurrency != 1 {
		t.Errorf("MaxConcurrency default = %d, want 1", out.MaxConcurrency)
	}
}

func TestCreateApp_ExplicitRamAndConcur(t *testing.T) {
	e := setup(t, api.PlanScale) // highest limits
	rec := e.do(t, "POST", "/v1/apps",
		api.CreateAppRequest{Slug: "explicit", RAMMB: 256, MaxConcurrency: 4}, nil)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.RAMMB != 256 || out.MaxConcurrency != 4 {
		t.Errorf("explicit values lost: %+v", out)
	}
}

func TestCreateDeployment_AppNotOwned(t *testing.T) {
	// Owner-A creates an app; owner-B tries to deploy to it.
	store := state.NewMemStore()
	acctA, _ := store.CreateAccount(context.Background(), "a@x.com", api.PlanPro)
	_, hashA, _ := api.GenerateAPIKey()
	store.CreateAPIKey(context.Background(), acctA.ID, hashA, "a")

	acctB, _ := store.CreateAccount(context.Background(), "b@x.com", api.PlanPro)
	keyB, hashB, _ := api.GenerateAPIKey()
	store.CreateAPIKey(context.Background(), acctB.ID, hashB, "b")

	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acctA.ID, Slug: "a-app", Status: state.AppActive,
	})

	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.com").handler()
	digest := "sha256:" + repeat("a", 64)

	// B tries to deploy to A's slug.
	body := `{"image":"r/x@` + digest + `"}`
	req := newRawRequest(t, "POST", "/v1/apps/"+app.Slug+"/deployments",
		body,
		map[string]string{"Authorization": "Bearer " + keyB})
	rec := serveRaw(srv, req)
	if rec.Code != 404 {
		t.Errorf("cross-owner deploy should 404, got %d %s", rec.Code, rec.Body)
	}
}

func TestCreateDeployment_BadJSONBody(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dep-app"}, nil)
	req := newRawRequest(t, "POST", "/v1/apps/dep-app/deployments", "{not json",
		map[string]string{"Authorization": "Bearer " + e.key})
	rec := serveRaw(e.h, req)
	if rec.Code != 400 {
		t.Errorf("bad json deploy: %d %s", rec.Code, rec.Body)
	}
}

func TestCreateDeployment_ImageNoShaPrefix(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dep-app"}, nil)
	rec := e.do(t, "POST", "/v1/apps/dep-app/deployments",
		api.CreateDeploymentRequest{Image: "registry.x/no-sha-prefix"}, nil)
	if rec.Code != 400 {
		t.Errorf("image without @sha256: should 400, got %d %s", rec.Code, rec.Body)
	}
}

func TestCreateDeployment_ImageBadDigest(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "dep-app"}, nil)
	// Has @sha256: but the digest part is the wrong length / format.
	rec := e.do(t, "POST", "/v1/apps/dep-app/deployments",
		api.CreateDeploymentRequest{Image: "r/x@sha256:short"}, nil)
	if rec.Code != 400 {
		t.Errorf("image with short digest should 400, got %d %s", rec.Code, rec.Body)
	}
}

func TestParseImageDigest_PureUnit(t *testing.T) {
	cases := map[string]bool{
		"r/x@sha256:" + strings.Repeat("a", 64): true,
		"r/x@sha256:" + strings.Repeat("a", 63): false,
		"r/x@sha256:" + strings.Repeat("A", 64): false, // uppercase rejected
		"r/x:latest":                            false,
		"":                                      false,
	}
	for in, want := range cases {
		_, ok := parseImageDigest(in)
		if ok != want {
			t.Errorf("parseImageDigest(%q) ok=%v want=%v", in, ok, want)
		}
	}
}

// helpers ---------------------------------------------------------------------

func newRawRequest(t *testing.T, method, path, body string, hdrs map[string]string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	return req
}

func serveRaw(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
