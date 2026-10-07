package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 053, 057 — source deploys preserve explicit typed startup readiness.
func TestSourceHealthcheckPersistsAcrossTransports(t *testing.T) {
	for _, transport := range []string{"multipart", "source-tarball", "source-ref", "resumable"} {
		for _, probe := range []*api.DeploymentHealthcheck{
			{Path: "/readyz", TimeoutS: 3, Retries: 4},
			{GRPC: &api.DeploymentGRPCHealthcheck{}},
			{GRPC: &api.DeploymentGRPCHealthcheck{Service: "audit.Echo"}, TimeoutS: 3},
		} {
			t.Run(transport+"/"+probe.Path+grpcProbeService(probe), func(t *testing.T) {
				e := newSourceRefTestServer(t, api.PlanPro, "x", 7777)
				raw := buildSourceRefTarGz(t)
				e.gh.streamBody = nopReadCloser{bytes.NewReader(raw)}
				srv := httptest.NewServer(e.h)
				defer srv.Close()
				client := api.NewClient(srv.URL, e.key).SetCompletionCache(nil)
				ctx := t.Context()
				var dep api.DeploymentResponse
				var err error
				switch transport {
				case "multipart":
					dep, err = client.DeployMultipart(ctx, "x", bytes.NewReader(raw), "source.tar.gz", "", "", false, api.DeployAnnotations{Healthcheck: probe})
				case "source-tarball":
					dep, err = client.DeployFromSourceTarball(ctx, "x", bytes.NewReader(raw), "source.tar.gz", api.SourceTarballDeployRequest{Healthcheck: probe})
				case "source-ref":
					dep, err = client.DeployFromSourceRef(ctx, "x", api.SourceRefDeployRequest{Repo: "onebox-faas/hello", Ref: "0123456789abcdef0123456789abcdef01234567", Healthcheck: probe})
				case "resumable":
					var session api.ResumableUploadSession
					session, err = client.StartUpload(ctx, "x", int64(len(raw)), "", api.UploadDeployOptions{Healthcheck: probe})
					if err == nil {
						_, err = client.AppendUpload(ctx, session.UploadID, 0, raw)
					}
					if err == nil {
						dep, err = client.CommitUpload(api.ContextWithIdempotencyKey(ctx, "probe-commit-replay"), session.UploadID)
					}
					if err == nil {
						replay, replayErr := client.CommitUpload(api.ContextWithIdempotencyKey(ctx, "probe-commit-replay"), session.UploadID)
						var conflict *api.APIError
						if !errors.As(replayErr, &conflict) || conflict.Problem.Status != 409 {
							t.Fatalf("commit replay: %+v %v; want committed conflict", replay, replayErr)
						}
						session, discoveryErr := client.GetUploadSession(ctx, session.UploadID)
						if discoveryErr != nil || session.DeploymentID == nil || *session.DeploymentID != dep.ID {
							t.Fatalf("replay discovery: %+v %v", session, discoveryErr)
						}
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				stored, err := e.store.LatestDeployment(ctx, e.appID)
				if err != nil {
					t.Fatal(err)
				}
				var got api.DeploymentHealthcheck
				if err = json.Unmarshal(stored.OverrideHealthcheck, &got); err != nil {
					t.Fatalf("stored startup probe %q: %v", stored.OverrideHealthcheck, err)
				}
				if !reflect.DeepEqual(&got, probe) {
					t.Fatalf("stored=%+v want=%+v", got, probe)
				}
				if dep.OverrideHealthcheck == nil || !reflect.DeepEqual(dep.OverrideHealthcheck, probe) {
					t.Fatalf("response probe=%+v want=%+v", dep.OverrideHealthcheck, probe)
				}
			})
		}
	}
}

func grpcProbeService(probe *api.DeploymentHealthcheck) string {
	if probe.GRPC == nil {
		return ""
	}
	return "grpc-" + probe.GRPC.Service
}

// adr: 053 — invalid source probes must not fetch source, enqueue work, or open an upload.
func TestSourceHealthcheckRejectsInvalidBeforeEnqueue(t *testing.T) {
	for _, transport := range []string{"multipart", "source-tarball", "source-ref", "resumable"} {
		t.Run(transport, func(t *testing.T) {
			e := newSourceRefTestServer(t, api.PlanPro, "x", 7777)
			raw := buildSourceRefTarGz(t)
			e.gh.streamBody = nopReadCloser{bytes.NewReader(raw)}
			srv := httptest.NewServer(e.h)
			defer srv.Close()
			client := api.NewClient(srv.URL, e.key).SetCompletionCache(nil)
			probe := &api.DeploymentHealthcheck{Path: "/readyz", GRPC: &api.DeploymentGRPCHealthcheck{}}
			var err error
			switch transport {
			case "multipart":
				_, err = client.DeployMultipart(t.Context(), "x", bytes.NewReader(raw), "source.tar.gz", "", "", false, api.DeployAnnotations{Healthcheck: probe})
			case "source-tarball":
				_, err = client.DeployFromSourceTarball(t.Context(), "x", bytes.NewReader(raw), "source.tar.gz", api.SourceTarballDeployRequest{Healthcheck: probe})
			case "source-ref":
				_, err = client.DeployFromSourceRef(t.Context(), "x", api.SourceRefDeployRequest{Repo: "onebox-faas/hello", Ref: "0123456789abcdef0123456789abcdef01234567", Healthcheck: probe})
			case "resumable":
				_, err = client.StartUpload(t.Context(), "x", int64(len(raw)), "", api.UploadDeployOptions{Healthcheck: probe})
			}
			var apiErr *api.APIError
			if !errors.As(err, &apiErr) || apiErr.Problem.Status != 400 {
				t.Fatalf("error=%v want validation 400", err)
			}
			if _, err = e.store.LatestDeployment(t.Context(), e.appID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("invalid probe enqueued deployment: %v", err)
			}
		})
	}
}
