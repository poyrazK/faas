//go:build !no_pg

package apidserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

type offboardingPausedBody struct {
	reader  io.Reader
	started chan struct{}
	resume  chan struct{}
	once    sync.Once
	unblock sync.Once
}

func (b *offboardingPausedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.resume
	return b.reader.Read(p)
}

func (b *offboardingPausedBody) release() { b.unblock.Do(func() { close(b.resume) }) }

func TestOrgOffboardingPostgresRejectsPausedMutation(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"configuration", "PUT", "/v1/apps/offboard-api/env/LEVEL", api.PutAppEnvRequest{Value: "forbidden"}},
		{"deployment", "POST", "/v1/apps/offboard-api/deployments", api.CreateDeploymentRequest{Image: "r/x@sha256:" + strings.Repeat("a", 64)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.SetupPGHandler(t, api.PlanPro)
			store := *e.Store()
			org, err := store.CreateOrg(t.Context(), state.Org{Slug: "offboard-team", Name: "Offboard Team", Plan: api.PlanPro})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.AddOrgMember(t.Context(), org.ID, (*e.Acct()).ID, state.OrgRoleOwner, nil); err != nil {
				t.Fatal(err)
			}
			actor, actorHeaders := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
			if err := store.UpdateAccountPlan(t.Context(), actor.ID, api.PlanPro); err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(t.Context(), state.App{AccountID: actor.ID, OrgID: org.ID, Slug: "offboard-api"})
			if err != nil {
				t.Fatal(err)
			}
			plain, hash, err := api.GenerateAPIKey()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateOrgAPIKey(t.Context(), org.ID, (*e.Acct()).ID, hash, "owner", api.ScopesAdminOnly, nil); err != nil {
				t.Fatal(err)
			}
			ownerHeaders := map[string]string{"Authorization": "Bearer " + plain}
			payload, err := json.Marshal(tc.body)
			if err != nil {
				t.Fatal(err)
			}
			body := &offboardingPausedBody{reader: bytes.NewReader(payload), started: make(chan struct{}), resume: make(chan struct{})}
			r := httptest.NewRequest(tc.method, tc.path, body)
			r.Header.Set("Authorization", actorHeaders["Authorization"])
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { (*e.H()).ServeHTTP(w, r); close(done) }()
			defer func() {
				body.release()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("request did not stop during cleanup")
				}
			}()
			select {
			case <-body.started:
			case <-done:
				t.Fatalf("request finished before body: %d %s", w.Code, w.Body)
			case <-time.After(5 * time.Second):
				t.Fatal("request did not reach the body after authorization")
			}
			removed := e.Do(t, "DELETE", "/v1/orgs/"+org.Slug+"/members/"+actor.ID, nil, ownerHeaders)
			if removed.Code != http.StatusNoContent {
				t.Fatalf("remove: %d %s", removed.Code, removed.Body)
			}
			// Resume only after the removal endpoint committed successfully.
			body.release()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("revoked request did not finish")
			}
			if w.Code != http.StatusForbidden {
				t.Fatalf("paused mutation: %d %s", w.Code, w.Body)
			}
			rows, err := store.ListAppEnvInScope(t.Context(), actor.ID, app.ID, "default")
			if err != nil || len(rows) != 0 {
				t.Fatalf("configuration persisted after removal: %+v %v", rows, err)
			}
			deps, err := store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
			if err != nil || len(deps) != 0 {
				t.Fatalf("deployment persisted after removal: %+v %v", deps, err)
			}
			if response := e.Do(t, "PUT", "/v1/apps/offboard-api/env/LEVEL", api.PutAppEnvRequest{Value: "teammate"}, ownerHeaders); response.Code != http.StatusOK {
				t.Fatalf("remaining owner lost company app: %d %s", response.Code, response.Body)
			}
		})
	}
}
