package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

type offboardingLogsFixture struct {
	store  *state.MemStore
	org    state.Org
	viewer state.Account
	key    state.APIKey
	plain  string
	auth   *authmw.Middleware
	log    *slog.Logger
}

func newOffboardingLogsFixture(t *testing.T) offboardingLogsFixture {
	t.Helper()
	store := state.NewMemStore()
	owner, err := store.CreateAccount(t.Context(), "owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := store.CreateAccount(t.Context(), "viewer@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(t.Context(), state.Org{Slug: "offboard-team", Name: "Team", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []struct {
		id   string
		role state.OrgRole
	}{{owner.ID, state.OrgRoleOwner}, {viewer.ID, state.OrgRoleViewer}} {
		if err := store.AddOrgMember(t.Context(), org.ID, member.id, member.role, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: owner.ID, OrgID: org.ID, Slug: "offboard-api"}); err != nil {
		t.Fatal(err)
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateOrgAPIKey(t.Context(), org.ID, viewer.ID, hash, "viewer", []string{api.ScopeAppsRead}, nil)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	auth := authmw.New(store, nil, nil, nil, log, middleware.NewLimiter(middleware.AuthLimitConfig{}), nil)
	return offboardingLogsFixture{store: store, org: org, viewer: viewer, key: key, plain: plain, auth: auth, log: log}
}

type offboardingLogResolver struct {
	opened, stopped chan struct{}
}

func (s *offboardingLogResolver) ScheddForApp(context.Context, string) (logStreamer, error) {
	return s, nil
}

func (s *offboardingLogResolver) StreamAppLogs(ctx context.Context, _ string, _ int64, _ time.Time, _ bool, _ string, _ string, _ string) (scheddgrpc.LogStream, error) {
	close(s.opened)
	return &offboardingLogStream{ctx: ctx, stopped: s.stopped}, nil
}

type offboardingLogStream struct {
	ctx     context.Context
	stopped chan struct{}
	once    sync.Once
}

func (s *offboardingLogStream) Recv() (scheddgrpc.LogFrame, error) {
	<-s.ctx.Done()
	s.once.Do(func() { close(s.stopped) })
	return scheddgrpc.LogFrame{}, s.ctx.Err()
}

func TestOrgOffboardingClosesActiveLogs(t *testing.T) {
	for _, operation := range []string{"remove", "demote", "revoke_key"} {
		t.Run(operation, func(t *testing.T) {
			f := newOffboardingLogsFixture(t)
			upstream := &offboardingLogResolver{opened: make(chan struct{}), stopped: make(chan struct{})}
			mux := http.NewServeMux()
			mux.Handle("GET /v1/apps/{slug}/logs", &AppLogsHandler{Auth: f.auth, Store: f.store, Log: f.log, ScheddFor: upstream, Heartbeat: 10 * time.Millisecond})
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)
			done, cancel := startOffboardingLogRequest(t, server.URL+"/v1/apps/offboard-api/logs?follow=1", f.plain)
			defer cancel()
			waitOffboardingSignal(t, upstream.opened, "live log connection")
			var err error
			switch operation {
			case "remove":
				err = f.store.RemoveOrgMember(t.Context(), f.org.ID, f.viewer.ID)
			case "demote":
				err = f.store.UpdateOrgMemberRole(t.Context(), f.org.ID, f.viewer.ID, state.OrgRoleBilling)
			case "revoke_key":
				_, err = f.store.RevokeOrgAPIKey(t.Context(), f.org.ID, f.key.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			waitOffboardingSignal(t, done, "revoked HTTP log stream")
			waitOffboardingSignal(t, upstream.stopped, "upstream log RPC cancellation")
		})
	}
}

func TestOrgOffboardingCancelsActiveArchiveFetch(t *testing.T) {
	f := newOffboardingLogsFixture(t)
	opened, stopped := make(chan struct{}), make(chan struct{})
	s3 := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(opened)
		<-r.Context().Done()
		close(stopped)
	}))
	t.Cleanup(s3.Close)
	mux := http.NewServeMux()
	mux.Handle("GET /v1/apps/{slug}/logs", &ArchiveLogsHandler{Auth: f.auth, Store: f.store, Log: f.log, S3: newArchiveTestClient(t, s3), Bucket: "test-bucket", Scope: ownedArchiveScope()})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	done, cancel := startOffboardingLogRequest(t, server.URL+"/v1/apps/offboard-api/logs?archive=1&instance=test-instance&date="+archiveTestDate(), f.plain)
	defer cancel()
	waitOffboardingSignal(t, opened, "archive fetch")
	if err := f.store.RemoveOrgMember(t.Context(), f.org.ID, f.viewer.ID); err != nil {
		t.Fatal(err)
	}
	waitOffboardingSignal(t, done, "revoked archive HTTP stream")
	waitOffboardingSignal(t, stopped, "upstream archive request cancellation")
}

func startOffboardingLogRequest(t *testing.T, url, token string) (<-chan struct{}, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, err := http.DefaultClient.Do(r)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}()
	return done, cancel
}

func waitOffboardingSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}
