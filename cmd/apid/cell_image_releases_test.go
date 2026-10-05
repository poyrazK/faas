package main

// adr: 604

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func cellImageReleaseURL(cellID, appID, releaseID string) string {
	return "/v1/cells/" + cellID + "/apps/" + appID + "/image-releases/" + releaseID
}

func decodeCellImageRelease(t *testing.T, rec *httptest.ResponseRecorder) api.CellImageReleaseResponse {
	t.Helper()
	if rec.Code != http.StatusAccepted && rec.Code != http.StatusOK {
		t.Fatalf("receipt: %d %s", rec.Code, rec.Body)
	}
	var out api.CellImageReleaseResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCellImageReleaseLostResponseAndStaleCache(t *testing.T) {
	e, app := cellDeploymentHarness(t)
	releaseID := uuid.NewString()
	path := cellImageReleaseURL(e.s.controlPlaneCell.ID, app.ID, releaseID)
	body := api.PutCellImageReleaseRequest{Image: cellImageRequest().Image}
	first := decodeCellImageRelease(t, e.do(t, http.MethodPut, path, body, nil))
	if first.Replayed || first.DeploymentID == nil || first.ReleaseID != releaseID || first.RegionalAppID != app.ID || first.RegionalAccountID != uuid.MustParse(e.acct.ID).String() {
		t.Fatalf("acceptance: %+v", first)
	}
	// Simulate a lost response and an unrelated stale ordinary replay entry.
	if err := e.store.PutIdempotent(t.Context(), e.acct.ID, "PUT "+path+"\nold-cache", http.StatusAccepted, []byte(`{"deployment_id":"wrong"}`)); err != nil {
		t.Fatal(err)
	}
	newer, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, ImageDigest: body.Image, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodPut, path, body, map[string]string{"Idempotency-Key": "old-cache"})
	retry := decodeCellImageRelease(t, rec)
	if !retry.Replayed || retry.DeploymentID == nil || *retry.DeploymentID != *first.DeploymentID || retry.DeploymentStatus != string(state.DeploySuperseded) || rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("durable replay: %+v", retry)
	}
	current, err := e.store.DeploymentByID(t.Context(), newer.ID)
	if err != nil || current.Status != state.DeployPending {
		t.Fatalf("newer release changed: %+v %v", current, err)
	}
	read := decodeCellImageRelease(t, e.do(t, http.MethodGet, path, nil, nil))
	if read.Replayed || read.DeploymentID == nil || *read.DeploymentID != *first.DeploymentID {
		t.Fatalf("read: %+v", read)
	}
	body.Image = strings.ReplaceAll(body.Image, strings.Repeat("a", 64), strings.Repeat("b", 64))
	conflict := e.do(t, http.MethodPut, path, body, map[string]string{"Idempotency-Key": "old-cache"})
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), api.CodeRegionalImageReleaseConflict) {
		t.Fatalf("conflicting body hidden by replay: %d %s", conflict.Code, conflict.Body)
	}
}

func TestCellImageReleaseConcurrentSubmissions(t *testing.T) {
	e, app := cellDeploymentHarness(t)
	path := cellImageReleaseURL(e.s.controlPlaneCell.ID, app.ID, uuid.NewString())
	const count = 12
	results := make(chan *httptest.ResponseRecorder, count)
	var wg sync.WaitGroup
	for range count {
		wg.Go(func() {
			results <- e.do(t, http.MethodPut, path, api.PutCellImageReleaseRequest{Image: cellImageRequest().Image}, nil)
		})
	}
	wg.Wait()
	close(results)
	id := ""
	accepted := 0
	for rec := range results {
		out := decodeCellImageRelease(t, rec)
		if out.DeploymentID == nil {
			t.Fatal("missing deployment")
		}
		if id == "" {
			id = *out.DeploymentID
		}
		if id != *out.DeploymentID {
			t.Fatal("concurrent releases diverged")
		}
		if !out.Replayed {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d times", accepted)
	}
	rows, err := e.store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("concurrent intent: %d %v", len(rows), err)
	}
}

func TestCellImageReleaseValidationAndAuthBeforeReplay(t *testing.T) {
	e, app := cellDeploymentHarness(t)
	path := cellImageReleaseURL(e.s.controlPlaneCell.ID, app.ID, uuid.NewString())
	for _, raw := range []string{`null`, `{"image":"shop:latest"}`, `{"image":"` + cellImageRequest().Image + `","overrides":{}}`, `{"image":"` + cellImageRequest().Image + `"}{}`, strings.Repeat(" ", api.CellImageReleaseRequestMaxBytes+1)} {
		req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+e.key)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid body: %d %s", rec.Code, rec.Body)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		bad := cellImageReleaseURL(e.s.controlPlaneCell.ID, app.ID, uuid.Nil.String())
		rec := e.do(t, method, bad, api.PutCellImageReleaseRequest{Image: cellImageRequest().Image}, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad release: %d %s", rec.Code, rec.Body)
		}
	}
	first := e.do(t, http.MethodPut, path, api.PutCellImageReleaseRequest{Image: cellImageRequest().Image}, nil)
	decodeCellImageRelease(t, first)
	unverified := e
	unverified.s = newServer(cellUnverifiedAuthn{e.store}, e.s.log, "gregale.dev", noopNotifier{})
	unverified.s.controlPlaneCell = e.s.controlPlaneCell
	unverified.h = unverified.s.handler()
	for _, method := range []string{http.MethodPut, http.MethodGet} {
		rec := unverified.do(t, method, path, api.PutCellImageReleaseRequest{Image: cellImageRequest().Image}, nil)
		if rec.Code != http.StatusForbidden || rec.Header().Get("Idempotent-Replayed") != "" {
			t.Fatalf("unverified replay: %d %s", rec.Code, rec.Body)
		}
	}
	if err := e.store.DeleteApp(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodPut, http.MethodGet} {
		rec := e.do(t, method, path, api.PutCellImageReleaseRequest{Image: cellImageRequest().Image}, nil)
		if rec.Code != http.StatusNotFound || rec.Header().Get("Idempotent-Replayed") != "" {
			t.Fatalf("deleted identity replay: %d %s", rec.Code, rec.Body)
		}
	}
}

func TestCellImageReleaseTwoCellsSDK(t *testing.T) {
	releaseID, sharedAppID := uuid.NewString(), uuid.NewString()
	for _, region := range []string{"eu-fsn", "eu-hel"} {
		e := setup(t, api.PlanPro)
		configureCellDeploymentTest(t, e.store, e.s, region)
		app := seedCellDeploymentApp(t, e.store, e.acct, "shop", sharedAppID, region)
		cell := httptest.NewTLSServer(e.h)
		defer cell.Close()
		client := api.NewClient(cell.URL, e.key).SetCompletionCache(nil)
		client.HTTPClient().Transport = cell.Client().Transport
		first, err := client.PutCellImageRelease(t.Context(), e.s.controlPlaneCell.ID, app.ID, releaseID, api.PutCellImageReleaseRequest{Image: cellImageRequest().Image})
		if err != nil || first.Region != region || first.CellID != e.s.controlPlaneCell.ID {
			t.Fatalf("regional acceptance: %+v %v", first, err)
		}
		retry, err := client.PutCellImageRelease(t.Context(), e.s.controlPlaneCell.ID, app.ID, releaseID, api.PutCellImageReleaseRequest{Image: cellImageRequest().Image})
		if err != nil || !retry.Replayed || retry.DeploymentID == nil || *retry.DeploymentID != *first.DeploymentID {
			t.Fatalf("regional retry: %+v %v", retry, err)
		}
		read, err := client.GetCellImageRelease(t.Context(), e.s.controlPlaneCell.ID, app.ID, releaseID)
		if err != nil || read.ReleaseID != releaseID || read.DeploymentID == nil {
			t.Fatalf("regional receipt: %+v %v", read, err)
		}
	}
}

// This wrapper keeps the optional receipt capability while pausing immutable
// app lookup; it never allows the deployment path to resolve a slug.
type cellImageReleaseGuardStore struct {
	*cellDeploymentGuardStore
	releases state.RegionalImageReleaseStore
}

func (g cellImageReleaseGuardStore) RegionalImageReleaseReceipt(ctx context.Context, c, a, r string) (state.RegionalImageReleaseReceipt, error) {
	return g.releases.RegionalImageReleaseReceipt(ctx, c, a, r)
}
func (g cellImageReleaseGuardStore) CreateRegionalImageRelease(ctx context.Context, i state.RegionalImageReleaseIntent, d state.Deployment, a *state.OrgActivity) (state.RegionalImageReleaseResult, error) {
	return g.releases.CreateRegionalImageRelease(ctx, i, d, a)
}

func TestCellImageReleaseReadScopes(t *testing.T) {
	for _, scope := range []string{api.ScopeAdmin, api.ScopeAppsRead, api.ScopeDeployWrite} {
		t.Run(scope, func(t *testing.T) {
			e, app := cellDeploymentHarness(t)
			path := cellImageReleaseURL(e.s.controlPlaneCell.ID, app.ID, uuid.NewString())
			decodeCellImageRelease(t, e.do(t, http.MethodPut, path, api.PutCellImageReleaseRequest{Image: cellImageRequest().Image}, nil))
			token, hash, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			key, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "receipt-read", []string{scope})
			if err != nil {
				t.Fatal(err)
			}
			e.key = token
			decodeCellImageRelease(t, e.do(t, http.MethodGet, path, nil, nil))
			if scope == api.ScopeAppsRead {
				rec := e.do(t, http.MethodPut, path, api.PutCellImageReleaseRequest{Image: cellImageRequest().Image}, nil)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("read key mutation: %d %s", rec.Code, rec.Body)
				}
			}
			if _, err := e.store.MarkAPIKeyRevoked(t.Context(), e.acct.ID, key.ID); err != nil {
				t.Fatal(err)
			}
			rec := e.do(t, http.MethodGet, path, nil, nil)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("revoked key: %d %s", rec.Code, rec.Body)
			}
		})
	}
}

var _ state.RegionalImageReleaseStore = cellImageReleaseGuardStore{}

func runCellImageReleaseLookupRace(t *testing.T, store state.Store, srv *server, account state.Account, key string, deleteApp bool) {
	t.Helper()
	app := seedCellDeploymentApp(t, store, account, "lookup-race", uuid.NewString(), "eu-fsn")
	guard := cellImageReleaseGuardStore{cellDeploymentGuardStore: &cellDeploymentGuardStore{Store: store, read: make(chan struct{}), resume: make(chan struct{})}, releases: store.(state.RegionalImageReleaseStore)}
	srv.store, srv.authMw.Authn = guard, guard
	handler := srv.handler()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, cellImageReleaseURL(srv.controlPlaneCell.ID, app.ID, uuid.NewString()), strings.NewReader(`{"image":"`+cellImageRequest().Image+`"}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); handler.ServeHTTP(rec, req) }()
	select {
	case <-guard.read:
	case <-time.After(10 * time.Second):
		t.Fatal("identity lookup did not pause")
	}
	if _, err := store.RenameApp(t.Context(), account.ID, app.Slug, "retired-"+app.ID[:8]); err != nil {
		close(guard.resume)
		<-done
		t.Fatal(err)
	}
	if deleteApp {
		if err := store.DeleteApp(t.Context(), app.ID); err != nil {
			close(guard.resume)
			<-done
			t.Fatal(err)
		}
	}
	replacement := seedCellDeploymentApp(t, store, account, app.Slug, uuid.NewString(), "eu-fsn")
	close(guard.resume)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("paused request did not finish")
	}
	status := http.StatusAccepted
	if deleteApp {
		status = http.StatusNotFound
	}
	if rec.Code != status || guard.slugCalls.Load() != 0 {
		t.Fatalf("lookup race: %d slug reads=%d %s", rec.Code, guard.slugCalls.Load(), rec.Body)
	}
	rows, err := store.ListDeploymentsForApp(t.Context(), replacement.ID, 0, 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("replacement received stale release: %d %v", len(rows), err)
	}
	rows, err = store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
	want := 1
	if deleteApp {
		want = 0
	}
	if err != nil || len(rows) != want {
		t.Fatalf("original received incorrect intent: %d %v", len(rows), err)
	}
}

func TestCellImageReleaseLookupRaces(t *testing.T) {
	for _, deleteApp := range []bool{false, true} {
		name := "rename"
		if deleteApp {
			name = "delete"
		}
		t.Run(name, func(t *testing.T) {
			e, _ := cellDeploymentHarness(t)
			runCellImageReleaseLookupRace(t, e.store, e.s, e.acct, e.key, deleteApp)
		})
	}
}
