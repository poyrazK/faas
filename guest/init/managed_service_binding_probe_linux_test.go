//go:build linux

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

func managedGuestProbeSpec() api.ManagedServiceProbeSpec {
	return api.ManagedServiceProbeSpec{Version: 1, CallerDeploymentID: uuid.NewString(), Scope: "production", Service: "billing", Binding: "CUSTOM_BILLING_URL", TargetAppID: uuid.NewString(), TargetDeploymentID: uuid.NewString(), ReleaseID: uuid.NewString(), ContextRevision: strings.Repeat("a", 64)}
}

func TestManagedWorkerProbeDoesNotExecuteImageEntrypoint(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "worker-started")
	spec := managedGuestProbeSpec()
	manifest := api.AppManifest{ExecutionMode: api.ExecutionModeWorker, User: "0", Entrypoint: []string{"/bin/sh", "-c", "touch \"$1\"", "worker", marker}}
	var output strings.Builder
	result, err := executeAppTaskCommand(t.Context(), apptaskproto.Request{Version: apptaskproto.Version, TaskID: "worker-probe", Command: spec.Command(), TimeoutSeconds: 2, MaxOutputBytes: 4096}, manifest, nil, map[string]string{api.PlatformDeploymentIDEnv: spec.CallerDeploymentID}, &output, io.Discard)
	var report api.ServiceBindingProbeReport
	if err != nil || result.Status != apptaskproto.StatusFailed || json.Unmarshal([]byte(output.String()), &report) != nil || report.ManagedService == nil || *report.ManagedService != spec || report.Authorization.Status != "failed" || report.DNS.Status != "not_checked" {
		t.Fatalf("worker probe did not use the reserved environment-validation handler: result=%+v report=%s err=%v", result, output.String(), err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("worker image entrypoint ran during a platform probe")
	}
}

func TestManagedServiceProbeRequiresGatewayReleaseConfirmation(t *testing.T) {
	for _, scenario := range []string{"exact", "missing-release", "other-release", "duplicate-release", "missing-target", "other-target", "duplicate-target"} {
		t.Run(scenario, func(t *testing.T) {
			spec := managedGuestProbeSpec()
			cert, caPEM := serviceProbeTestCertificate(t, "billing.internal")
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead || r.URL.Path != api.ServiceBindingProbePath || r.Header.Get(api.TargetDeploymentHeader) != spec.TargetDeploymentID || r.Header.Get(api.ReleaseHeader) != spec.ReleaseID {
					t.Errorf("request did not pin reviewed route: %s %s %v", r.Method, r.URL, r.Header)
				}
				w.Header().Set(api.ServiceBindingProbeResponseHeader, api.ServiceBindingProbeVersion)
				w.Header().Set(api.ServiceBindingProbeStageHeader, "complete")
				w.Header().Set(api.TargetDeploymentHeader, spec.TargetDeploymentID)
				w.Header().Set(api.ReleaseHeader, spec.ReleaseID)
				switch scenario {
				case "missing-release":
					w.Header().Del(api.ReleaseHeader)
				case "other-release":
					w.Header().Set(api.ReleaseHeader, uuid.NewString())
				case "duplicate-release":
					w.Header().Add(api.ReleaseHeader, spec.ReleaseID)
				case "missing-target":
					w.Header().Del(api.TargetDeploymentHeader)
				case "other-target":
					w.Header().Set(api.TargetDeploymentHeader, uuid.NewString())
				case "duplicate-target":
					w.Header().Add(api.TargetDeploymentHeader, spec.TargetDeploymentID)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
			server.StartTLS()
			defer server.Close()
			resolver := &serviceProbeTestResolver{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}
			report := runServiceBindingContextProbe(t.Context(), spec.Service, spec.TargetDeploymentID, &spec, caPEM, resolver, func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			})
			if report.Passed() != (scenario == "exact") || report.ManagedService == nil || *report.ManagedService != spec {
				t.Fatalf("report=%+v", report)
			}
		})
	}
}

func TestManagedServiceProbeRejectsMissingFrozenGuestEnvironment(t *testing.T) {
	spec := managedGuestProbeSpec()
	for _, scenario := range []string{"missing-binding", "wrong-caller", "wrong-url", "malformed-context"} {
		t.Run(scenario, func(t *testing.T) {
			env := map[string]string{api.PlatformDeploymentIDEnv: spec.CallerDeploymentID, spec.Binding: "https://billing.internal"}
			command := spec.Command()
			switch scenario {
			case "missing-binding":
				delete(env, spec.Binding)
				t.Setenv(spec.Binding, "https://billing.internal") // Ambient values cannot replace staged frozen inputs.
			case "wrong-caller":
				env[api.PlatformDeploymentIDEnv] = uuid.NewString()
			case "wrong-url":
				env[spec.Binding] = "https://other.internal"
			case "malformed-context":
				command[2] = "{}"
			}
			var output strings.Builder
			result, err := executeManagedServiceBindingProbe(t.Context(), apptaskproto.Request{Command: command}, api.AppManifest{}, nil, env, &output)
			var report api.ServiceBindingProbeReport
			if err != nil || json.Unmarshal([]byte(output.String()), &report) != nil || result.Status != apptaskproto.StatusFailed || report.Passed() || report.DNS.Status != "not_checked" {
				t.Fatalf("invalid environment reached network: result=%+v report=%s err=%v", result, output.String(), err)
			}
			if scenario != "malformed-context" && report.Authorization.Status != "failed" {
				t.Fatalf("missing frozen inputs reached trust/network preparation: %+v", report)
			}
		})
	}
}
