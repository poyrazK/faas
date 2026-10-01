package e2e_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEdgeRuleConvergenceAcrossPublicGatewaysE2E(t *testing.T) {
	const slug = "edge-rule-convergence"
	f := newNormalPathFixtureWithRequest(t, slug, api.PlanHobby, 0,
		api.CreateAppRequest{Slug: slug, Type: string(state.AppTypeApp), RequireAuthn: boolPtr(false)},
		"FAAS_E2E_APID_NODE_NAME=edge-rule-control",
		"FAAS_E2E_GATEWAY_NODE_NAME="+state.DefaultLocalNodeName,
	)
	if f == nil {
		return
	}

	primary := registerEdgeRuleGateway(t, f, state.DefaultLocalNodeName, f.h.GatewayURL)
	secondaryName := "edge-rule-gateway-b"
	secondary := primary
	secondary.ID = ""
	secondary.Name = secondaryName
	missingGatewayTarget := "tcp://127.0.0.1:1"
	secondary.GatewayTargetURL = &missingGatewayTarget
	if _, err := f.store.UpsertComputeNodeFromOperator(f.ctx, secondary); err != nil {
		t.Fatalf("register secondary gateway before startup: %v", err)
	}

	allowLoopback := edgeRuleIPAction(t, "127.0.0.1/32")
	create := api.CreateEdgeRuleRequest{
		MatchHost: f.host,
		MatchPath: "/",
		Kind:      string(state.EdgeRuleKindIP),
		Action:    allowLoopback,
	}

	// A registered but unavailable replica must keep the mutation from being
	// committed. While apid waits for its ACK, the live gateway must fence this
	// host and fail customer requests closed.
	fenceSeen := make(chan struct{}, 1)
	stopProbes := make(chan struct{})
	probeDone := make(chan struct{})
	var stopProbeOnce sync.Once
	stopProbeLoop := func() { stopProbeOnce.Do(func() { close(stopProbes) }) }
	defer func() {
		stopProbeLoop()
		<-probeDone
	}()
	go func() {
		defer close(probeDone)
		client := edgeRuleProbeClient()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopProbes:
				return
			default:
			}
			status, body, err := edgeRuleProbe(client, f.h.EdgeURL(), f.host)
			if err == nil && status == http.StatusServiceUnavailable && strings.Contains(string(body), "Edge policy update in progress") {
				select {
				case fenceSeen <- struct{}{}:
				default:
				}
			}
			select {
			case <-stopProbes:
				return
			case <-ticker.C:
			}
		}
	}()
	time.Sleep(40 * time.Millisecond)
	_, failedStatus, failedBody := edgeRuleAPIRequest(t, f, http.MethodPost, "/v1/apps/"+slug+"/edge-rules", create)
	stopProbeLoop()
	<-probeDone
	if failedStatus != http.StatusServiceUnavailable {
		t.Fatalf("create with an unavailable registered gateway: status=%d body=%s, want 503", failedStatus, failedBody)
	}
	select {
	case <-fenceSeen:
	default:
		t.Fatalf("primary gateway never returned the fail-closed policy-update response during the missing-replica barrier")
	}
	var rulesBeforeStartup int
	if err := f.h.Pool.QueryRow(f.ctx, `select count(*) from edge_rules where app_id = $1 and match_host = $2`, f.app.ID, f.host).Scan(&rulesBeforeStartup); err != nil {
		t.Fatalf("count rules after rejected create: %v", err)
	}
	if rulesBeforeStartup != 0 {
		t.Fatalf("failed prepare persisted %d edge rules, want none", rulesBeforeStartup)
	}
	waitForEdgeRuleFallthrough(t, f.h.EdgeURL(), f.host)

	secondaryPublicURL, secondaryInternalURL := f.h.StartAdditionalGatewayPublic(secondaryName)
	secondaryGatewayTarget := "tcp://" + strings.TrimPrefix(secondaryInternalURL, "http://")
	secondary.GatewayTargetURL = &secondaryGatewayTarget
	if _, err := f.store.UpsertComputeNodeFromOperator(f.ctx, secondary); err != nil {
		t.Fatalf("publish secondary gateway endpoint: %v", err)
	}
	frontends := []string{f.h.EdgeURL(), secondaryPublicURL}

	createdHeaders, createdStatus, createBody := edgeRuleAPIRequest(t, f, http.MethodPost, "/v1/apps/"+slug+"/edge-rules", create)
	if createdStatus != http.StatusCreated {
		t.Fatalf("create edge rule: status=%d body=%s", createdStatus, createBody)
	}
	lastGeneration := assertEdgeRuleMutationActive(t, createdHeaders, 0)
	var rule api.EdgeRuleResponse
	if err := json.Unmarshal(createBody, &rule); err != nil {
		t.Fatalf("decode created edge rule: %v body=%s", err, createBody)
	}
	if rule.ID == "" {
		t.Fatalf("create response omitted edge rule id: %s", createBody)
	}
	for _, frontend := range frontends {
		assertEdgeRuleFallthrough(t, frontend, f.host)
	}

	allowUnreachable := edgeRuleIPAction(t, "192.0.2.10/32")
	updatedHeaders, updatedStatus, updateBody := edgeRuleAPIRequest(t, f, http.MethodPatch, "/v1/edge-rules/"+rule.ID,
		api.UpdateEdgeRuleRequest{Action: &allowUnreachable})
	if updatedStatus != http.StatusOK {
		t.Fatalf("update edge rule: status=%d body=%s", updatedStatus, updateBody)
	}
	lastGeneration = assertEdgeRuleMutationActive(t, updatedHeaders, lastGeneration)
	for _, frontend := range frontends {
		assertEdgeRuleForbidden(t, frontend, f.host)
	}

	deletedHeaders, deletedStatus, deleteBody := edgeRuleAPIRequest(t, f, http.MethodDelete, "/v1/edge-rules/"+rule.ID, nil)
	if deletedStatus != http.StatusNoContent {
		t.Fatalf("delete edge rule: status=%d body=%s", deletedStatus, deleteBody)
	}
	assertEdgeRuleMutationActive(t, deletedHeaders, lastGeneration)
	for _, frontend := range frontends {
		assertEdgeRuleFallthrough(t, frontend, f.host)
	}
}

func registerEdgeRuleGateway(t *testing.T, f *normalPathFixture, nodeName, gatewayURL string) state.ComputeNode {
	t.Helper()
	node, err := f.store.ComputeNodeByName(f.ctx, nodeName)
	if err != nil {
		t.Fatalf("load compute gateway %q: %v", nodeName, err)
	}
	role := "compute-only"
	target := "tcp://" + strings.TrimPrefix(gatewayURL, "http://")
	node.Role = &role
	node.GatewayTargetURL = &target
	if _, err := f.store.UpsertComputeNodeFromOperator(f.ctx, node); err != nil {
		t.Fatalf("register compute gateway %q: %v", nodeName, err)
	}
	return node
}

func edgeRuleIPAction(t *testing.T, allowCIDR string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(api.EdgeRuleIPAction{Allow: []string{allowCIDR}})
	if err != nil {
		t.Fatalf("marshal IP edge rule action: %v", err)
	}
	return raw
}

func edgeRuleAPIRequest(t *testing.T, f *normalPathFixture, method, path string, body any) (http.Header, int, []byte) {
	t.Helper()
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal %s %s: %v", method, path, err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(f.ctx, method, f.h.APIDURL+path, requestBody)
	if err != nil {
		t.Fatalf("new %s %s request: %v", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+f.key)
	req.Header.Set("Content-Type", "application/json")
	client := f.h.HTTPClient()
	client.Timeout = 10 * time.Second
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	return resp.Header.Clone(), resp.StatusCode, bodyBytes
}

func assertEdgeRuleMutationActive(t *testing.T, headers http.Header, previousGeneration int64) int64 {
	t.Helper()
	if got := headers.Get("X-Faas-Edge-Rules-State"); got != "active" {
		t.Fatalf("mutation state header=%q, want active", got)
	}
	generation, err := strconv.ParseInt(headers.Get("X-Faas-Edge-Rules-Generation"), 10, 64)
	if err != nil || generation <= previousGeneration {
		t.Fatalf("mutation generation=%q, want integer greater than %d", headers.Get("X-Faas-Edge-Rules-Generation"), previousGeneration)
	}
	return generation
}

func edgeRuleProbeClient() *http.Client {
	return &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{Proxy: nil},
	}
}

func edgeRuleProbe(client *http.Client, frontend, host string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, frontend+"/", nil)
	if err != nil {
		return 0, nil, err
	}
	req.Host = host
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func waitForEdgeRuleFallthrough(t *testing.T, frontend, host string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	client := edgeRuleProbeClient()
	for time.Now().Before(deadline) {
		status, body, err := edgeRuleProbe(client, frontend, host)
		if err == nil && isEdgeRuleFallthrough(status, body) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("gateway %s remained fenced or rejected the no-rule request after abort", frontend)
}

func assertEdgeRuleFallthrough(t *testing.T, frontend, host string) {
	t.Helper()
	status, body, err := edgeRuleProbe(edgeRuleProbeClient(), frontend, host)
	if err != nil {
		t.Fatalf("probe gateway %s after edge rule mutation: %v", frontend, err)
	}
	if !isEdgeRuleFallthrough(status, body) {
		t.Fatalf("gateway %s did not fall through after edge rule mutation: status=%d body=%s", frontend, status, body)
	}
}

func isEdgeRuleFallthrough(status int, body []byte) bool {
	if status == http.StatusNotFound {
		return true
	}
	var problem api.Problem
	return status == http.StatusServiceUnavailable && json.Unmarshal(body, &problem) == nil && problem.Code == api.CodeCapacity
}

func assertEdgeRuleForbidden(t *testing.T, frontend, host string) {
	t.Helper()
	status, body, err := edgeRuleProbe(edgeRuleProbeClient(), frontend, host)
	if err != nil {
		t.Fatalf("probe gateway %s after edge rule mutation: %v", frontend, err)
	}
	if status != http.StatusForbidden {
		t.Fatalf("gateway %s status=%d after edge rule update, want 403; body=%s", frontend, status, body)
	}
}
