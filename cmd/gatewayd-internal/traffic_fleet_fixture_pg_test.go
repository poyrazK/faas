//go:build !no_pg

// adr: 570
package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type fleetDaemonVMFixture struct {
	vmmdpb.UnimplementedVmmdServer
	guest        string
	badInstance  string
	mu           sync.Mutex
	requests     map[string][]string
	correlation  map[string][]wire.CorrelationFields
	origins      map[string]int
	guestHeaders map[string][]http.Header
}

func (v *fleetDaemonVMFixture) calls(path string) []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.requests[path]...)
}

func (v *fleetDaemonVMFixture) guestCalls(path string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.origins[path]
}

func (v *fleetDaemonVMFixture) correlations(path string) []wire.CorrelationFields {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]wire.CorrelationFields(nil), v.correlation[path]...)
}

func (v *fleetDaemonVMFixture) ForwardHTTPStream(stream grpc.BidiStreamingServer[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse]) error {
	frame, err := stream.Recv()
	if err != nil {
		return err
	}
	init := frame.GetInit()
	if init == nil {
		return status.Error(codes.InvalidArgument, "fixture requires init")
	}
	v.mu.Lock()
	v.requests[init.RequestUri] = append(v.requests[init.RequestUri], init.Instance)
	fields, _ := wire.CorrelationFromIncoming(stream.Context())
	v.correlation[init.RequestUri] = append(v.correlation[init.RequestUri], fields)
	v.mu.Unlock()
	if init.RequestUri == "/retry" && init.Instance == v.badInstance {
		return status.Error(codes.Unavailable, "fixture transport failure before guest execution")
	}
	request, err := http.NewRequestWithContext(stream.Context(), init.Method, v.guest+init.RequestUri, nil)
	if err != nil {
		return err
	}
	for _, header := range init.Headers {
		request.Header.Add(header.Name, header.Value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	headers := make([]*vmmdpb.Header, 0, len(response.Header))
	for key, values := range response.Header {
		for _, value := range values {
			headers = append(headers, &vmmdpb.Header{Name: key, Value: value})
		}
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{
		Init: &vmmdpb.ForwardHTTPResponseInit{Status: int32(response.StatusCode), Headers: headers},
	}}); err != nil {
		return err
	}
	return stream.Send(&vmmdpb.ForwardHTTPStreamResponse{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: body}})
}

type fleetDaemonFixture struct {
	publicRoutingPGFixture
	apps      []fleetDaemonApp
	nodes     []state.ComputeNode
	usage     string
	vm        *fleetDaemonVMFixture
	telemetry *fleetDaemonTelemetryFixture
}

type fleetDaemonTelemetryFixture struct {
	usageReceiverForTest
	store *state.PgStore
	mu    sync.Mutex
	rows  map[string]*apidpb.IncrementRequestTelemetryRequest
}

func (r *fleetDaemonTelemetryFixture) IncrementRequestTelemetry(stream grpc.BidiStreamingServer[apidpb.IncrementRequestTelemetryRequest, apidpb.IncrementRequestTelemetryResponse]) error {
	for {
		row, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		r.mu.Lock()
		r.rows[row.EventId] = proto.Clone(row).(*apidpb.IncrementRequestTelemetryRequest)
		r.mu.Unlock()
		if err := stream.Send(&apidpb.IncrementRequestTelemetryResponse{Outcome: "inserted"}); err != nil {
			return err
		}
	}
}

func (r *fleetDaemonTelemetryFixture) records() []*apidpb.IncrementRequestTelemetryRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := make([]*apidpb.IncrementRequestTelemetryRequest, 0, len(r.rows))
	for _, row := range r.rows {
		rows = append(rows, proto.Clone(row).(*apidpb.IncrementRequestTelemetryRequest))
	}
	return rows
}

func (r *fleetDaemonTelemetryFixture) RecordRequestIDJournal(ctx context.Context, request *apidpb.RecordRequestIDJournalRequest) (*apidpb.RecordRequestIDJournalResponse, error) {
	received := time.UnixMilli(request.ReceivedAtUnixMs)
	err := r.store.RecordRequestIDJournal(ctx, state.RequestIDJournalEntry{ID: request.RecordId,
		AccountID: request.AccountId, AppID: request.AppId, RequestID: request.RequestId, TraceID: request.TraceId,
		ReceivedAt: received, ExpiresAt: received.Add(time.Duration(api.MustLimitsFor(api.PlanPro).DebugTelemetryRetentionDays) * 24 * time.Hour)})
	if err != nil {
		return nil, status.Error(codes.Unavailable, "fixture journal write failed")
	}
	return &apidpb.RecordRequestIDJournalResponse{Recorded: true}, nil
}

func newFleetDaemonFixture(t *testing.T) fleetDaemonFixture {
	t.Helper()
	f := fleetDaemonFixture{publicRoutingPGFixture: newPublicRoutingPGFixture(t)}
	f.vm = &fleetDaemonVMFixture{requests: make(map[string][]string), correlation: make(map[string][]wire.CorrelationFields),
		origins: make(map[string]int), guestHeaders: make(map[string][]http.Header)}
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.vm.mu.Lock()
		f.vm.origins[r.URL.RequestURI()]++
		f.vm.guestHeaders[r.URL.RequestURI()] = append(f.vm.guestHeaders[r.URL.RequestURI()], r.Header.Clone())
		f.vm.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain")
		if r.URL.Query().Get("application") == "error" {
			w.WriteHeader(http.StatusInternalServerError)
		}
		_, _ = io.WriteString(w, "guest served")
	}))
	t.Cleanup(guest.Close)
	f.vm.guest = guest.URL
	dir, err := os.MkdirTemp("/tmp", "gregale-fleet-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	vmSocket := filepath.Join(dir, "vmmd.sock")
	listener, err := net.Listen("unix", vmSocket)
	if err != nil {
		t.Fatal(err)
	}
	vmm := grpc.NewServer()
	vmmdpb.RegisterVmmdServer(vmm, f.vm)
	go func() { _ = vmm.Serve(listener) }()
	t.Cleanup(vmm.Stop)
	f.usage = filepath.Join(dir, "usage.sock")
	usageListener, err := net.Listen("unix", f.usage)
	if err != nil {
		t.Fatal(err)
	}
	usage := grpc.NewServer()
	f.telemetry = &fleetDaemonTelemetryFixture{
		usageReceiverForTest: usageReceiverForTest{events: make(map[string]int)}, store: f.store,
		rows: make(map[string]*apidpb.IncrementRequestTelemetryRequest)}
	apidpb.RegisterRequestTelemetryServer(usage, f.telemetry)
	go func() { _ = usage.Serve(usageListener) }()
	t.Cleanup(usage.Stop)
	role, gatewayURL := "compute-only", "tcp://127.0.0.1:9090"
	for range 2 {
		node, err := f.store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "fleet-daemon-" + uuid.NewString(),
			TargetURL: "unix://" + vmSocket, VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1,
			AdmissionCeilingMB: 512, VCPUBudget: 1, Active: true, Role: &role, GatewayTargetURL: &gatewayURL})
		if err != nil {
			t.Fatal(err)
		}
		f.nodes = append(f.nodes, node)
	}
	members := make([]state.ProjectReleaseMember, 0, 3)
	for i, name := range []string{"rate", "cache", "retry"} {
		app := f.app
		if i != 0 {
			app, err = f.store.CreateApp(t.Context(), state.App{AccountID: f.app.AccountID, ProjectID: f.project.ID,
				Slug: "fleet-" + name, WorkloadName: name, Type: state.AppTypeApp, RAMMB: 128,
				Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
			if err != nil {
				t.Fatalf("create fleet app %s: %v", name, err)
			}
		}
		rps, burst := 1, 4
		if name == "retry" {
			rps, burst = 100, 100
		}
		app, err = f.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{SetRequestRateLimitRPS: true, RequestRateLimitRPS: &rps,
			SetRequestRateLimitBurst: true, RequestRateLimitBurst: &burst})
		if err != nil {
			t.Fatalf("update fleet app %s: %v", name, err)
		}
		projectFixture := f.publicRoutingPGFixture
		projectFixture.app = app
		deployment := projectFixture.deployment(t, "production", "sha256:fleet-"+name)
		peer := fleetDaemonApp{ID: app.ID, Deployment: deployment.ID, Host: app.Slug + ".apps.gregale.dev", Instances: []string{uuid.NewString()}}
		if name == "retry" {
			f.vm.badInstance = peer.Instances[0]
			peer.Instances = append(peer.Instances, uuid.NewString())
		}
		f.apps = append(f.apps, peer)
		members = append(members, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: deployment.ID})
	}
	if _, err := f.store.PublishProjectReleaseSet(t.Context(), f.app.AccountID, f.project.ID, "production", 1800, members); err != nil {
		t.Fatalf("publish fleet release: %v", err)
	}
	for _, rule := range []state.CreateEdgeRuleParams{
		{AccountID: f.app.AccountID, AppID: f.apps[1].ID, MatchHost: f.apps[1].Host, MatchPath: "/cache", MatchMethods: []string{http.MethodGet}, Enabled: true,
			Kind: state.EdgeRuleKindCache, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindCache, Cache: &state.EdgeRuleCacheAction{MaxAgeSeconds: 30, Methods: []string{http.MethodGet}}}},
		{AccountID: f.app.AccountID, AppID: f.apps[2].ID, MatchHost: f.apps[2].Host, MatchPath: "/retry", MatchMethods: []string{http.MethodGet}, Enabled: true,
			Kind: state.EdgeRuleKindRetry, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRetry, Retry: &state.EdgeRuleRetryAction{MaxAttempts: 2, BudgetPercent: 10, BudgetMinRetries: 1}}},
	} {
		if _, err := f.store.CreateEdgeRule(t.Context(), rule); err != nil {
			t.Fatalf("create fleet %s rule: %v", rule.Kind, err)
		}
	}
	return f
}

type fleetDaemonResponse struct {
	status   int
	body     string
	headers  http.Header
	duration time.Duration
}

func requestFleetDaemon(ctx context.Context, endpoint, host, path string) (fleetDaemonResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
	if err != nil {
		return fleetDaemonResponse{}, err
	}
	request.Host = host
	started := time.Now()
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fleetDaemonResponse{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return fleetDaemonResponse{response.StatusCode, strings.TrimSpace(string(body)), response.Header.Clone(), time.Since(started)}, err
}

func (f fleetDaemonFixture) process(t *testing.T, node int) *fleetDaemonProcess {
	t.Helper()
	return startFleetDaemon(t, f.pool, f.apps, f.nodes[0].ID, f.nodes[node].Name, f.usage)
}
