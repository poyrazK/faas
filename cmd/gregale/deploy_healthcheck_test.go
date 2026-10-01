package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 053, 057 — CLI startup probes select the existing typed contract.
func TestDeployHealthcheckFlagsBeforeNetwork(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "unexpected network", 500) }))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	for _, args := range [][]string{
		{"--healthcheck-path", ""},
		{"--healthcheck-path", "readyz"},
		{"--healthcheck-path", "/readyz", "--healthcheck-grpc"},
		{"--healthcheck-grpc-service", "audit.Echo"},
		{"--healthcheck-grpc=false", "--healthcheck-grpc-service", ""},
		{"--healthcheck-grpc", "--project"},
		{"--healthcheck-grpc", "--dry-run"},
		{"--healthcheck-grpc", "--create-only"},
	} {
		if code := cmdDeployTarball(args); code != 1 {
			t.Errorf("args=%v code=%d want1", args, code)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid flags made %d network calls", calls)
	}
}

// adr: 053 — source-ref CLI request retains named and whole-server gRPC readiness.
func TestDeployHealthcheckSourceRefCLI(t *testing.T) {
	for _, service := range []string{"", "audit.Echo"} {
		t.Run(service, func(t *testing.T) {
			sink := &sourceRefSink{existingApp: true, status: http.StatusAccepted, body: api.DeploymentResponse{ID: "dep-health", Status: "queued"}}
			srv := httptest.NewServer(sink)
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			withResetJSONOutput(t, false)
			args := []string{"--name", "hello", "--repo", "owner/repo", "--ref", "main", "--no-wait", "--healthcheck-grpc", "--healthcheck-grpc-service", service}
			if code := cmdDeployTarball(args); code != 0 {
				t.Fatalf("code=%d", code)
			}
			var req api.SourceRefDeployRequest
			if err := json.Unmarshal(sink.capturedBody, &req); err != nil {
				t.Fatal(err)
			}
			want := &api.DeploymentHealthcheck{GRPC: &api.DeploymentGRPCHealthcheck{Service: service}}
			if !reflect.DeepEqual(req.Healthcheck, want) {
				t.Fatalf("probe=%+v want=%+v", req.Healthcheck, want)
			}
		})
	}
}

// adr: 053 — changing probe semantics must not replay an older auto-keyed deployment.
func TestDeployHealthcheckIdempotency(t *testing.T) {
	base := deployIdempotencyIntent{Slug: "demo", SourceSHA256: "same-source"}
	old, err := deployIdempotencyKey("", base)
	if err != nil {
		t.Fatal(err)
	}
	base.Healthcheck = &api.DeploymentHealthcheck{GRPC: &api.DeploymentGRPCHealthcheck{}}
	next, err := deployIdempotencyKey("", base)
	if err != nil {
		t.Fatal(err)
	}
	if old == next {
		t.Fatal("startup probe missing from idempotency intent")
	}
	base.Healthcheck.GRPC.Service = "audit.Echo"
	named, _ := deployIdempotencyKey("", base)
	if next == named {
		t.Fatal("gRPC service missing from idempotency intent")
	}
}

// adr: 053 — a local deploy keeps its probe through resumable, fallback,
// explicit traffic and image transport selection.
func TestDeployHealthcheckLocalCLITransports(t *testing.T) {
	for _, transport := range []string{"resumable", "fallback", "traffic", "image"} {
		t.Run(transport, func(t *testing.T) {
			repo := initZeroConfigRepo(t)
			mustGit(t, repo, "remote", "remove", "origin")
			withCwd(t, repo)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			var got *api.DeploymentHealthcheck
			stub := newZeroConfigStubServer(t, func(w http.ResponseWriter, r *http.Request, _ *zeroConfigStubServer) {
				switch {
				case r.URL.Path == "/v1/apps" && r.Method == http.MethodPost:
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "demo"})
				case r.URL.Path == "/v1/uploads" && r.Method == http.MethodPost:
					if transport != "resumable" {
						http.NotFound(w, r)
						return
					}
					var req api.UploadStartRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if req.DeployOptions != nil {
						got = req.DeployOptions.Healthcheck
					}
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(api.UploadStartResponse{UploadID: "probe-upload", ChunkSize: 8 * 1024 * 1024, TotalSize: req.TotalSize})
				case r.URL.Path == "/v1/uploads/probe-upload" && r.Method == http.MethodPatch:
					raw, _ := io.ReadAll(r.Body)
					w.Header().Set("Upload-Offset", strconv.Itoa(len(raw)))
				case r.URL.Path == "/v1/uploads/probe-upload/commit":
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", Status: "pending"})
				case r.URL.Path == "/v1/apps/demo/deployments" && r.Method == http.MethodPost:
					if transport == "image" {
						var req api.CreateDeploymentRequest
						if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
							t.Error(err)
						}
						if req.Overrides != nil {
							got = req.Overrides.Healthcheck
						}
					} else {
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Error(err)
						}
						if err := json.Unmarshal([]byte(r.FormValue("healthcheck")), &got); err != nil {
							t.Error(err)
						}
					}
					w.WriteHeader(http.StatusAccepted)
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", AppID: "a1", Status: "pending"})
				default:
					http.NotFound(w, r)
				}
			})
			t.Setenv("FAAS_API", stub.srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			withResetJSONOutput(t, false)
			args := []string{"--name", "demo", "--app", "--no-wait", "--healthcheck-grpc", "--healthcheck-grpc-service", "audit.Echo"}
			if transport == "traffic" {
				args = append(args, "--traffic-percent", "0")
			}
			if transport == "image" {
				args = append(args, "--image", "registry.example.com/app@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			}
			if code := cmdDeployTarball(args); code != 0 {
				t.Fatalf("code=%d", code)
			}
			want := &api.DeploymentHealthcheck{GRPC: &api.DeploymentGRPCHealthcheck{Service: "audit.Echo"}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("transport=%s probe=%+v want=%+v", transport, got, want)
			}
		})
	}
}
