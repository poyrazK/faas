// adr: 429
package gateway

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/chaos"
)

func TestServiceRequestObservationRequiresGuestResponseAndAuthoritativeIdentity(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            int
		handled, scenario bool
	}{
		{"guest success", 200, true, true},
		{"guest error", 503, true, true},
		{"production guest", 200, true, false},
		{"platform error", 503, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accountID, appID, deploymentID := uuid.NewString(), uuid.NewString(), uuid.NewString()
			at := time.Date(2026, 10, 2, 3, 1, 17, 123000000, time.UTC)
			recorder := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true}, nopLog())
			handler := &Handler{requestTelemetry: recorder}
			endpoint := ServiceEndpoint{InstanceID: "selected-instance", NodeID: "selected-node", DeploymentID: deploymentID, CommitSHA: "selected-commit", Port: 8080}
			provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: appID, Endpoints: []ServiceEndpoint{endpoint}}}
			proxy := NewServiceProxy(ServiceProxyConfig{
				Provider: provider, Now: func() time.Time { return at }, ObserveRequest: handler.RecordServiceRequest,
				Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
					runID := ""
					if tc.scenario {
						runID = "verified-run"
					}
					return ServiceTarget{AppID: appID, ScenarioTestRunID: runID}, true, nil
				},
				Authorize: func(context.Context, string, string) (ServiceCaller, error) {
					return ServiceCaller{AppID: "verified-caller", AccountID: accountID}, nil
				},
				Forward: func(target Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if tc.handled {
							recordForwardedFirstByte(r.Context())
						}
						w.WriteHeader(tc.status)
					})
				},
			})
			req := httptest.NewRequest("GET", "/v1/internal/services/worker/private-secret?credential=secret", nil)
			req.Header.Set(ServiceProxyCallerAppHeader, "verified-caller")
			req.Header.Set("x-faas-instance", "spoofed-instance")
			req.Header.Set("x-faas-account", uuid.NewString())
			req.Header.Set("x-gregale-test-run-id", "spoofed-run")
			proxy.ServeHTTP(httptest.NewRecorder(), req)
			rows := recorder.DrainBatch(10)
			if !tc.handled {
				if len(rows) != 0 {
					t.Fatalf("platform response created handled evidence: %+v", rows)
				}
				return
			}
			if len(rows) != 1 {
				t.Fatalf("rows=%+v", rows)
			}
			row := rows[0]
			if row.AccountID.String() != accountID || row.AppID.String() != appID || row.DeploymentID.String() != deploymentID || row.InstanceID != endpoint.InstanceID || row.NodeID != endpoint.NodeID || row.CommitSHA != endpoint.CommitSHA || row.Status != tc.status {
				t.Fatalf("lost authoritative target identity: %+v", row)
			}
			if row.Route != otherRouteLabel || row.ConsumerID != "" || row.PlatformTenantID != "" || !row.UsageOutboxed || row.EventID == uuid.Nil || row.Count != 1 || row.preserveExact != tc.scenario || !row.ReceivedAt.Equal(at) {
				t.Fatalf("privacy/accounting/scenario contract: %+v", row)
			}
		})
	}
}

func TestServiceRequestObservationPublishedBeforeStreamCloses(t *testing.T) {
	observed := make(chan ServiceRequestObservation, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	appID := uuid.NewString()
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: appID, Endpoints: []ServiceEndpoint{{InstanceID: "stream-instance", NodeID: "node-1", Port: 8080}}}},
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: appID}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: "caller", AccountID: uuid.NewString()}, nil
		},
		ObserveRequest: func(_ *http.Request, o ServiceRequestObservation) { observed <- o },
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				recordForwardedFirstByte(r.Context())
				w.WriteHeader(200)
				<-release
			})
		},
	})
	req := httptest.NewRequest("GET", "/v1/internal/services/worker/stream", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "caller")
	go func() { defer close(done); proxy.ServeHTTP(httptest.NewRecorder(), req) }()
	defer func() { close(release); <-done }()
	select {
	case o := <-observed:
		if o.Target.InstanceID != "stream-instance" || o.Status != 200 {
			t.Fatalf("observation=%+v", o)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("evidence waited for stream EOF")
	}
	select {
	case <-done:
		t.Fatal("stream ended before the observation assertion")
	default:
	}
}

func TestServiceRequestObservationDoesNotCarryFirstByteAcrossRetries(t *testing.T) {
	appID := uuid.NewString()
	var observations []ServiceRequestObservation
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: appID, Endpoints: []ServiceEndpoint{{InstanceID: "guest-error", NodeID: "node-1", Port: 8080}, {InstanceID: "transport-error", NodeID: "node-1", Port: 8080}}}},
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: appID}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: "caller"}, nil
		},
		ObserveRequest: func(_ *http.Request, o ServiceRequestObservation) { observations = append(observations, o) },
		Forward: func(target Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if target.InstanceID == "guest-error" {
					recordForwardedFirstByte(r.Context())
				}
				markStaleTarget(r.Context())
				w.WriteHeader(503)
			})
		},
	})
	req := httptest.NewRequest("GET", "/v1/internal/services/worker/", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "caller")
	proxy.ServeHTTP(httptest.NewRecorder(), req)
	if len(observations) != 1 || observations[0].Target.InstanceID != "guest-error" {
		t.Fatalf("observations=%+v", observations)
	}
}

func TestScenarioRequestTelemetryPreservesExactTimeAndStableEvents(t *testing.T) {
	base := makeCollapseRow(uuid.New(), uuid.New(), uuid.New(), otherRouteLabel, "GET", 200, 17, false, "", time.Date(2026, 10, 2, 3, 1, 17, 0, time.UTC))
	first, second := base, base
	first.EventID, second.EventID = uuid.New(), uuid.New()
	first.InstanceID, second.InstanceID = "instance-a", "instance-b"
	first.preserveExact, second.preserveExact = true, true
	second.ReceivedAt = first.ReceivedAt.Add(150 * time.Millisecond)
	ordinary := base
	ordinary.EventID = uuid.New()
	rows := collapseRequestTelemetry([]RequestTelemetryRow{first, second, ordinary, ordinary})
	if len(rows) != 3 || rows[0].EventID != first.EventID || rows[1].EventID != second.EventID || !rows[0].ReceivedAt.Equal(first.ReceivedAt) || !rows[1].ReceivedAt.Equal(second.ReceivedAt) || rows[0].LatencyMS != 17 || rows[0].Count != 1 || rows[1].InstanceID != second.InstanceID {
		t.Fatalf("scenario evidence was collapsed: %+v", rows)
	}
	if rows[2].Count != 2 || !rows[2].ReceivedAt.Equal(base.ReceivedAt.Truncate(time.Minute)) || rows[2].LatencyMS != 20 {
		t.Fatalf("ordinary aggregation changed: %+v", rows[2])
	}
	retry := collapseRequestTelemetry(rows)
	if retry[0].EventID != first.EventID || retry[1].EventID != second.EventID || !retry[1].ReceivedAt.Equal(second.ReceivedAt) {
		t.Fatalf("retry changed evidence: %+v", retry)
	}
}

func TestServiceRequestTelemetryRejectsInvalidIdentity(t *testing.T) {
	valid := ServiceRequestObservation{AccountID: uuid.NewString(), Target: Target{AppID: uuid.NewString(), DeploymentID: uuid.NewString(), InstanceID: "selected"}, Status: 200, ReceivedAt: time.Now()}
	for _, tc := range []struct {
		name   string
		change func(*ServiceRequestObservation)
	}{
		{"account", func(o *ServiceRequestObservation) { o.AccountID = "spoof" }},
		{"app", func(o *ServiceRequestObservation) { o.Target.AppID = uuid.Nil.String() }},
		{"deployment", func(o *ServiceRequestObservation) { o.Target.DeploymentID = "spoof" }},
		{"instance", func(o *ServiceRequestObservation) { o.Target.InstanceID = "" }},
		{"time", func(o *ServiceRequestObservation) { o.ReceivedAt = time.Time{} }},
		{"status", func(o *ServiceRequestObservation) { o.Status = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true}, nopLog())
			o := valid
			tc.change(&o)
			(&Handler{requestTelemetry: recorder}).RecordServiceRequest(httptest.NewRequest("GET", "/", nil), o)
			if recorder.PendingCount() != 0 {
				t.Fatal("invalid identity reached recorder")
			}
		})
	}
}

func TestServiceRequestObservationRejectsSyntheticChaosResponse(t *testing.T) {
	appID := uuid.NewString()
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: &serviceProxyProvider{},
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: appID, ScenarioTestRunID: "verified-run"}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: "caller"}, nil
		},
		ResolveChaos: func(context.Context, string, string, string) (chaos.Lease, error) {
			return chaos.Lease{ExpiresAt: time.Now().Add(time.Minute), Rules: []chaos.Rule{{From: "caller", To: "worker", Kind: chaos.KindHTTPStatus, Percent: 100, StatusCode: 503}}}, nil
		},
		Forward:        func(Target) http.Handler { t.Fatal("synthetic chaos forwarded to guest"); return nil },
		ObserveRequest: func(*http.Request, ServiceRequestObservation) { t.Fatal("synthetic fault became handled evidence") },
	})
	req := httptest.NewRequest("GET", "/v1/internal/services/worker/", nil)
	req.Header.Set(ServiceProxyCallerAppHeader, "caller")
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, req)
	if response.Code != 503 || response.Header().Get("X-Gregale-Chaos-Injected") != chaos.KindHTTPStatus {
		t.Fatalf("response=%d/%v", response.Code, response.Header())
	}
}

func TestServiceRequestObservationMarksActualWake(t *testing.T) {
	appID := uuid.NewString()
	provider := &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: appID}}
	var observed []ServiceRequestObservation
	wakes := 0
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: provider,
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: appID}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: "caller"}, nil
		},
		Wake: func(context.Context, string) error {
			wakes++
			provider.snapshot.Endpoints = []ServiceEndpoint{{InstanceID: "woken-instance", NodeID: "node-1", Port: 8080}}
			return nil
		},
		ObserveRequest: func(_ *http.Request, o ServiceRequestObservation) { observed = append(observed, o) },
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				recordForwardedFirstByte(r.Context())
				w.WriteHeader(200)
			})
		},
	})
	for range 2 {
		req := httptest.NewRequest("GET", "/v1/internal/services/worker/", nil)
		req.Header.Set(ServiceProxyCallerAppHeader, "caller")
		proxy.ServeHTTP(httptest.NewRecorder(), req)
	}
	if wakes != 1 || len(observed) != 2 || !observed[0].ColdBoot || observed[1].ColdBoot {
		t.Fatalf("wakes=%d observations=%+v", wakes, observed)
	}
}

func TestServiceRequestObservationOnRealUpgradeSocketBeforeDisconnect(t *testing.T) {
	appID := uuid.NewString()
	observed := make(chan ServiceRequestObservation, 1)
	finished := make(chan struct{})
	proxy := NewServiceProxy(ServiceProxyConfig{
		Provider: &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: appID, Endpoints: []ServiceEndpoint{{InstanceID: "socket-instance", NodeID: "node-1", Port: 8080}}}},
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: appID, WebSocketEnabled: true}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: "caller"}, nil
		},
		Forward: func(Target) http.Handler {
			return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("upgrade used ordinary forwarding") })
		},
		RawForward: func(target Target) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rawStreamOnceWithEvents(w, r, &upgradeWireClient{}, slog.Default(), target, nil, nil)
			})
		},
		ObserveRequest: func(_ *http.Request, o ServiceRequestObservation) { observed <- o },
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(finished); proxy.ServeHTTP(w, r) }))
	defer server.Close()
	conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "GET /v1/internal/services/worker/socket HTTP/1.1\r\nHost: localhost\r\nX-Faas-Caller-App: caller\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 101 {
		t.Fatalf("upgrade=%d", response.StatusCode)
	}
	select {
	case o := <-observed:
		if o.Status != 101 || o.Target.InstanceID != "socket-instance" {
			t.Fatalf("observation=%+v", o)
		}
	case <-time.After(time.Second):
		t.Fatal("upgrade evidence waited for socket EOF")
	}
	select {
	case <-finished:
		t.Fatal("socket ended before evidence")
	default:
	}
	if _, err := conn.Write([]byte("binary-roundtrip")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("binary-roundtrip"))
	if _, err := io.ReadFull(reader, got); err != nil || string(got) != "binary-roundtrip" {
		t.Fatalf("roundtrip=%q/%v", got, err)
	}
	_ = conn.Close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("socket disconnect did not cancel forwarding")
	}
}
