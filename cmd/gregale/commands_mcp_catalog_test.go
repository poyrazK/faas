package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProbeMCPCatalogHealth(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		supported bool
		healthy   bool
		wantError string
	}{
		{
			name:      "healthy catalog",
			status:    http.StatusOK,
			body:      fmt.Sprintf(`{"status":"ok","instance_id":"instance-a","catalog_revision":7,"catalog_sha256":%q}`, digest),
			supported: true,
			healthy:   true,
		},
		{
			name:      "degraded pinned catalog",
			status:    http.StatusServiceUnavailable,
			body:      fmt.Sprintf(`{"status":"degraded","instance_id":"instance-a","catalog_revision":8,"catalog_sha256":%q}`, digest),
			supported: true,
			wantError: "HTTP 503",
		},
		{
			name:      "ordinary health endpoint has no catalog contract",
			status:    http.StatusOK,
			body:      `{"status":"ok"}`,
			wantError: "does not expose catalog integrity",
		},
		{
			name:      "malformed digest is supported but unhealthy",
			status:    http.StatusOK,
			body:      `{"status":"ok","catalog_revision":2,"catalog_sha256":"bad"}`,
			supported: true,
			wantError: "lowercase 64-character",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/healthz" {
					t.Errorf("health path = %q, want /healthz", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			report := probeMCPCatalogHealth(context.Background(), server.URL+"/mcp", time.Second)
			if report.Supported != tc.supported || report.Healthy != tc.healthy {
				t.Fatalf("report support/health = %t/%t, want %t/%t", report.Supported, report.Healthy, tc.supported, tc.healthy)
			}
			if tc.wantError != "" && !strings.Contains(report.Error, tc.wantError) {
				t.Fatalf("report error = %q, want substring %q", report.Error, tc.wantError)
			}
			if tc.healthy && (report.CatalogRevision == nil || *report.CatalogRevision != 7 || report.CatalogSHA256 != digest) {
				t.Fatalf("catalog health fields = revision %v digest %q", report.CatalogRevision, report.CatalogSHA256)
			}
			if tc.healthy && report.InstanceID != "instance-a" {
				t.Fatalf("instance ID = %q, want instance-a", report.InstanceID)
			}
		})
	}
}

func TestProbeMCPCatalogDeploymentCoversRunningReplicas(t *testing.T) {
	digest := strings.Repeat("a", 64)
	var healthRequests int
	var expectedHost string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/demo/instances" {
			_, _ = w.Write([]byte(`[
				{"id":"instance-a","deployment_id":"deployment-a","state":"running"},
				{"id":"instance-b","deployment_id":"deployment-a","state":"running"},
				{"id":"parked","deployment_id":"deployment-a","state":"parked"},
				{"id":"other-deployment","deployment_id":"deployment-b","state":"running"}
			]`))
			return
		}
		const probePrefix = "/v1/apps/demo/deployments/deployment-a/instances/"
		if !strings.HasPrefix(r.URL.Path, probePrefix) || !strings.HasSuffix(r.URL.Path, "/mcp-health") {
			http.NotFound(w, r)
			return
		}
		instanceID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, probePrefix), "/mcp-health")
		if (instanceID != "instance-a" && instanceID != "instance-b") || r.URL.Query().Get("host") != expectedHost {
			t.Errorf("probe path/query = %q/%q, want the requested running replica and host %q", r.URL.Path, r.URL.RawQuery, expectedHost)
		}
		healthRequests++
		w.Header().Set("Content-Type", "application/json")
		healthBody := fmt.Sprintf(`{"status":"ok","instance_id":%q,"catalog_revision":3,"catalog_sha256":%q}`, instanceID, digest)
		_, _ = fmt.Fprintf(w, `{"app_id":"app-id","deployment_id":"deployment-a","instance_id":%q,"http_status":200,"health_body":%q}`, instanceID, healthBody)
	}))
	defer server.Close()
	expectedHost = strings.TrimPrefix(server.URL, "http://")

	report := probeMCPCatalogDeployment(context.Background(), api.NewClient(server.URL, "token"), "demo", "deployment-a", server.URL, time.Second)
	if !report.Supported || !report.Healthy || report.ExpectedReplicas != 2 || report.CheckedReplicas != 2 {
		t.Fatalf("fleet health = %+v, want both running replicas healthy", report)
	}
	if len(report.Replicas) != 2 || report.Replicas[0].InstanceID != "instance-a" || report.Replicas[1].InstanceID != "instance-b" {
		t.Fatalf("replica reports = %+v, want sorted instance-a and instance-b", report.Replicas)
	}
	if healthRequests != 2 {
		t.Fatalf("health probes = %d, want one per replica", healthRequests)
	}
}

func TestProbeMCPCatalogExactFleetFailsWhenReplicaProbeFails(t *testing.T) {
	digest := strings.Repeat("b", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/instance-b/") {
			http.NotFound(w, r)
			return
		}
		healthBody := fmt.Sprintf(`{"status":"ok","instance_id":"instance-a","catalog_revision":1,"catalog_sha256":%q}`, digest)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"app_id":"app-id","deployment_id":"deployment-a","instance_id":"instance-a","http_status":200,"health_body":%q}`, healthBody)
	}))
	defer server.Close()

	report := probeMCPCatalogExactFleet(context.Background(), api.NewClient(server.URL, "token"), "demo", "deployment-a", server.URL, []string{"instance-a", "instance-b"}, time.Second)
	if report.Healthy || report.CheckedReplicas != 1 || len(report.MissingInstanceIDs) != 1 || report.MissingInstanceIDs[0] != "instance-b" {
		t.Fatalf("fleet health = %+v, want an incomplete check naming instance-b", report)
	}
}

func TestProbeMCPCatalogExactFleetRejectsDigestDrift(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		instanceID := "instance-a"
		if strings.Contains(r.URL.Path, "/instance-b/") {
			instanceID = "instance-b"
		}
		digest := strings.Repeat("a", 64)
		if instanceID == "instance-b" {
			digest = strings.Repeat("b", 64)
		}
		healthBody := fmt.Sprintf(`{"status":"ok","instance_id":%q,"catalog_revision":1,"catalog_sha256":%q}`, instanceID, digest)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"app_id":"app-id","deployment_id":"deployment-a","instance_id":%q,"http_status":200,"health_body":%q}`, instanceID, healthBody)
	}))
	defer server.Close()

	report := probeMCPCatalogExactFleet(context.Background(), api.NewClient(server.URL, "token"), "demo", "deployment-a", server.URL, []string{"instance-a", "instance-b"}, time.Second)
	if report.Healthy || !strings.Contains(report.Error, "different MCP catalog digests") {
		t.Fatalf("fleet health = %+v, want digest drift failure", report)
	}
}

func TestMCPHealthURLUsesRootHealthPath(t *testing.T) {
	got, err := mcpHealthURL("https://mcp.example.test/custom/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://mcp.example.test/healthz" {
		t.Fatalf("health URL = %q", got)
	}
	for _, endpoint := range []string{
		"https://user:pass@mcp.example.test/mcp",
		"https://mcp.example.test/mcp?token=secret",
		"https://mcp.example.test/mcp#fragment",
	} {
		if _, err := mcpHealthURL(endpoint); err == nil {
			t.Errorf("mcpHealthURL(%q) accepted an unsafe endpoint", endpoint)
		}
	}
}

func TestMCPCatalogDigestForPin(t *testing.T) {
	digest := strings.Repeat("a", 64)
	revision := int64(7)
	for _, tc := range []struct {
		name    string
		report  mcpCatalogHealthReport
		want    string
		wantErr bool
	}{
		{name: "unsupported endpoint remains compatible", report: mcpCatalogHealthReport{}},
		{name: "healthy catalog", report: mcpCatalogHealthReport{Supported: true, Healthy: true, HTTPStatus: http.StatusOK, Status: "ok", CatalogRevision: &revision, CatalogSHA256: digest}, want: digest},
		{name: "stale pin can be repaired from observed digest", report: mcpCatalogHealthReport{Supported: true, HTTPStatus: http.StatusServiceUnavailable, Status: "degraded", CatalogRevision: &revision, CatalogSHA256: digest}, want: digest},
		{name: "incomplete contract fails closed", report: mcpCatalogHealthReport{Supported: true, CatalogSHA256: digest}, wantErr: true},
		{name: "unexpected degraded status fails closed", report: mcpCatalogHealthReport{Supported: true, HTTPStatus: http.StatusOK, Status: "degraded", CatalogRevision: &revision, CatalogSHA256: digest}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mcpCatalogDigestForPin(tc.report)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("digest=%q err=%v, want digest=%q error=%t", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestValidateMCPDeploymentCatalogPin(t *testing.T) {
	digest := strings.Repeat("a", 64)
	revision := int64(7)
	pin := &mcpCatalogPinReceipt{DiscoveryDeploymentID: "discover", SHA256: digest}
	for _, tc := range []struct {
		name    string
		pin     *mcpCatalogPinReceipt
		health  mcpCatalogHealthReport
		wantErr bool
	}{
		{name: "no pin retains compatibility", health: mcpCatalogHealthReport{}},
		{name: "matching healthy revision", pin: pin, health: mcpCatalogHealthReport{Supported: true, Healthy: true, HTTPStatus: http.StatusOK, Status: "ok", CatalogRevision: &revision, CatalogSHA256: digest}},
		{name: "health contract disappeared", pin: pin, health: mcpCatalogHealthReport{}, wantErr: true},
		{name: "final catalog changed", pin: pin, health: mcpCatalogHealthReport{Supported: true, Healthy: true, HTTPStatus: http.StatusOK, Status: "ok", CatalogRevision: &revision, CatalogSHA256: strings.Repeat("b", 64)}, wantErr: true},
		{name: "pinned catalog unhealthy", pin: pin, health: mcpCatalogHealthReport{Supported: true, HTTPStatus: http.StatusServiceUnavailable, Status: "degraded", CatalogRevision: &revision, CatalogSHA256: digest, Error: "HTTP 503"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMCPDeploymentCatalogPin(tc.pin, tc.health)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v, want error=%t", err, tc.wantErr)
			}
		})
	}
}

func TestValidateMCPDeploymentCatalogFleetPin(t *testing.T) {
	digest := strings.Repeat("c", 64)
	revision := int64(9)
	pin := &mcpCatalogPinReceipt{DiscoveryDeploymentID: "discovery", SHA256: digest}
	good := mcpCatalogHealthReport{
		Supported: true, Healthy: true, HTTPStatus: http.StatusOK, Status: "ok",
		InstanceID: "instance-a", CatalogRevision: &revision, CatalogSHA256: digest,
	}
	for _, tc := range []struct {
		name    string
		fleet   mcpCatalogFleetHealthReport
		wantErr bool
	}{
		{
			name: "all replicas pinned",
			fleet: mcpCatalogFleetHealthReport{
				Supported: true, Healthy: true, ExpectedReplicas: 1, CheckedReplicas: 1,
				Replicas: []mcpCatalogHealthReport{good},
			},
		},
		{
			name: "missing replica blocks",
			fleet: mcpCatalogFleetHealthReport{
				Supported: true, ExpectedReplicas: 2, CheckedReplicas: 1,
				Replicas: []mcpCatalogHealthReport{good}, MissingInstanceIDs: []string{"instance-b"},
				Error: "catalog health observed 1 of 2 running replicas",
			},
			wantErr: true,
		},
		{
			name: "replica digest mismatch blocks",
			fleet: mcpCatalogFleetHealthReport{
				Supported: true, Healthy: true, ExpectedReplicas: 1, CheckedReplicas: 1,
				Replicas: []mcpCatalogHealthReport{{
					Supported: true, Healthy: true, HTTPStatus: http.StatusOK, Status: "ok",
					InstanceID: "instance-b", CatalogRevision: &revision, CatalogSHA256: strings.Repeat("d", 64),
				}},
			},
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMCPDeploymentCatalogFleetPin(pin, tc.fleet)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v, want error=%t", err, tc.wantErr)
			}
		})
	}
}
