package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	fixtureUpload = "111111111111111111111111"
	fixtureDeploy = "22222222-2222-4222-8222-222222222222"
	fixtureApp    = "33333333-3333-4333-8333-333333333333"
)

func testRelay(t *testing.T, upstream http.Handler) (*relay, *httptest.Server) {
	t.Helper()
	target := httptest.NewServer(upstream)
	t.Cleanup(target.Close)
	handler, err := newRelay(target.URL, "fixture-credential")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(handler.transport.CloseIdleConnections)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return handler, server
}

type relayResult struct {
	StatusCode int
	Body       []byte
}

func relayRequest(t *testing.T, server *httptest.Server, method, path, token, body string) relayResult {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		t.Fatal(err)
	}
	return relayResult{StatusCode: response.StatusCode, Body: data}
}

func TestFaultsAndMutationGuardNeverReachUpstream(t *testing.T) {
	var calls atomic.Int32
	handler, server := testRelay(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, mode := range []string{"old-api", "old-gateway", "outage"} {
		if _, err := handler.command([]byte(`{"mode":"` + mode + `"}`)); err != nil {
			t.Fatal(err)
		}
		response := relayRequest(t, server, http.MethodGet, capabilityPath, "fixture-credential", "")
		var body map[string]any
		if err := json.Unmarshal(response.Body, &body); err != nil {
			t.Fatal(err)
		}
		if mode == "old-api" {
			if response.StatusCode != http.StatusNotFound {
				t.Fatal("older API was not missing its capability endpoint")
			}
		} else {
			reason := "gateway_unsupported"
			if mode == "outage" {
				reason = "gateway_unavailable"
			}
			if response.StatusCode != http.StatusOK || body["available"] != false || body["unavailable_reason"] != reason {
				t.Fatal("gateway failure did not preserve its distinct capability reason")
			}
		}
	}
	for _, request := range []struct{ method, path string }{
		{http.MethodPost, "/v1/apps"}, {http.MethodPut, "/v1/apps/fixture/secrets/KEY"},
		{http.MethodPatch, "/v1/uploads/session"}, {http.MethodPatch, "/v1/apps/fixture"},
	} {
		response := relayRequest(t, server, request.method, request.path, "fixture-credential", "private fixture body")
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatal("mutation guard permitted a write")
		}
	}
	state, err := handler.command([]byte("{}"))
	if err != nil || calls.Load() != 0 || state.Counts.CapabilityFaults != 3 || state.Counts.Writes != 4 ||
		state.Counts.BlockedWrites != 4 || state.Counts.ForwardedWrites != 0 ||
		state.Counts.AppCreates != 1 || state.Counts.SecretWrites != 1 || state.Counts.SourceWrites != 1 || state.Counts.OtherWrites != 1 {
		t.Fatalf("fault audit differs: %+v err=%v upstream=%d", state, err, calls.Load())
	}
	wire, _ := json.Marshal(state)
	if strings.Contains(string(wire), "fixture-credential") || strings.Contains(string(wire), "private fixture body") {
		t.Fatal("relay audit contains private request data")
	}
}

func TestHealthyRelayStreamsChunkedSourceAndPreservesTarget(t *testing.T) {
	body := strings.Repeat("accepted-source-", 4096)
	handler, server := testRelay(t, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/apps/fixture/uploads" || request.URL.RawQuery != "scope=staging" ||
			request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer fixture-credential" ||
			request.Header.Get("Proxy-Authorization") != "" {
			t.Error("relay changed the selected API operation or leaked proxy credentials")
		}
		data, err := io.ReadAll(request.Body)
		if err != nil || string(data) != body {
			t.Error("relay did not preserve the chunked source body")
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "accepted")
	}))
	if _, err := handler.command([]byte(`{"mode":"healthy","block_writes":false}`)); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/v1/apps/fixture/uploads?scope=staging", io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer fixture-credential")
	request.Header.Set("Proxy-Authorization", "must-not-leave-the-relay")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	wire, _ := io.ReadAll(response.Body)
	state, _ := handler.command([]byte("{}"))
	if response.StatusCode != http.StatusAccepted || string(wire) != "accepted" || state.Counts.ForwardedWrites != 1 {
		t.Fatal("healthy baseline did not receive its actual acceptance reply")
	}
}

func TestCommitFaultsWithholdAcceptanceAndRecoveryCannotWrite(t *testing.T) {
	for _, mode := range []string{"lost-commit-reply", "hold-commit-reply"} {
		t.Run(mode, func(t *testing.T) {
			var commits atomic.Int32
			handler, server := testRelay(t, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodPost {
					if commits.Add(1) == 1 {
						w.WriteHeader(http.StatusCreated)
						_, _ = io.WriteString(w, `{"id":"`+fixtureDeploy+`","app_id":"`+fixtureApp+`","private":"must-not-be-recorded"}`)
					} else {
						w.WriteHeader(http.StatusConflict)
						_, _ = io.WriteString(w, `{"deployment_id":"`+fixtureDeploy+`"}`)
					}
					return
				}
				_, _ = io.WriteString(w, `{"deployment_id":"`+fixtureDeploy+`","status":"committed"}`)
			}))
			if _, err := handler.command([]byte(`{"mode":"` + mode + `","block_writes":false}`)); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/uploads/"+fixtureUpload+"/commit", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer fixture-credential")
			done := make(chan error, 1)
			go func() {
				response, callErr := server.Client().Do(request)
				if response != nil {
					_ = response.Body.Close()
				}
				done <- callErr
			}()
			var state snapshot
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				state, err = handler.command([]byte("{}"))
				if err != nil || state.Commit != nil {
					break
				}
				time.Sleep(time.Millisecond)
			}
			want := commitReceipt{UploadID: fixtureUpload, DeploymentID: fixtureDeploy, AppID: fixtureApp}
			if err != nil || state.Commit == nil || *state.Commit != want || state.CommitError || state.Counts.CommitFaults != 1 {
				t.Fatalf("relay did not observe real server acceptance: %+v err=%v", state, err)
			}
			if mode == "hold-commit-reply" {
				select {
				case <-done:
					t.Fatal("held reply completed before the CLI connection was killed")
				default:
				}
				cancel()
			}
			select {
			case callErr := <-done:
				if callErr == nil {
					t.Fatal("commit acceptance reached the CLI")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("commit fault did not close after disconnect")
			}
			if mode == "lost-commit-reply" {
				retry := request.Clone(t.Context())
				response, callErr := server.Client().Do(retry)
				if response != nil {
					_ = response.Body.Close()
				}
				if callErr == nil {
					t.Fatal("already-committed retry leaked acceptance to the CLI")
				}
			}
			before, err := handler.command([]byte(`{"mode":"healthy","block_writes":true}`))
			if err != nil {
				t.Fatal(err)
			}
			recovered := relayRequest(t, server, http.MethodGet, "/v1/uploads/"+fixtureUpload, "fixture-credential", "")
			if recovered.StatusCode != http.StatusOK || !strings.Contains(string(recovered.Body), fixtureDeploy) {
				t.Fatal("healthy discovery did not identify the server's exact committed deployment")
			}
			state, _ = handler.command([]byte("{}"))
			if state.Counts.Writes != before.Counts.Writes || state.Counts.UploadReads != before.Counts.UploadReads+1 ||
				state.Counts.CapabilityFaults != 0 || state.Commit == nil || *state.Commit != want {
				t.Fatal("recovery changed write accounting or committed identity")
			}
			wire, _ := json.Marshal(state)
			if strings.Contains(string(wire), "must-not-be-recorded") || strings.Contains(string(wire), "fixture-credential") {
				t.Fatal("commit audit retained private response data")
			}
			blocked := relayRequest(t, server, http.MethodPost, "/v1/uploads/"+fixtureUpload+"/commit", "fixture-credential", "")
			if blocked.StatusCode != http.StatusServiceUnavailable || commits.Load() != int32(before.Counts.ForwardedWrites) {
				t.Fatal("recovery guard forwarded another commit")
			}
		})
	}
}

func TestCommitFaultRejectsInvalidOrContradictoryReceipt(t *testing.T) {
	for _, body := range []string{
		`{"id":"not-an-id","app_id":"` + fixtureApp + `"}`,
		`{"id":"` + fixtureDeploy + `"}`,
		strings.Repeat("x", (64<<10)+1),
	} {
		t.Run(body[:12], func(t *testing.T) {
			handler, server := testRelay(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, body)
			}))
			_, _ = handler.command([]byte(`{"mode":"lost-commit-reply","block_writes":false}`))
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/v1/uploads/"+fixtureUpload+"/commit", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer fixture-credential")
			response, _ := server.Client().Do(request)
			if response != nil {
				_ = response.Body.Close()
			}
			state, _ := handler.command([]byte("{}"))
			if !state.CommitError || state.Commit != nil {
				t.Fatal("invalid commit body became authoritative acceptance evidence")
			}
		})
	}
	t.Run("conflicting-acceptances", func(t *testing.T) {
		var calls atomic.Int32
		handler, server := testRelay(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			deployment := fixtureDeploy
			if calls.Add(1) > 1 {
				deployment = "44444444-4444-4444-8444-444444444444"
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"`+deployment+`","app_id":"`+fixtureApp+`"}`)
		}))
		_, _ = handler.command([]byte(`{"mode":"lost-commit-reply","block_writes":false}`))
		for range 2 {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/v1/uploads/"+fixtureUpload+"/commit", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer fixture-credential")
			response, _ := server.Client().Do(request)
			if response != nil {
				_ = response.Body.Close()
			}
		}
		state, _ := handler.command([]byte("{}"))
		if !state.CommitError || state.Commit == nil || state.Commit.DeploymentID != fixtureDeploy {
			t.Fatal("conflicting acceptance replaced the first committed deployment identity")
		}
	})
}

func TestResponseCheckIsReadOnlyButCanFailAfterHealthyPreflight(t *testing.T) {
	handler, server := testRelay(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"protocol_version":1,"available":true}`)
	}))
	if _, err := handler.command([]byte(`{"mode":"response-outage"}`)); err != nil {
		t.Fatal(err)
	}
	capability := relayRequest(t, server, http.MethodGet, capabilityPath, "fixture-credential", "")
	check := relayRequest(t, server, http.MethodPost, "/v1/apps/fixture/deployments/11111111-1111-4111-8111-111111111111/response-check", "fixture-credential", "{}")
	state, _ := handler.command([]byte("{}"))
	if capability.StatusCode != http.StatusOK || check.StatusCode != http.StatusServiceUnavailable ||
		state.Counts.Capabilities != 1 || state.Counts.ResponseFaults != 1 || state.Counts.Writes != 0 {
		t.Fatal("late verification outage changed mutation accounting or preflight")
	}
	if _, err := handler.command([]byte(`{"mode":"healthy"}`)); err != nil {
		t.Fatal(err)
	}
	recovered := relayRequest(t, server, http.MethodPost, "/v1/apps/fixture/deployments/11111111-1111-4111-8111-111111111111/response-check", "fixture-credential", "{}")
	if recovered.StatusCode != http.StatusOK {
		t.Fatal("recovery did not forward the same read-only endpoint")
	}
}

func TestHealthyBaselineCannotCreateAnUnpinnedApp(t *testing.T) {
	var forwarded atomic.Int32
	handler, server := testRelay(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded.Add(1) }))
	if _, err := handler.command([]byte(`{"mode":"healthy","block_writes":false}`)); err != nil {
		t.Fatal(err)
	}
	response := relayRequest(t, server, http.MethodPost, "/v1/apps", "fixture-credential", "{}")
	state, _ := handler.command([]byte("{}"))
	if response.StatusCode != http.StatusServiceUnavailable || forwarded.Load() != 0 ||
		state.Counts.AppCreates != 1 || state.Counts.BlockedWrites != 1 || state.Counts.ForwardedWrites != 0 {
		t.Fatal("healthy baseline created an app outside the pinned receipt")
	}
}

func TestStreamingEventsFlushBeforeUpstreamFinishes(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	_, server := testRelay(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	defer once.Do(func() { close(release) })
	client := *server.Client()
	client.Timeout = 2 * time.Second
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer fixture-credential")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatal("relay buffered streaming deployment progress")
	}
}

func TestRelayRejectsCredentialsAndInvalidControlWithoutForwarding(t *testing.T) {
	var calls atomic.Int32
	handler, server := testRelay(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	if relayRequest(t, server, http.MethodGet, "/v1/account", "wrong-credential", "").StatusCode != http.StatusForbidden || calls.Load() != 0 {
		t.Fatal("unauthorized local caller reached the upstream")
	}
	for _, command := range []string{`{"mode":"arbitrary"}`, `{"token":"private"}`, `{} {}`} {
		if _, err := handler.command([]byte(command)); err == nil {
			t.Fatal("invalid relay control was accepted")
		}
	}
	if handler.transport.Proxy == nil || handler.transport.TLSClientConfig != nil && handler.transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("relay discarded the inherited proxy or TLS verification")
	}
}

func TestUntrustedUpstreamTLSIsNotBypassed(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()
	handler, err := newRelay(upstream.URL, "fixture-credential")
	if err != nil {
		t.Fatal(err)
	}
	defer handler.transport.CloseIdleConnections()
	server := httptest.NewServer(handler)
	defer server.Close()
	response := relayRequest(t, server, http.MethodGet, "/v1/account", "fixture-credential", "")
	if response.StatusCode != http.StatusBadGateway {
		t.Fatal("relay accepted an untrusted upstream certificate")
	}
}

func TestRelayRejectsNonlocalHTTPAndCredentialBearingOrigins(t *testing.T) {
	for _, origin := range []string{"http://api.example.com", "https://user:password@api.example.com", "https://api.example.com/path", "https://api.example.com?credential=value"} {
		if _, err := newRelay(origin, "fixture-credential"); err == nil {
			t.Fatal("relay accepted an unpinned or insecure API origin")
		}
	}
}
