package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentCacheWatchStatesAndConsent(t *testing.T) {
	for _, mode := range []string{"converged", "inherited", "pending", "degraded", "transition", "forged", "superseded"} {
		t.Run(mode, func(t *testing.T) {
			f, _, opts := newEnvironmentCacheFixture(t)
			ctx := t.Context()
			var zero int64
			_, err := f.store.SetEnvironmentCachePolicy(ctx, f.account.ID, f.app.ID, "staging", api.SetEnvironmentCachePolicyRequest{AppID: f.app.ID, ProjectID: f.project.ID, EnvironmentID: f.env.ID, RequestID: uuid.NewString(), ExpectedRevision: &zero, Rules: []api.EnvironmentCacheRule{}, Inherited: mode == "inherited"})
			if err != nil {
				t.Fatal(err)
			}
			role, target := "compute-node", "tcp://127.0.0.1:8080"
			node, err := f.store.CreateComputeNode(ctx, state.ComputeNode{Name: "observer", TargetURL: "tcp://127.0.0.1:50051", GatewayTargetURL: &target, Active: true, Role: &role, VPCPUs: 1, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 512, VCPUBudget: 1})
			if err != nil {
				t.Fatal(err)
			}
			session := uuid.NewString()
			if mode != "degraded" {
				if err = f.store.BeginEnvironmentCacheGatewaySession(ctx, node.Name, session, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "converged" || mode == "inherited" || mode == "superseded" {
				if err = f.store.RecordEnvironmentCacheGatewayObserved(ctx, node.Name, session, f.app.ID, f.env.ID, 1); err != nil {
					t.Fatal(err)
				}
			}
			reads := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/apps/alpha/cache-policy/convergence" {
					f.serveCache(w, r)
					return
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				reads++
				if r.URL.Query().Get("environment") != "staging" {
					t.Error("watch escaped scope")
				}
				if mode == "transition" && reads > 1 {
					if err := f.store.RecordEnvironmentCacheGatewayObserved(r.Context(), node.Name, session, f.app.ID, f.env.ID, 1); err != nil {
						t.Error(err)
					}
				}
				out, err := f.store.GetEnvironmentCacheConvergence(r.Context(), f.account.ID, f.app.ID, "staging")
				if err != nil {
					w.WriteHeader(500)
					return
				}
				if mode == "forged" {
					out.Status = "converged"
					out.ServingGateways = 0
					out.PendingGateways = 0
				}
				_ = json.NewEncoder(w).Encode(out)
			}))
			t.Cleanup(backend.Close)
			opts.APIBase = backend.URL
			opts.AllowWrites = false
			cs, callCtx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			revision := int64(1)
			if mode == "superseded" {
				revision = 2
			}
			args := map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "revision": revision, "wait_seconds": 2}
			wantError := mode == "forged" || mode == "superseded"
			out := callTool(t, callCtx, cs, "gregale_watch_environment_cache_policy", args, wantError)
			if mode == "forged" && string(out["code"]) != `"invalid_response"` {
				t.Fatalf("forged evidence=%s", out["code"])
			}
			if mode == "superseded" && string(out["code"]) != `"policy_superseded"` {
				t.Fatalf("superseded=%s", out["code"])
			}
			if !wantError {
				expected := mode
				if mode == "pending" {
					expected = "timed_out"
				}
				if mode == "transition" || mode == "inherited" {
					expected = "converged"
				}
				if string(out["status"]) != `"`+expected+`"` {
					t.Fatalf("watch=%s want %s", out["status"], expected)
				}
			}
			args["environment"] = "production"
			callTool(t, callCtx, cs, "gregale_watch_environment_cache_policy", args, true)
			if f.writes != 0 {
				t.Fatal("watch performed a write")
			}
		})
	}
}
