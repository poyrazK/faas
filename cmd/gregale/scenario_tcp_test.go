package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type scenarioTCPClient struct{ request api.UpdateAppRequest }

func (c *scenarioTCPClient) UpdateApp(_ context.Context, _ string, req api.UpdateAppRequest) (api.AppResponse, error) {
	c.request = req
	return api.AppResponse{}, nil
}

func TestScenarioTCPManifestAndServiceReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gregale-test.yaml")
	data := `version: 1
scenarios:
  cache-test:
    project: checkout
    command: [true]
    secrets:
      CACHE_HOST: ${service.cache.host}
      CACHE_PORT: ${service.cache.port}
    services:
      cache:
        fixture: tcp-echo
        tcp_ports: [6379]
    chaos:
      duration: 2m
      rules:
        - from: checkout
          to: cache
          port: 6379
          kind: tcp_bandwidth
          direction: downstream
          rate_kib_per_second: 64
          percent: 100
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := readTestManifestDocument(path, func(string, testScenario) []string { return nil })
	if err != nil {
		t.Fatal(err)
	}
	scenario := manifest.Scenarios["cache-test"]
	plan, err := scenario.Chaos.plan()
	if err != nil || plan.Rules[0].RateKiBPerSecond != 64 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	value, err := expandTestSecretValue("${service.cache.host}:${service.cache.port}", nil, map[string]string{"cache": "dev-cache"}, nil, "run", "secret", map[string][]int{"cache": {6379}})
	if err != nil || value != "cache.svc.gregale:6379" {
		t.Fatalf("address=%q err=%v", value, err)
	}
	data = strings.Replace(data, "port: 6379", "port: 6380", 1)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifestDocument(path, func(string, testScenario) []string { return nil }); err == nil {
		t.Fatal("undeclared fault port accepted")
	}
}

func TestScenarioTCPRelayProvisioningKeepsPortsPrivateAndEgressBounded(t *testing.T) {
	client := &scenarioTCPClient{}
	spec := testService{Fixture: testTCPRelayFixture, TCPPorts: []int{15432}, Upstream: "db.example.com:5432"}
	if err := validateScenarioTCPService(spec); err != nil {
		t.Fatal(err)
	}
	if err := configureScenarioTCP(context.Background(), client, "relay", spec); err != nil {
		t.Fatal(err)
	}
	if client.request.Ports == nil || !(*client.request.Ports)[0].Internal {
		t.Fatal("relay exposed a public listener")
	}
	if client.request.EgressPorts == nil || len(*client.request.EgressPorts) != 1 || (*client.request.EgressPorts)[0] != 5432 {
		t.Fatal("relay did not declare exactly its upstream port")
	}
	for _, bad := range []testService{
		{Fixture: testTCPRelayFixture, TCPPorts: []int{443}, Upstream: "db.example.com:5432"},
		{Fixture: testTCPRelayFixture, TCPPorts: []int{15432}, Upstream: "db.example.com:25"},
		{Fixture: testTCPRelayFixture, TCPPorts: []int{15432}, Upstream: "user:pass@db.example.com:5432"},
		{Fixture: testTCPRelayFixture, TCPPorts: []int{15432}, Upstream: "db.example.com:5432", Secrets: map[string]string{"GREGALE_TEST_TCP_UPSTREAM": "other:443"}},
	} {
		if err := validateScenarioTCPService(bad); err == nil {
			t.Fatalf("invalid relay accepted: %+v", bad)
		}
	}
	for _, fixture := range []string{testTCPRelayFixture, testTCPEchoFixture} {
		dir, cleanup, err := materializeScenarioFixture(fixture)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "server.js")); err != nil {
			t.Fatal(err)
		}
		cleanup()
	}
}

func TestScenarioTCPRelayByteTransport(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for relay fixture integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, node, "--test", "scenario_fixtures/tcp-relay/server.test.mjs").CombinedOutput(); err != nil {
		t.Fatalf("relay integration: %v\n%s", err, output)
	}
}

func TestScenarioTCPWarmEvidenceDoesNotRequireHTTPRequest(t *testing.T) {
	client := &testServiceHotFakeClient{wakes: []api.WakeTimelineJSONRow{{WakeID: "baseline"}}}
	baseline := map[string]bool{"baseline": true}
	evidence, err := verifyTestServiceTCPHot(context.Background(), client, "cache", baseline)
	if err != nil || evidence.Transport != "tcp" || !evidence.NoNewWake || evidence.RequestID != "" {
		t.Fatalf("TCP lifecycle evidence = %+v, %v", evidence, err)
	}
	client.wakes = append(client.wakes, api.WakeTimelineJSONRow{WakeID: "unexpected"})
	if _, err := verifyTestServiceTCPHot(context.Background(), client, "cache", baseline); err == nil {
		t.Fatal("new wake satisfied warm TCP profile")
	}
}

func TestTCPResilienceExampleManifest(t *testing.T) {
	manifest, _, err := readTestManifestDocument("../../examples/scenario-tcp/gregale-test.yaml", func(string, testScenario) []string { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Scenarios) != 4 {
		t.Fatal("example must exercise all four TCP faults")
	}
}
