package apidserver_test

// adr: 053, 057, 987 — source transport parity and lifecycle-aware release admission.

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

type sourceAdmissionFixture struct {
	env    apidserver.SourceRefTestEnv
	client *api.Client
}

func newSourceAdmissionFixture(t *testing.T) sourceAdmissionFixture {
	t.Helper()
	e := apidserver.NewSourceRefTestServer(t, api.PlanPro, "x", 7777)
	if err := (*e.Store()).StampSafeReleaseWorkerLease(t.Context(), time.Hour); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(*e.H())
	t.Cleanup(srv.Close)
	return sourceAdmissionFixture{env: e, client: api.NewClient(srv.URL, *e.Key()).SetCompletionCache(nil)}
}

func sourceAdmissionArchive(t *testing.T, manifest string) []byte {
	t.Helper()
	return apidserver.BuildTestTarGz(t, []tar.Header{{Name: "index.js"}, {Name: "gregale.yaml"}}, map[string][]byte{"index.js": []byte("console.log(1)\n"), "gregale.yaml": []byte(manifest)})
}

func (f sourceAdmissionFixture) deploy(t *testing.T, transport string, raw []byte, ann api.DeployAnnotations) (api.DeploymentResponse, error) {
	t.Helper()
	ctx := t.Context()
	switch transport {
	case "multipart":
		return f.client.DeployMultipart(ctx, "x", bytes.NewReader(raw), "source.tar.gz", "", "", false, ann)
	case "source-tarball":
		return f.client.DeployFromSourceTarball(ctx, "x", bytes.NewReader(raw), "source.tar.gz", api.SourceTarballDeployRequest{TrafficPercent: ann.TrafficPercent, Canary: ann.Canary, Healthcheck: ann.Healthcheck})
	case "source-ref":
		*(*f.env.Gh()).StreamBody() = apidserver.NopReadCloser{Reader: bytes.NewReader(raw)}
		return f.client.DeployFromSourceRef(ctx, "x", api.SourceRefDeployRequest{Repo: "onebox-faas/hello", Ref: "0123456789abcdef0123456789abcdef01234567", TrafficPercent: ann.TrafficPercent, Canary: ann.Canary, Healthcheck: ann.Healthcheck})
	case "resumable":
		session, err := f.client.StartUpload(ctx, "x", int64(len(raw)), "", api.UploadDeployOptions{Companions: ann.Companions, Healthcheck: ann.Healthcheck, Workflows: ann.Workflows})
		if err != nil {
			return api.DeploymentResponse{}, err
		}
		if _, err = f.client.AppendUpload(ctx, session.UploadID, 0, raw); err != nil {
			return api.DeploymentResponse{}, err
		}
		return f.client.CommitUpload(ctx, session.UploadID)
	default:
		t.Fatalf("unsupported transport %s", transport)
		return api.DeploymentResponse{}, nil
	}
}

func TestSourceDeploymentPreservesManifestStartupDependencies(t *testing.T) {
	for _, transport := range []string{"multipart", "source-tarball", "source-ref", "resumable"} {
		for _, traffic := range []string{"default", "dark", "canary"} {
			if transport == "resumable" && traffic != "default" {
				continue
			}
			t.Run(transport+"/"+traffic, func(t *testing.T) {
				apidserver.WithTestSidecarRecipient(t)
				f := newSourceAdmissionFixture(t)
				manifest := "main_depends_on:\n  - name: proxy\n    condition: healthy\ncompanions:\n  - name: proxy\n    image: registry.example.com/proxy@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n    startup_probe:\n      tcp_socket:\n        port: 8081\n"
				probe := &api.DeploymentHealthcheck{Path: "/readyz", TimeoutS: 3}
				ann := api.DeployAnnotations{Healthcheck: probe}
				if traffic == "dark" {
					zero := 0
					ann.TrafficPercent = &zero
				}
				if traffic == "canary" {
					ann.Canary = &api.CanaryPresetSpec{Preset: "balanced"}
				}
				if transport == "multipart" {
					ann.OverrideEnv = map[string]string{"REVISION": "fixture"}
				}
				d, err := f.deploy(t, transport, sourceAdmissionArchive(t, manifest), ann)
				if err != nil {
					t.Fatal(err)
				}
				want := []api.WorkloadDependency{{Name: "proxy", Condition: api.WorkloadDependencyHealthy}}
				if !reflect.DeepEqual(d.OverrideMainDependsOn, want) || len(d.Companions) != 1 {
					t.Fatalf("dependencies=%+v companions=%+v", d.OverrideMainDependsOn, d.Companions)
				}
				if !reflect.DeepEqual(d.OverrideHealthcheck, probe) {
					t.Fatalf("probe=%+v", d.OverrideHealthcheck)
				}
				if transport == "multipart" {
					stored, err := (*f.env.Store()).DeploymentByID(t.Context(), d.ID)
					if err != nil {
						t.Fatal(err)
					}
					var env map[string]string
					if err := json.Unmarshal(stored.OverrideEnv, &env); err != nil || env["REVISION"] != "fixture" {
						t.Fatalf("override env=%s err=%v", stored.OverrideEnv, err)
					}
				}
			})
		}
	}
}

func TestSourceDeploymentSelectsEffectiveLifecycleRollout(t *testing.T) {
	for _, transport := range []string{"multipart", "source-tarball", "source-ref", "resumable"} {
		for _, transition := range []struct {
			name, before, after string
			canary, service     bool
		}{
			{"request-to-service", api.ExecutionModeRequest, api.ExecutionModeService, false, true},
			{"service-to-request", api.ExecutionModeService, api.ExecutionModeRequest, true, false},
			{"service-to-worker", api.ExecutionModeService, api.ExecutionModeWorker, false, false},
		} {
			t.Run(transport+"/"+transition.name, func(t *testing.T) {
				f := newSourceAdmissionFixture(t)
				store := *f.env.Store()
				app, err := store.AppByID(t.Context(), *f.env.AppID())
				if err != nil {
					t.Fatal(err)
				}
				manifest := app.Manifest
				manifest.ExecutionMode = transition.before
				maximum := 2
				if _, err = store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &manifest, MaxConcurrency: &maximum}); err != nil {
					t.Fatal(err)
				}
				if _, err = store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage, ImageDigest: "sha256:serving"}); err != nil {
					t.Fatal(err)
				}
				if err = store.StampSafeReleaseWorkerLease(t.Context(), time.Hour); err != nil {
					t.Fatal(err)
				}
				source := "lifecycle:\n  execution_mode: " + transition.after + "\n"
				if transition.service {
					source += "  restart_policy: always\n  service_replicas:\n    min: 0\n    max: 2\n    desired: 2\n"
				}
				d, err := f.deploy(t, transport, sourceAdmissionArchive(t, source), api.DeployAnnotations{})
				if err != nil {
					t.Fatal(err)
				}
				stored, err := store.DeploymentByID(t.Context(), d.ID)
				if err != nil {
					t.Fatal(err)
				}
				if state.IsServiceRollout(stored) != transition.service || (stored.CanaryTotalSteps > 0) != transition.canary {
					t.Fatalf("rollout=%s canary=%s steps=%d traffic=%d", stored.RolloutState, stored.CanaryPreset, stored.CanaryTotalSteps, stored.TrafficPercent)
				}
				if transition.service && stored.TrafficPercent != 0 {
					t.Fatalf("service rollout traffic=%d", stored.TrafficPercent)
				}
			})
		}
	}
}

func TestResumableFunctionSourceValidation(t *testing.T) {
	for _, tc := range []struct {
		name, runtime, handler string
		dockerfile, flag       bool
		wantError              bool
	}{
		{"binary explicit Dockerfile", "binary", "handler", true, true, false},
		{"binary detected Dockerfile", "binary", "handler", true, false, false},
		{"binary missing Dockerfile", "binary", "handler", false, false, true},
		{"node flagged Dockerfile", "node22", "handler.handler", true, true, true},
		{"node detected Dockerfile", "node22", "handler.handler", true, false, true},
		{"node escaping handler", "node22", "../outside.js", false, false, true},
		{"node valid handler", "node22", "handler.js", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
			e := apidserver.Setup(t, api.PlanPro)
			srv := httptest.NewServer(*e.H())
			defer srv.Close()
			client := api.NewClient(srv.URL, *e.Key()).SetCompletionCache(nil)
			app, err := client.CreateApp(t.Context(), api.CreateAppRequest{Slug: "function-source", Type: "function", Runtime: tc.runtime})
			if err != nil {
				t.Fatal(err)
			}
			headers := []tar.Header{{Name: "handler.js"}}
			files := map[string][]byte{"handler.js": []byte("exports.handler=()=>1;\n")}
			if tc.dockerfile {
				headers = append(headers, tar.Header{Name: "Dockerfile"}, tar.Header{Name: "handler"})
				files["Dockerfile"] = []byte("FROM scratch\nCOPY handler /app/handler\nENTRYPOINT [\"/app/handler\"]\n")
				files["handler"] = []byte("\x7fELF-static")
			}
			raw := apidserver.BuildTestTarGz(t, headers, files)
			session, err := client.StartUpload(t.Context(), app.Slug, int64(len(raw)), "", api.UploadDeployOptions{Runtime: tc.runtime, Handler: tc.handler, Dockerfile: tc.flag})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = client.AppendUpload(t.Context(), session.UploadID, 0, raw); err != nil {
				t.Fatal(err)
			}
			d, err := client.CommitUpload(t.Context(), session.UploadID)
			if tc.wantError {
				var remote *api.APIError
				if !errors.As(err, &remote) || remote.Problem.Status != http.StatusBadRequest {
					t.Fatalf("deployment=%+v error=%v; want validation 400", d, err)
				}
				if _, err := (*e.Store()).LatestDeployment(t.Context(), app.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("invalid source enqueued work: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
