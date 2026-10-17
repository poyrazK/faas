// adr: 20261017201006
package outbound

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/outbound/relaywire"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

const simulationTestRun = "abababababababababababababababab"

type simulationFixture struct {
	store            state.Store
	recorderID       string
	recorderIdentity rehearsalrecorder.Identity
	pool             *pgxpool.Pool
	req              api.ScenarioSimulationRequest
	plan             api.ScenarioSimulationPlan
	server           *http.Server
	addr, token      string
	signer           *workloadidentity.Signer
	verifier         *WorkloadIdentityVerifier
	providerCalls    *atomic.Int64
}

func simulationTestPlan() api.ScenarioSimulationPlan {
	return api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{
		{Workload: "primary", Name: "notification", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", CaptureFields: []string{"to", "subject"}, Responses: []api.ScenarioSimulationResponse{{Fault: "timeout", DelayMS: 20}, {Status: 200, Body: `{"id":"simulated-email-1"}`}}},
		{Workload: "primary", Name: "http-import", Integration: "imports", Adapter: "http", Method: "POST", Path: "/imports", CaptureFields: []string{"order_id"}, Responses: []api.ScenarioSimulationResponse{{Status: 429, RetryAfterSeconds: 1, Body: `{"error":"rate_limit"}`}, {Status: 200, Body: `{"ok":true}`}}},
	}}
}

func newSimulationFixture(t *testing.T, backend string, captureOnly ...bool) *simulationFixture {
	t.Helper()
	return newSimulationFixtureWithRecorder(t, backend, true, captureOnly...)
}

func newSimulationFixtureWithRecorder(t *testing.T, backend string, enrollRecorder bool, captureOnly ...bool) *simulationFixture {
	t.Helper()
	f := &simulationFixture{plan: simulationTestPlan(), providerCalls: &atomic.Int64{}}
	if backend == "postgres" {
		f.pool = pgtest.OpenMigrated(t)
		f.store = state.NewPgStore(f.pool)
	} else {
		f.store = state.NewMemStore()
	}
	account, err := f.store.CreateAccount(t.Context(), uuid.NewString()+"@simulation.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	app, err := f.store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "sim-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, PreviewOfSlug: "source", PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := api.NormalizeScenarioSimulationPlan(f.plan, []string{"primary"})
	if err != nil {
		t.Fatal(err)
	}
	if len(captureOnly) > 0 && captureOnly[0] {
		body = nil
	}
	if err = f.store.RegisterScenarioTestMembers(t.Context(), account.ID, simulationTestRun, []state.ScenarioTestMember{{AppID: app.ID, Workload: "primary", WebhookMode: "capture", NetworkMode: "contained", SimulationJSON: string(body)}}); err != nil {
		t.Fatal(err)
	}
	dep, err := f.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:simulation", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	node := "simulation-node"
	if pg, ok := f.store.(*state.PgStore); ok {
		n, err := pg.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
		if err != nil {
			t.Fatal(err)
		}
		node = n.ID
	}
	inst, err := f.store.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 128, node, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > 0 {
		f.recorderIdentity = rehearsalrecorder.Identity{AccountID: account.ID, AppID: app.ID, RunID: simulationTestRun, InstanceID: inst.ID, DeploymentID: dep.ID}
		if enrollRecorder {
			f.recorderID, err = f.store.(rehearsalrecorder.Store).StartScenarioRecorder(t.Context(), f.recorderIdentity)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	f.req = api.ScenarioSimulationRequest{AccountID: account.ID, AppID: app.ID, InstanceID: inst.ID, RunID: simulationTestRun}
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.providerCalls.Add(1); w.WriteHeader(200) }))
	t.Cleanup(provider.Close)
	integration, err := NewIntegration("email", provider.URL, "live-secret", []string{app.ID}, 100, 10, 10, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	handler.SimulationHandler = &SimulationHandler{Store: f.store.(SimulationStore)}
	signer, jwks := testWorkloadSigner(t, workloadidentity.DefaultIssuer)
	f.signer = signer
	f.verifier, err = NewWorkloadIdentityVerifier(jwks, workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.addr = listener.Addr().String()
	relay, err := NewRelayListener(listener, f.verifier, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.server, err = NewRelayServer(handler, f.verifier, 5*time.Second, 10*time.Second, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = f.server.Serve(relay) }()
	t.Cleanup(func() { _ = f.server.Close() })
	token, err := f.signer.MintRehearsalRelay(time.Now(), account.ID, app.ID, inst.ID, simulationTestRun)
	if err != nil {
		t.Fatal(err)
	}
	f.token = token.BearerValue()
	return f
}

type simulationHTTPResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

func (f *simulationFixture) send(t *testing.T, token, path, key, body string, requestIDs ...string) (*simulationHTTPResponse, error) {
	t.Helper()
	conn, err := net.Dial("tcp", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	requestID := ""
	if len(requestIDs) > 0 {
		requestID = requestIDs[0]
	} else {
		requestID = f.requestID(t, true)
	}
	if err = relaywire.Write(conn, relaywire.Preamble{Assertion: token, RecorderRequestID: requestID}); err != nil {
		return nil, err
	}
	request := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nContent-Type: application/json\r\nIdempotency-Key: %s\r\nAuthorization: Bearer live-secret\r\nX-Gregale-App-ID: forged\r\nContent-Length: %d\r\n\r\n%s", path, key, len(body), body)
	if _, err = io.WriteString(conn, request); err != nil {
		return nil, err
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	b, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	return &simulationHTTPResponse{StatusCode: response.StatusCode, Header: response.Header, Body: b}, nil
}

const simulatedEmailBody = `{"from":"test@example.test","to":["customer@example.test"],"subject":"Import ready","text":"Done","secret":"body-secret"}`

func (f *simulationFixture) emailRetry(t *testing.T, key string) {
	t.Helper()
	if response, err := f.send(t, f.token, "/i/email/emails", key, simulatedEmailBody); err == nil {
		t.Fatalf("timeout returned a response: %+v", response)
	}
	response, err := f.send(t, f.token, "/i/email/emails", key, simulatedEmailBody)
	if err != nil {
		t.Fatal(err)
	}
	b := response.Body
	if response.StatusCode != 200 || string(b) != `{"id":"simulated-email-1"}` || response.Header.Get("X-Gregale-Rehearsal") != "simulated" {
		t.Fatalf("fixture response: %d %s", response.StatusCode, b)
	}
}

func (f *simulationFixture) requestID(t *testing.T, refusedAllowed ...bool) string {
	t.Helper()
	id, err := f.store.(rehearsalrecorder.Store).BeginScenarioRecorderRequest(t.Context(), f.recorderIdentity, f.recorderID)
	if err != nil {
		if len(refusedAllowed) > 0 {
			return uuid.NewString()
		}
		t.Fatal(err)
	}
	return id
}

func (f *simulationFixture) seal(t *testing.T) api.ScenarioActionSnapshot {
	t.Helper()
	s, err := f.store.(state.ScenarioActionSnapshotStore).SealScenarioTestActions(t.Context(), f.req.AccountID, f.req.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status == "pending" && s.PendingRecorderSessions > 0 && s.PendingSimulationAttempts == 0 {
		_, err = f.store.(rehearsalrecorder.Store).SettleScenarioRecorder(t.Context(), f.recorderIdentity, f.recorderID, false, true)
		if err != nil {
			t.Fatal(err)
		}
		s, err = f.store.(state.ScenarioActionSnapshotStore).SealScenarioTestActions(t.Context(), f.req.AccountID, f.req.RunID)
		if err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func simulationEvidence(s api.ScenarioActionSnapshot) *actioncompare.Evidence {
	return &actioncompare.Evidence{Version: 1, Status: "complete", Provenance: actioncompare.SimulationProvenance, ContractSHA256: strings.Repeat("c", 64), FixtureSHA256: strings.Repeat("f", 64), Snapshot: &s}
}

func TestRehearsalSimulationRetryComparisonNeverContactsProvider(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			before, after := newSimulationFixture(t, backend), newSimulationFixture(t, backend)
			before.emailRetry(t, "import-123")
			after.emailRetry(t, "import-123")
			after.emailRetry(t, "duplicate-123")
			left, right := before.seal(t), after.seal(t)
			comparison := actioncompare.Compare(simulationEvidence(left), simulationEvidence(right))
			if comparison.Status != "failed" || comparison.BeforeCount == nil || *comparison.BeforeCount != 2 || *comparison.AfterCount != 4 {
				t.Fatalf("duplicate notification lost: %+v", comparison)
			}
			if len(left.SimulatedAttempts) != 2 || left.SimulatedAttempts[0].IdempotencySHA256 != left.SimulatedAttempts[1].IdempotencySHA256 {
				t.Fatalf("retry identity lost: %+v", left)
			}
			body, _ := json.Marshal(right)
			for _, secret := range []string{"body-secret", "live-secret", "Bearer", "forged"} {
				if bytes.Contains(body, []byte(secret)) {
					t.Fatalf("exported credential or excluded field %q", secret)
				}
			}
			if before.providerCalls.Load() != 0 || after.providerCalls.Load() != 0 {
				t.Fatal("simulation reached a provider")
			}
			if response, err := after.send(t, after.token, "/i/email/emails", "late", simulatedEmailBody); err != nil || response.StatusCode != 503 {
				t.Fatalf("sealed relay reopened: %+v %v", response, err)
			}
			original, _ := json.Marshal(right)
			if _, err := after.store.SoftDeleteAppCascade(t.Context(), after.req.AppID); err != nil {
				t.Fatal(err)
			}
			if err := after.store.DeleteScenarioTestMembers(t.Context(), after.req.AccountID, after.req.RunID); err != nil {
				t.Fatal(err)
			}
			retained, _ := json.Marshal(after.seal(t))
			if !bytes.Equal(original, retained) {
				t.Fatal("cleanup changed sealed simulation evidence")
			}
		})
	}
}

func TestRehearsalSimulationWaitsForInflightAndRefusesUnsupportedCoverage(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newSimulationFixture(t, backend)
			req := f.req
			req.Integration, req.Method, req.Path, req.Body = "imports", "POST", "/imports", []byte(`{"order_id":123}`)
			store := f.store.(SimulationStore)
			req.RecorderRequestID = f.requestID(t)
			d, err := store.BeginScenarioSimulation(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			if f.pool != nil {
				_, err := f.pool.Exec(t.Context(), `UPDATE scenario_simulation_attempts SET evidence=jsonb_set(evidence,'{payload}','{"order_id":"changed"}') WHERE id=$1`, d.Attempt.ID)
				assertSimulationSQLIdentityFence(t, err)
				_, err = f.pool.Exec(t.Context(), `UPDATE scenario_simulation_attempts SET id=$2::uuid,evidence=jsonb_set(evidence,'{id}',to_jsonb($2::text)) WHERE id=$1`, d.Attempt.ID, uuid.NewString())
				assertSimulationSQLIdentityFence(t, err)
			}
			r, err := f.send(t, f.token, "/i/unknown/private?token=secret-query", "unknown", `{}`)
			if err != nil || r.StatusCode != 422 {
				t.Fatalf("unsupported call: %+v %v", r, err)
			}
			if s := f.seal(t); s.Status != "pending" || s.PendingSimulationAttempts != 1 {
				t.Fatalf("inflight request sealed: %+v", s)
			}
			if err = store.FinishScenarioSimulation(t.Context(), req, d.Attempt.ID, "completed"); err != nil {
				t.Fatal(err)
			}
			s := f.seal(t)
			if s.Status != "incomplete" || s.IncompleteSimulationAttempts != 1 {
				t.Fatalf("unsupported call passed: %+v", s)
			}
			if c := actioncompare.Compare(simulationEvidence(s), simulationEvidence(s)); c.Status != "inconclusive" || c.Scope != api.ScenarioSimulationScope {
				t.Fatalf("incomplete comparison lost its declared scope: %+v", c)
			}
			body, _ := json.Marshal(s)
			if bytes.Contains(body, []byte("secret-query")) {
				t.Fatal("stored unknown query value")
			}
			if f.providerCalls.Load() != 0 {
				t.Fatal("unsupported call reached provider")
			}
		})
	}
}

func assertSimulationSQLIdentityFence(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || !strings.Contains(pgErr.Message, "evidence is immutable") {
		t.Fatalf("SQL evidence identity fence did not refuse the mutation: %v", err)
	}
}

func TestRehearsalSimulationIdentityCannotUseLiveRelay(t *testing.T) {
	f := newSimulationFixture(t, "memory")
	if _, err := f.verifier.VerifyRelay(f.token); !errors.Is(err, ErrInvalidWorkloadIdentity) {
		t.Fatalf("simulation token grants live relay: %v", err)
	}
	if _, err := f.verifier.Verify(f.token, "email"); !errors.Is(err, ErrInvalidWorkloadIdentity) {
		t.Fatalf("simulation token grants integration: %v", err)
	}
	wrong, err := f.signer.MintRehearsalRelay(time.Now(), f.req.AccountID, f.req.AppID, f.req.InstanceID, strings.Repeat("c", 32))
	if err != nil {
		t.Fatal(err)
	}
	response, err := f.send(t, wrong.BearerValue(), "/i/email/emails", "forged-run", simulatedEmailBody)
	if err != nil || response.StatusCode != 503 || f.providerCalls.Load() != 0 {
		t.Fatalf("cross-run assertion accepted: %+v %v", response, err)
	}
	if s := f.seal(t); s.Status != "pending" || len(s.SimulatedAttempts) != 0 || s.PendingRecorderSessions != 1 {
		t.Fatalf("rejected call lost its unacknowledged obligation: %+v", s)
	}
}

func TestRehearsalSimulationRateLimitSequenceAndDurableFences(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newSimulationFixture(t, backend)
			for i, want := range []int{429, 200, 200} {
				r, err := f.send(t, f.token, "/i/imports/imports", "import-123", `{"order_id":123,"secret":"excluded"}`)
				if err != nil || r.StatusCode != want {
					t.Fatalf("attempt %d: %+v %v", i, r, err)
				}
				if i == 0 && r.Header.Get("Retry-After") != "1" {
					t.Fatal("rate limit lost Retry-After")
				}
			}
			s := f.seal(t)
			if _, err := actioncompare.ValidateSnapshot(&s); err != nil || len(s.SimulatedAttempts) != 3 {
				t.Fatalf("HTTP evidence: %+v %v", s, err)
			}
			if f.pool != nil {
				if _, err := f.pool.Exec(t.Context(), `UPDATE scenario_test_action_policies SET simulation_plan='{}' WHERE app_id=$1`, f.req.AppID); err == nil {
					t.Fatal("fixture identity could be rewritten")
				}
				body, _ := json.Marshal(s.SimulatedAttempts[0])
				if _, err := f.pool.Exec(t.Context(), `INSERT INTO scenario_simulation_attempts(id,app_id,evidence) VALUES($1,$2,$3)`, uuid.NewString(), f.req.AppID, body); err == nil {
					t.Fatal("late SQL writer reopened sealed window")
				}
			}
		})
	}
}

func TestRehearsalSimulationOverflowAndInterruptedWorkRemainIncomplete(t *testing.T) {
	for _, mode := range []string{"overflow", "interrupted"} {
		t.Run(mode, func(t *testing.T) {
			f := newSimulationFixture(t, "memory")
			store := f.store.(SimulationStore)
			req := f.req
			req.Integration, req.Method, req.Path, req.Body = "imports", "POST", "/imports", []byte(`{}`)
			count := 1
			if mode == "overflow" {
				count = api.MaxScenarioSimulationAttempts
			}
			for range count {
				req.RecorderRequestID = f.requestID(t)
				d, err := store.BeginScenarioSimulation(t.Context(), req)
				if err != nil {
					t.Fatal(err)
				}
				status := "completed"
				if mode == "interrupted" {
					status = "interrupted"
				}
				if err := store.FinishScenarioSimulation(t.Context(), req, d.Attempt.ID, status); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "overflow" {
				if _, err := f.store.(rehearsalrecorder.Store).BeginScenarioRecorderRequest(t.Context(), f.recorderIdentity, f.recorderID); err == nil {
					t.Fatal("unbounded recorder accepted")
				}
				if _, err := store.BeginScenarioSimulation(t.Context(), req); err == nil {
					t.Fatal("unbounded ledger accepted")
				}
			}
			if s := f.seal(t); s.Status != "incomplete" || s.IncompleteSimulationAttempts < 1 {
				t.Fatalf("incomplete coverage passed: %+v", s)
			}
		})
	}
}

type legacyRelayVerifier struct{ verifier *WorkloadIdentityVerifier }

func (v legacyRelayVerifier) VerifyRelay(raw string) (WorkloadIdentity, error) {
	return v.verifier.VerifyRelay(raw)
}
func (v legacyRelayVerifier) VerifyExecutionRelay(raw string) (ExecutionIdentity, error) {
	return v.verifier.VerifyExecutionRelay(raw)
}

func TestRehearsalSimulationLegacyRelayRefusesBeforeHTTP(t *testing.T) {
	f := newSimulationFixture(t, "memory")
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	done := make(chan error, 1)
	go func() { _, err := readRelayPrincipal(server, legacyRelayVerifier{f.verifier}); done <- err }()
	if err := relaywire.Write(client, relaywire.Preamble{Assertion: f.token}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("legacy live relay admitted a simulation token")
	}
}

func TestRehearsalSimulationBypassesLiveURLTracingAndRetainsRefusalInput(t *testing.T) {
	liveCalls, simCalls := 0, 0
	h := WithSimulationRelay(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { liveCalls++ }), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		simCalls++
		if r.URL.RawQuery != "token=excluded" {
			t.Fatal("trace routing changed fixture refusal input")
		}
		if p, ok := relayPrincipalFromContext(r.Context()); !ok || p.rehearsalRunID != simulationTestRun {
			t.Fatal("trace routing lost authenticated principal")
		}
		w.WriteHeader(422)
	}))
	req := httptest.NewRequest("POST", "/i/email/emails?token=excluded", strings.NewReader(`{}`))
	req = req.WithContext(withRelayPrincipal(req.Context(), relayPrincipal{app: WorkloadIdentity{AppID: "app"}, rehearsalRunID: simulationTestRun}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if liveCalls != 0 || simCalls != 1 || w.Code != 422 {
		t.Fatal("simulation passed through live request tracing")
	}
}

func TestRehearsalSimulationPendingSnapshotDoesNotAliasDurablePayload(t *testing.T) {
	f := newSimulationFixture(t, "memory")
	store := f.store.(SimulationStore)
	req := f.req
	req.Integration, req.Method, req.Path, req.Body = "imports", "POST", "/imports", []byte(`{"order_id":123}`)
	req.RecorderRequestID = f.requestID(t)
	d, err := store.BeginScenarioSimulation(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	s := f.seal(t)
	if s.Status != "pending" || len(s.SimulatedAttempts) != 1 {
		t.Fatalf("pending snapshot: %+v", s)
	}
	s.SimulatedAttempts[0].Payload[0] = '!'
	if err := store.FinishScenarioSimulation(t.Context(), req, d.Attempt.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	final := f.seal(t)
	if _, err := actioncompare.ValidateSnapshot(&final); err != nil || string(final.SimulatedAttempts[0].Payload) != `{"order_id":123}` {
		t.Fatalf("caller mutated durable evidence: %+v %v", final, err)
	}
}

func TestRehearsalSimulationRequiresExplicitEnrollmentAndKeepsLegacyScope(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newSimulationFixture(t, backend, true)
			s := f.seal(t)
			if _, err := actioncompare.ValidateSnapshot(&s); err != nil || s.Version != 1 || s.Scope != api.ScenarioActionScope || s.SimulationSHA256 != "" {
				t.Fatalf("capture-only scope changed: %+v %v", s, err)
			}
			r, err := f.send(t, f.token, "/i/email/emails", "unsupported", simulatedEmailBody)
			if err != nil || r.StatusCode != 503 || f.providerCalls.Load() != 0 {
				t.Fatalf("a signed assertion bypassed missing fixture enrollment: %+v %v", r, err)
			}
		})
	}
}

func TestRehearsalSimulationConcurrentAdmissionAndCompletionSealEveryAttempt(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newSimulationFixture(t, backend)
			store := f.store.(SimulationStore)
			req := f.req
			req.Integration, req.Method, req.Path, req.IdempotencyKey, req.Body = "imports", "POST", "/imports", "same-import", []byte(`{"order_id":123}`)
			type result struct {
				decision api.ScenarioSimulationDecision
				request  api.ScenarioSimulationRequest
				err      error
			}
			const count = 8
			results := make(chan result, count)
			start := make(chan struct{})
			for range count {
				request := req
				request.RecorderRequestID = f.requestID(t)
				go func() {
					<-start
					d, err := store.BeginScenarioSimulation(t.Context(), request)
					results <- result{d, request, err}
				}()
			}
			close(start)
			decisions := make([]result, 0, count)
			ordinals := map[int]bool{}
			for range count {
				r := <-results
				if r.err != nil {
					t.Fatal(r.err)
				}
				if ordinals[r.decision.Attempt.Attempt] {
					t.Fatal("concurrent retry ordinal reused")
				}
				ordinals[r.decision.Attempt.Attempt] = true
				decisions = append(decisions, r)
			}
			if s := f.seal(t); s.Status != "pending" || s.PendingSimulationAttempts != count {
				t.Fatalf("concurrent requests sealed early: %+v", s)
			}
			completed := make(chan error, count)
			for _, d := range decisions {
				go func() {
					completed <- store.FinishScenarioSimulation(t.Context(), d.request, d.decision.Attempt.ID, "completed")
				}()
			}
			for range count {
				if err := <-completed; err != nil {
					t.Fatal(err)
				}
			}
			s := f.seal(t)
			if _, err := actioncompare.ValidateSnapshot(&s); err != nil || len(s.SimulatedAttempts) != count {
				t.Fatalf("concurrent request missing from seal: %+v %v", s, err)
			}
		})
	}
}
