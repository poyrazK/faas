package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestComputeMetricsDiscoveryUsesActiveRegistry(t *testing.T) {
	store := state.NewMemStore()
	region := "eu-fsn1"
	zone := "a"
	target := "tcp://fsn-2.gregale.dev:8080"
	if _, err := store.UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{
		Name:               "fsn-2.faas",
		TargetURL:          "tcp://vmmd-2.gregale.dev:50051",
		GatewayTargetURL:   &target,
		VPCPUs:             4,
		MemMB:              8192,
		MaxConcurrency:     16,
		AdmissionCeilingMB: 4096,
		Region:             &region,
		Zone:               &zone,
	}); err != nil {
		t.Fatalf("upsert active node: %v", err)
	}
	inactiveTarget := "tcp://fsn-old.gregale.dev:8080"
	inactive, err := store.UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{
		Name:               "fsn-old.faas",
		TargetURL:          "tcp://vmmd-old.gregale.dev:50051",
		GatewayTargetURL:   &inactiveTarget,
		VPCPUs:             4,
		MemMB:              8192,
		MaxConcurrency:     16,
		AdmissionCeilingMB: 4096,
	})
	if err != nil {
		t.Fatalf("upsert inactive candidate: %v", err)
	}
	if err := store.SetComputeNodeActive(context.Background(), inactive.ID, false); err != nil {
		t.Fatalf("deactivate candidate: %v", err)
	}

	srv := newServer(store, nil, "gregale.dev", nil)
	req := httptest.NewRequest(http.MethodGet, computeMetricsDiscoveryPath, nil)
	req.RemoteAddr = "127.0.0.1:9099"
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got []prometheusTargetGroup
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode discovery response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("target groups=%d, want 1: %s", len(got), rec.Body.String())
	}
	if got[0].Targets[0] != "fsn-2.gregale.dev:8080" {
		t.Errorf("target=%q", got[0].Targets[0])
	}
	if got[0].Labels["node"] != "fsn-2.faas" || got[0].Labels["node_id"] == "" {
		t.Errorf("identity labels=%v", got[0].Labels)
	}
	if got[0].Labels["region"] != region || got[0].Labels["zone"] != zone {
		t.Errorf("locality labels=%v", got[0].Labels)
	}

	// A replacement updates the registry row in place. The next HTTP-SD
	// refresh must expose the new endpoint without an Ansible change.
	replacementTarget := "tcp://fsn-2-replacement.gregale.dev:8080"
	if _, err := store.UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{
		Name:               "fsn-2.faas",
		TargetURL:          "tcp://vmmd-2-replacement.gregale.dev:50051",
		GatewayTargetURL:   &replacementTarget,
		VPCPUs:             4,
		MemMB:              8192,
		MaxConcurrency:     16,
		AdmissionCeilingMB: 4096,
	}); err != nil {
		t.Fatalf("upsert replacement: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, computeMetricsDiscoveryPath, nil)
	req.RemoteAddr = "127.0.0.1:9099"
	rec = httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	var replacement []prometheusTargetGroup
	if err := json.Unmarshal(rec.Body.Bytes(), &replacement); err != nil {
		t.Fatalf("decode replacement response: %v", err)
	}
	if len(replacement) != 1 || replacement[0].Targets[0] != "fsn-2-replacement.gregale.dev:8080" {
		t.Fatalf("replacement targets=%v, want replacement endpoint", replacement)
	}
}

func TestComputeMetricsDiscoveryRejectsNonLoopback(t *testing.T) {
	srv := newServer(state.NewMemStore(), nil, "gregale.dev", nil)
	req := httptest.NewRequest(http.MethodGet, computeMetricsDiscoveryPath, nil)
	req.RemoteAddr = "203.0.113.10:8080"
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}

func TestComputeMetricsDiscoveryScalesTo1000Nodes(t *testing.T) {
	store := state.NewMemStore()
	for i := 0; i < 1000; i++ {
		name := fmt.Sprintf("compute-%04d.faas", i)
		vmmdTarget := fmt.Sprintf("tcp://vmmd-%04d.faas:50051", i)
		target := fmt.Sprintf("tcp://%s:8080", name)
		if _, err := store.UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{
			Name:               name,
			TargetURL:          vmmdTarget,
			GatewayTargetURL:   &target,
			VPCPUs:             4,
			MemMB:              8192,
			MaxConcurrency:     16,
			AdmissionCeilingMB: 4096,
		}); err != nil {
			t.Fatalf("upsert node %d: %v", i, err)
		}
	}

	srv := newServer(store, nil, "gregale.dev", nil)
	req := httptest.NewRequest(http.MethodGet, computeMetricsDiscoveryPath, nil)
	req.RemoteAddr = "127.0.0.1:9099"
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got []prometheusTargetGroup
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode discovery response: %v", err)
	}
	if len(got) != 1000 {
		t.Fatalf("target groups=%d, want 1000", len(got))
	}
	if got[0].Labels["node"] != "compute-0000.faas" || got[999].Labels["node"] != "compute-0999.faas" {
		t.Fatalf("registry order/identity labels not stable: first=%v last=%v", got[0].Labels, got[999].Labels)
	}
}

func TestComputeMetricsTargetValidation(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "hostname", raw: "tcp://fsn-2.gregale.dev:8080", want: true},
		{name: "ipv6", raw: "tcp://[fd00::2]:8080", want: true},
		{name: "wildcard", raw: "tcp://0.0.0.0:8080", want: false},
		{name: "loopback", raw: "tcp://127.0.0.1:8080", want: false},
		{name: "path", raw: "tcp://fsn-2.gregale.dev:8080/metrics", want: false},
		{name: "bad-port", raw: "tcp://fsn-2.gregale.dev:0", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, got := computeMetricsTarget(tc.raw)
			if got != tc.want {
				t.Fatalf("computeMetricsTarget(%q)=%v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestPromtailMetricsTargetReusesComputeHost(t *testing.T) {
	got, ok := promtailMetricsTarget("tcp://fsn-2.gregale.dev:8080")
	if !ok {
		t.Fatal("promtailMetricsTarget rejected a valid compute target")
	}
	if got != "fsn-2.gregale.dev:9080" {
		t.Fatalf("target=%q, want fsn-2.gregale.dev:9080", got)
	}
}

func TestPromtailMetricsDiscoveryUsesActiveRegistry(t *testing.T) {
	store := state.NewMemStore()
	target := "tcp://fsn-2.gregale.dev:8080"
	if _, err := store.UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{
		Name:               "fsn-2.faas",
		TargetURL:          "tcp://vmmd-2.gregale.dev:50051",
		GatewayTargetURL:   &target,
		VPCPUs:             4,
		MemMB:              8192,
		MaxConcurrency:     16,
		AdmissionCeilingMB: 4096,
	}); err != nil {
		t.Fatalf("upsert active node: %v", err)
	}

	srv := newServer(store, nil, "gregale.dev", nil)
	req := httptest.NewRequest(http.MethodGet, promtailMetricsDiscoveryPath, nil)
	req.RemoteAddr = "127.0.0.1:9099"
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got []prometheusTargetGroup
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode discovery response: %v", err)
	}
	if len(got) != 1 || got[0].Targets[0] != "fsn-2.gregale.dev:9080" {
		t.Fatalf("targets=%v, want Promtail endpoint", got)
	}
	if got[0].Labels["job"] != "promtail-compute" {
		t.Fatalf("job label=%q, want promtail-compute", got[0].Labels["job"])
	}
}

func TestComputeMetricsDiscoveryRecordsProducerHealth(t *testing.T) {
	store := state.NewMemStore()
	validTarget := "tcp://fsn-2.gregale.dev:8080"
	if _, err := store.UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{
		Name:               "fsn-2.faas",
		TargetURL:          "tcp://vmmd-2.gregale.dev:50051",
		GatewayTargetURL:   &validTarget,
		VPCPUs:             4,
		MemMB:              8192,
		MaxConcurrency:     16,
		AdmissionCeilingMB: 4096,
	}); err != nil {
		t.Fatalf("upsert valid node: %v", err)
	}
	invalidTarget := "tcp://127.0.0.1:8080"
	if _, err := store.UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{
		Name:               "fsn-invalid.faas",
		TargetURL:          "tcp://vmmd-invalid.gregale.dev:50051",
		GatewayTargetURL:   &invalidTarget,
		VPCPUs:             4,
		MemMB:              8192,
		MaxConcurrency:     16,
		AdmissionCeilingMB: 4096,
	}); err != nil {
		t.Fatalf("upsert invalid node: %v", err)
	}

	ops := wire.NewOpsMetrics("apid_discovery_test")
	srv := newServer(store, nil, "gregale.dev", nil).WithOpsMetrics(context.Background(), ops)
	req := httptest.NewRequest(http.MethodGet, computeMetricsDiscoveryPath, nil)
	req.RemoteAddr = "127.0.0.1:9099"
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	body := scrapeDiscoveryMetrics(t, ops)
	for _, want := range []string{
		`apid_discovery_test_metrics_discovery_requests_total{job="gatewayd-internal",outcome="success"} 1`,
		`apid_discovery_test_metrics_discovery_registry_nodes{job="gatewayd-internal"} 2`,
		`apid_discovery_test_metrics_discovery_targets{job="gatewayd-internal"} 1`,
		`apid_discovery_test_metrics_discovery_invalid_targets{job="gatewayd-internal"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, `apid_discovery_test_metrics_discovery_last_success_timestamp_seconds{job="gatewayd-internal"} `) {
		t.Errorf("metrics body missing last-success timestamp:\n%s", body)
	}

	forbiddenReq := httptest.NewRequest(http.MethodGet, computeMetricsDiscoveryPath, nil)
	forbiddenReq.RemoteAddr = "203.0.113.10:8080"
	forbiddenRec := httptest.NewRecorder()
	srv.handler().ServeHTTP(forbiddenRec, forbiddenReq)
	if forbiddenRec.Code != http.StatusNotFound {
		t.Fatalf("forbidden status=%d, want 404", forbiddenRec.Code)
	}
	body = scrapeDiscoveryMetrics(t, ops)
	if !strings.Contains(body, `apid_discovery_test_metrics_discovery_requests_total{job="gatewayd-internal",outcome="forbidden"} 1`) {
		t.Errorf("metrics body missing forbidden request count:\n%s", body)
	}
}

func scrapeDiscoveryMetrics(t *testing.T, ops *wire.OpsMetrics) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	ops.Handler().ServeHTTP(rec, req)
	b, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read metrics body: %v", err)
	}
	return string(b)
}
