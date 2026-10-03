package main

// adr: 477

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func cellDeploymentHarness(t *testing.T) (testEnv, state.App) {
	t.Helper()
	e := setup(t, api.PlanPro)
	configureCellDeploymentTest(t, e.store, e.s, "eu-fsn")
	app := seedCellDeploymentApp(t, e.store, e.acct, "regional-shop", uuid.NewString(), "eu-fsn")
	e.h = e.s.handler()
	return e, app
}

func configureCellDeploymentTest(t *testing.T, store state.Store, srv *server, region string) {
	t.Helper()
	srv.controlPlaneCell = db.CellIdentity{ID: uuid.NewString(), Region: region}
	if _, err := store.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: region, DisplayName: region, AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateComputeNode(t.Context(), state.ComputeNode{
		Name: region, TargetURL: "unix:///regional", Active: true, Region: &region,
		VPCPUs: 4, MemMB: 8192, AdmissionCeilingMB: 1024, VCPUBudget: 160, MaxConcurrency: 8,
	}); err != nil {
		t.Fatal(err)
	}
}

func seedCellDeploymentApp(t *testing.T, store state.Store, acct state.Account, slug, id, region string) state.App {
	t.Helper()
	app, err := store.CreateApp(t.Context(), state.App{ID: id, AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp, Status: state.AppActive, ComputeRegion: region, RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func cellDeploymentURL(cellID, appID string) string {
	return "/v1/cells/" + cellID + "/apps/" + appID + "/deployments"
}

func cellImageRequest() api.CreateDeploymentRequest {
	return api.CreateDeploymentRequest{Image: "ghcr.io/gregale/shop@sha256:" + strings.Repeat("a", 64)}
}

func TestCellDeploymentDirectoryToTwoCellsSDK(t *testing.T) {
	directory := globalDirectoryHarness(t)
	globalServer := httptest.NewServer(directory.h)
	defer globalServer.Close()
	client := api.NewClient(globalServer.URL, directory.key).SetCompletionCache(nil)
	globalApp, err := client.CreateGlobalApp(t.Context(), api.CreateGlobalAppRequest{Slug: "global-shop"})
	if err != nil {
		t.Fatal(err)
	}
	sharedAppID := uuid.NewString() // Even identical app UUIDs must be disambiguated by the cell.
	for _, region := range []string{"eu-fsn", "eu-hel"} {
		regional := setup(t, api.PlanPro)
		configureCellDeploymentTest(t, regional.store, regional.s, region)
		app := seedCellDeploymentApp(t, regional.store, regional.acct, "regional-shop", sharedAppID, region)
		cell := httptest.NewTLSServer(regional.h)
		defer cell.Close()
		directory.s.globalVerifier.Client = cell.Client()
		registerDirectoryCell(t, directory, globaldirectory.Cell{ID: regional.s.controlPlaneCell.ID, Region: region, APIBase: cell.URL, Enabled: true})
		if _, err := client.AttachGlobalAppTarget(t.Context(), globalApp.Slug, region, api.AttachGlobalAppTargetRequest{RegionalAppSlug: app.Slug, RegionalAPIKey: regional.key}); err != nil {
			t.Fatal(err)
		}
		target, err := client.ResolveGlobalApp(t.Context(), globalApp.Slug, region)
		if err != nil {
			t.Fatal(err)
		}
		regionalClient := api.NewClient(target.APIBase, regional.key).SetCompletionCache(nil)
		regionalClient.HTTPClient().Transport = cell.Client().Transport
		ctx := api.ContextWithIdempotencyKey(t.Context(), "global-release-1")
		dep, err := regionalClient.DeployCellApp(ctx, target.CellID, target.RegionalAppID, cellImageRequest())
		if err != nil || dep.AppID != sharedAppID || dep.Status != string(state.DeployPending) {
			t.Fatalf("regional acceptance: %+v %v", dep, err)
		}
		replayed, err := regionalClient.DeployCellApp(ctx, target.CellID, target.RegionalAppID, cellImageRequest())
		if err != nil || replayed.ID != dep.ID {
			t.Fatalf("regional retry: %+v %v", replayed, err)
		}
		wrongClient := api.NewClient(target.APIBase, directory.key).SetCompletionCache(nil)
		wrongClient.HTTPClient().Transport = cell.Client().Transport
		_, err = wrongClient.DeployCellApp(t.Context(), target.CellID, target.RegionalAppID, cellImageRequest())
		assertGlobalAPIError(t, err, api.CodeUnauthorized)
		deps, err := regional.store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
		if err != nil || len(deps) != 1 {
			t.Fatalf("retry inserted duplicate intent: %d %v", len(deps), err)
		}
	}
}

func TestCellDeploymentIdentityAndOwnership(t *testing.T) {
	e, app := cellDeploymentHarness(t)
	foreign, err := e.store.CreateAccount(t.Context(), "foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreignApp := seedCellDeploymentApp(t, e.store, foreign, "foreign-shop", uuid.NewString(), "eu-fsn")
	for _, tc := range []struct {
		name, cellID, appID string
		status              int
	}{
		{"wrong-cell", uuid.NewString(), app.ID, http.StatusNotFound},
		{"foreign-app", e.s.controlPlaneCell.ID, foreignApp.ID, http.StatusNotFound},
		{"missing-app", e.s.controlPlaneCell.ID, uuid.NewString(), http.StatusNotFound},
		{"zero-cell", uuid.Nil.String(), app.ID, http.StatusBadRequest},
		{"compact-app", e.s.controlPlaneCell.ID, strings.ReplaceAll(app.ID, "-", ""), http.StatusBadRequest},
		{"uppercase-app", e.s.controlPlaneCell.ID, "AD6B5B5F-08B6-4C6C-9391-5CC874BAE39B", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.do(t, http.MethodPost, cellDeploymentURL(tc.cellID, tc.appID), cellImageRequest(), nil)
			if rec.Code != tc.status {
				t.Fatalf("identity: %d %s", rec.Code, rec.Body)
			}
		})
	}
	e.s.controlPlaneCell = db.CellIdentity{}
	if rec := e.do(t, http.MethodPost, cellDeploymentURL(uuid.NewString(), app.ID), cellImageRequest(), nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unbound: %d %s", rec.Code, rec.Body)
	}
	for _, id := range []string{app.ID, foreignApp.ID} {
		deps, err := e.store.ListDeploymentsForApp(t.Context(), id, 0, 0)
		if err != nil || len(deps) != 0 {
			t.Fatal("denied identity created work", err)
		}
	}
}

func TestCellDeploymentReplayRejectsDeletionAndIsolatesTargets(t *testing.T) {
	e, app := cellDeploymentHarness(t)
	headers := map[string]string{"Idempotency-Key": "release-1"}
	path := cellDeploymentURL(e.s.controlPlaneCell.ID, app.ID)
	first := e.do(t, http.MethodPost, path, cellImageRequest(), headers)
	if first.Code != http.StatusAccepted {
		t.Fatalf("initial: %d %s", first.Code, first.Body)
	}
	second := e.do(t, http.MethodPost, path, cellImageRequest(), headers)
	if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) || second.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatal("same tuple did not replay")
	}
	cellID := e.s.controlPlaneCell.ID
	e.s.controlPlaneCell.ID = uuid.NewString()
	wrongCell := e.do(t, http.MethodPost, path, cellImageRequest(), headers)
	if wrongCell.Code != http.StatusNotFound || wrongCell.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("success replayed in a foreign cell: %d %s", wrongCell.Code, wrongCell.Body)
	}
	e.s.controlPlaneCell.ID = cellID
	if _, err := e.store.RenameApp(t.Context(), e.acct.ID, app.Slug, "retired-"+app.ID[:8]); err != nil {
		t.Fatal(err)
	}
	if err := e.store.DeleteApp(t.Context(), app.ID); err != nil {
		t.Fatal(err)
	}
	replacement := seedCellDeploymentApp(t, e.store, e.acct, app.Slug, uuid.NewString(), "eu-fsn")
	rejected := e.do(t, http.MethodPost, path, cellImageRequest(), headers)
	if rejected.Code != http.StatusNotFound || rejected.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("stale replay: %d %s", rejected.Code, rejected.Body)
	}
	fresh := e.do(t, http.MethodPost, cellDeploymentURL(e.s.controlPlaneCell.ID, replacement.ID), cellImageRequest(), headers)
	if fresh.Code != http.StatusAccepted || fresh.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("new UUID inherited old replay: %d %s", fresh.Code, fresh.Body)
	}
}

func TestCellDeploymentRetainsUUIDAfterRenameWithoutSlugLookup(t *testing.T) {
	e, app := cellDeploymentHarness(t)
	if _, err := e.store.RenameApp(t.Context(), e.acct.ID, app.Slug, "renamed-shop"); err != nil {
		t.Fatal(err)
	}
	seedCellDeploymentApp(t, e.store, e.acct, app.Slug, uuid.NewString(), "eu-fsn")
	guard := &cellDeploymentGuardStore{Store: e.store}
	e.s.store, e.s.authMw.Authn = guard, guard
	rec := e.do(t, http.MethodPost, cellDeploymentURL(e.s.controlPlaneCell.ID, app.ID), cellImageRequest(), nil)
	if rec.Code != http.StatusAccepted || guard.slugCalls.Load() != 0 {
		t.Fatalf("UUID rename: %d slug reads=%d %s", rec.Code, guard.slugCalls.Load(), rec.Body)
	}
	var dep api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &dep); err != nil || dep.AppID != app.ID {
		t.Fatalf("wrong rename target: %+v %v", dep, err)
	}
}

type cellDeploymentGuardStore struct {
	state.Store
	slugCalls atomic.Int32
	read      chan struct{}
	resume    chan struct{}
	paused    atomic.Bool
}

func (s *cellDeploymentGuardStore) AppBySlug(context.Context, string) (state.App, error) {
	s.slugCalls.Add(1)
	return state.App{}, fmt.Errorf("a UUID deployment must not resolve a slug")
}

func (s *cellDeploymentGuardStore) AppByID(ctx context.Context, id string) (state.App, error) {
	app, err := s.Store.AppByID(ctx, id)
	if err == nil && s.read != nil && s.paused.CompareAndSwap(false, true) {
		close(s.read)
		select {
		case <-s.resume:
		case <-ctx.Done():
			return state.App{}, ctx.Err()
		}
	}
	return app, err
}

func runCellDeploymentDeleteRace(t *testing.T, store state.Store, srv *server, acct state.Account, key string) {
	t.Helper()
	app := seedCellDeploymentApp(t, store, acct, "race-shop", uuid.NewString(), "eu-fsn")
	guard := &cellDeploymentGuardStore{Store: store, read: make(chan struct{}), resume: make(chan struct{})}
	srv.store, srv.authMw.Authn = guard, guard
	handler := srv.handler()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, cellDeploymentURL(srv.controlPlaneCell.ID, app.ID), strings.NewReader(`{"image":"`+cellImageRequest().Image+`"}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); handler.ServeHTTP(rec, req) }()
	select {
	case <-guard.read:
	case <-time.After(10 * time.Second):
		t.Fatal("identity lookup did not run")
	}
	// PostgreSQL retains a soft-deleted app's slug. Release the old name
	// through the supported rename operation before deleting the same UUID.
	if _, err := store.RenameApp(t.Context(), acct.ID, app.Slug, "retired-"+app.ID[:8]); err != nil {
		close(guard.resume)
		<-done
		t.Fatal(err)
	}
	if err := store.DeleteApp(t.Context(), app.ID); err != nil {
		close(guard.resume)
		<-done
		t.Fatal(err)
	}
	replacement := seedCellDeploymentApp(t, store, acct, app.Slug, uuid.NewString(), "eu-fsn")
	close(guard.resume)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("paused request did not finish")
	}
	if rec.Code != http.StatusNotFound || guard.slugCalls.Load() != 0 {
		t.Fatalf("delete race: %d slug reads=%d %s", rec.Code, guard.slugCalls.Load(), rec.Body)
	}
	for _, id := range []string{app.ID, replacement.ID} {
		deps, err := store.ListDeploymentsForApp(t.Context(), id, 0, 0)
		if err != nil || len(deps) != 0 {
			t.Fatalf("race inserted work for %s: %d %v", id, len(deps), err)
		}
	}
}

func TestCellDeploymentDeleteBetweenLookupAndInsert(t *testing.T) {
	e := setup(t, api.PlanPro)
	configureCellDeploymentTest(t, e.store, e.s, "eu-fsn")
	runCellDeploymentDeleteRace(t, e.store, e.s, e.acct, e.key)
}

func TestCellDeploymentSourceUsesExistingPipeline(t *testing.T) {
	e, _ := cellDeploymentHarness(t)
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	t.Setenv("FAAS_STORAGE_BACKEND", "")
	t.Setenv("FAAS_STORAGE_REGIONS_FILE", "")
	// Empty placement retains ADR-475's cell-local filesystem contract.
	app := seedCellDeploymentApp(t, e.store, e.acct, "source-shop", uuid.NewString(), "")
	raw := buildTestTarGz(t, []tar.Header{{Name: "Dockerfile"}}, map[string][]byte{"Dockerfile": []byte("FROM scratch\n")})
	srv := httptest.NewTLSServer(e.h)
	defer srv.Close()
	client := api.NewClient(srv.URL, e.key).SetCompletionCache(nil)
	client.HTTPClient().Transport = srv.Client().Transport
	ctx := api.ContextWithIdempotencyKey(t.Context(), "source-release-1")
	dep, err := client.DeployCellAppMultipart(ctx, e.s.controlPlaneCell.ID, app.ID, bytes.NewReader(raw), "source.tar.gz", "", "", true, "", api.DeployAnnotations{})
	if err != nil || dep.AppID != app.ID || dep.Kind != string(state.DeploymentKindTarball) || dep.BuildID == "" || dep.Status != string(state.DeployBuilding) {
		t.Fatalf("source: %+v %v", dep, err)
	}
	replay, err := client.DeployCellAppMultipart(ctx, e.s.controlPlaneCell.ID, app.ID, bytes.NewReader(raw), "source.tar.gz", "", "", true, "", api.DeployAnnotations{})
	if err != nil || replay.ID != dep.ID {
		t.Fatalf("source replay: %+v %v", replay, err)
	}
	stored, err := e.store.DeploymentByID(t.Context(), dep.ID)
	if err != nil || stored.SourceBytes != int64(len(raw)) || stored.SourcePath == "" {
		t.Fatalf("source intent: %+v %v", stored, err)
	}
}

func TestCellDeploymentScopeRevocationAndEmailGates(t *testing.T) {
	for _, scope := range []string{api.ScopeAppsRead, api.ScopeDeployWrite} {
		t.Run(scope, func(t *testing.T) {
			e, app := cellDeploymentHarness(t)
			token, hash, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			key, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "regional-release", []string{scope})
			if err != nil {
				t.Fatal(err)
			}
			e.key = token
			path := cellDeploymentURL(e.s.controlPlaneCell.ID, app.ID)
			headers := map[string]string{"Idempotency-Key": "auth-release"}
			rec := e.do(t, http.MethodPost, path, cellImageRequest(), headers)
			want := http.StatusForbidden
			if scope == api.ScopeDeployWrite {
				want = http.StatusAccepted
			}
			if rec.Code != want {
				t.Fatalf("scope: %d %s", rec.Code, rec.Body)
			}
			if scope != api.ScopeDeployWrite {
				return
			}
			// Keep the first middleware's authenticator stable while its async
			// key-use bookkeeping runs. A second server shares the replay store.
			unverified := e
			unverified.s = newServer(cellUnverifiedAuthn{e.store}, e.s.log, "gregale.dev", noopNotifier{})
			unverified.s.controlPlaneCell = e.s.controlPlaneCell
			unverified.h = unverified.s.handler()
			rec = unverified.do(t, http.MethodPost, path, cellImageRequest(), headers)
			if rec.Code != http.StatusForbidden || rec.Header().Get("Idempotent-Replayed") != "" {
				t.Fatalf("unverified replay: %d %s", rec.Code, rec.Body)
			}
			if _, err := e.store.MarkAPIKeyRevoked(t.Context(), e.acct.ID, key.ID); err != nil {
				t.Fatal(err)
			}
			rec = e.do(t, http.MethodPost, path, cellImageRequest(), headers)
			if rec.Code != http.StatusUnauthorized || rec.Header().Get("Idempotent-Replayed") != "" {
				t.Fatalf("revoked replay: %d %s", rec.Code, rec.Body)
			}
		})
	}
}

type cellUnverifiedAuthn struct{ state.Store }

func (s cellUnverifiedAuthn) AuthenticateKey(ctx context.Context, hash []byte) (state.Account, state.APIKey, error) {
	acct, key, err := s.Store.AuthenticateKey(ctx, hash)
	acct.EmailVerifiedAt = nil
	return acct, key, err
}

func TestCellDeploymentPersonalOrgAndSharedOrg(t *testing.T) {
	for _, personal := range []bool{true, false} {
		t.Run(fmt.Sprintf("personal=%t", personal), func(t *testing.T) {
			e, _ := cellDeploymentHarness(t)
			var org state.Org
			if personal {
				org = seedActivityPersonalOrg(t, e)
			} else {
				var err error
				org, err = e.store.CreateOrg(t.Context(), state.Org{Slug: "shared-team", Name: "Shared", Plan: api.PlanPro, Status: state.OrgStatusActive})
				if err != nil {
					t.Fatal(err)
				}
				if err := e.store.AddOrgMember(t.Context(), org.ID, e.acct.ID, state.OrgRoleOwner, nil); err != nil {
					t.Fatal(err)
				}
			}
			app, err := e.store.CreateApp(t.Context(), state.App{ID: uuid.NewString(), AccountID: e.acct.ID, OrgID: org.ID, Slug: "org-shop", Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 60, ComputeRegion: "eu-fsn"})
			if err != nil {
				t.Fatal(err)
			}
			path := cellDeploymentURL(e.s.controlPlaneCell.ID, app.ID)
			rec := e.do(t, http.MethodPost, path, cellImageRequest(), nil)
			want := http.StatusNotFound
			if personal {
				want = http.StatusAccepted
			}
			if rec.Code != want {
				t.Fatalf("org app: %d %s", rec.Code, rec.Body)
			}
			token, hash, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.CreateOrgAPIKey(t.Context(), org.ID, e.acct.ID, hash, "org-release", []string{api.ScopeDeployWrite}, nil); err != nil {
				t.Fatal(err)
			}
			e.key = token
			rec = e.do(t, http.MethodPost, path, cellImageRequest(), nil)
			want = http.StatusForbidden
			if personal {
				want = http.StatusAccepted
			}
			if rec.Code != want {
				t.Fatalf("org key: %d %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestCellDeploymentSessionAndMFAGates(t *testing.T) {
	e, cookie := setupWithSession(t)
	req := httptest.NewRequest(http.MethodPost, cellDeploymentURL(uuid.NewString(), uuid.NewString()), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("session-only mutation: %d %s", rec.Code, rec.Body)
	}
	mfa := setupWithMFA(t, api.PlanPro, true, false)
	req = httptest.NewRequest(http.MethodPost, cellDeploymentURL(uuid.NewString(), uuid.NewString()), nil)
	req.AddCookie(mfa.mfaIssueWithPending(t, true))
	rec = httptest.NewRecorder()
	mfa.h.ServeHTTP(rec, req)
	assertProblem(t, rec, http.StatusForbidden, api.CodeMFARequired)
}

func TestCellDeploymentPreservesImageAndPlanValidation(t *testing.T) {
	e, app := cellDeploymentHarness(t)
	env := make(map[string]string)
	for i := 0; i <= api.MustLimitsFor(api.PlanPro).EnvVarsMax; i++ {
		env[fmt.Sprintf("VALUE_%d", i)] = "value"
	}
	for _, req := range []api.CreateDeploymentRequest{
		{Image: "ghcr.io/gregale/shop:latest"},
		{Image: cellImageRequest().Image, Overrides: &api.CreateDeploymentOverrides{Env: env}},
	} {
		legacy := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/deployments", req, nil)
		regional := e.do(t, http.MethodPost, cellDeploymentURL(e.s.controlPlaneCell.ID, app.ID), req, nil)
		if regional.Code != legacy.Code || regional.Code < 400 {
			t.Fatalf("validation diverged: legacy=%d regional=%d %s", legacy.Code, regional.Code, regional.Body)
		}
		var a, b api.Problem
		if err := json.Unmarshal(legacy.Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(regional.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		if a.Code != b.Code {
			t.Fatalf("validation codes diverged: %s vs %s", a.Code, b.Code)
		}
	}
}
