package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/simpleapp"
)

func TestComputeRegionDeployPreviewUsesReadOnlyPlanner(t *testing.T) {
	for _, flag := range []string{"--dry-run", "--diff", "--server-diff"} {
		t.Run(flag, func(t *testing.T) {
			withCwd(t, t.TempDir())
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/regional/diff" {
					t.Errorf("unexpected remote request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusInternalServerError)
					return
				}
				calls++
				var req api.DiffRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ComputeRegion == nil || *req.ComputeRegion != "eu-fsn" {
					t.Errorf("region intent=%+v err=%v", req, err)
				}
				_ = json.NewEncoder(w).Encode(api.DiffResponse{Slug: "regional", Plan: "pro",
					Diff: api.DiffPayload{Slug: "regional", Plan: "pro", ComputeRegion: "eu-fsn", Changes: []api.DiffChange{}, Breaks: []api.DiffBreak{}}})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			var out bytes.Buffer
			oldOut := osStdout
			osStdout = &out
			defer func() { osStdout = oldOut }()
			if code := cmdDeployTarball([]string{flag, "--image", "registry.example/app@sha256:" + strings.Repeat("a", 64), "--name", "regional", "--region", "eu-fsn", "--json"}); code != 0 {
				t.Fatalf("preview exit=%d output=%s", code, out.String())
			}
			var response api.DiffResponse
			if err := json.Unmarshal(out.Bytes(), &response); err != nil || calls != 1 || response.Diff.ComputeRegion != "eu-fsn" {
				t.Fatalf("preview=%s calls=%d err=%v", out.String(), calls, err)
			}
		})
	}
}

func TestComputeRegionPlanIsLocalAndMatchesCreateIntent(t *testing.T) {
	withCwd(t, t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("local plan contacted remote: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, jsonMode := range []bool{false, true} {
		var out bytes.Buffer
		oldOut := osStdout
		osStdout = &out
		args := []string{"--plan", "--image", "registry.example/app@sha256:" + strings.Repeat("a", 64), "--name", "regional", "--region", "eu-fsn"}
		if jsonMode {
			args = append(args, "--json")
		}
		code := cmdDeployTarball(args)
		osStdout = oldOut
		if code != 0 {
			t.Fatalf("local plan exit=%d output=%s", code, out.String())
		}
		if !jsonMode {
			if !strings.Contains(out.String(), "eu-fsn (immutable on creation)") || !strings.Contains(out.String(), "no capacity reserved") {
				t.Fatalf("incomplete placement plan: %s", out.String())
			}
			continue
		}
		var plan simpleapp.Plan
		if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
			t.Fatal(err)
		}
		create := api.CreateAppRequest{}
		applySimpleAppPlanToCreateRequest(&create, plan)
		if plan.ComputeRegion != "eu-fsn" || create.ComputeRegion != plan.ComputeRegion {
			t.Fatalf("plan/create placement differs: %+v %+v", plan, create)
		}
	}
}

func TestComputeRegionConnectedRepositoryPreviewPreservesIntent(t *testing.T) {
	for _, flag := range []string{"--dry-run", "--diff", "--server-diff"} {
		t.Run(flag, func(t *testing.T) {
			withCwd(t, t.TempDir())
			sink := &sourceRefSink{scanStatus: http.StatusOK, scanBody: api.PlanResponse{ProjectSlug: "hello", ComputeRegion: "eu-fsn", CanApply: true}}
			srv := httptest.NewServer(sink)
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			_, restore := captureStdout(t)
			defer restore()
			if code := cmdDeployTarball([]string{flag, "--repo", "onebox-faas/hello", "--ref", "main", "--name", "hello", "--region", "eu-fsn"}); code != 0 {
				t.Fatalf("repository preview exit=%d", code)
			}
			if sink.scanCalls != 1 || sink.scanRequest.ComputeRegion != "eu-fsn" || sink.capturedCalls != 0 || sink.createCalls != 0 || sink.getAppCalls != 0 {
				t.Fatalf("repository preview dropped intent or mutated: %+v", sink)
			}
		})
	}
}
