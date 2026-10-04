// adr: 471
package main

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/vmmd/failureoutbox"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type failureAckServer struct {
	scheddpb.UnimplementedScheddServer
	mode  atomic.Int32
	calls atomic.Int32
}

func (s *failureAckServer) ReportLivenessFailed(_ context.Context, r *scheddpb.LivenessFailedReport) (*scheddpb.LivenessFailedAck, error) {
	s.calls.Add(1)
	if r.GetInstanceId() != "old-instance" || r.GetReason() != "timeout" || r.GetSourceNodeId() != "node-host" {
		return nil, status.Error(codes.InvalidArgument, "payload changed")
	}
	if s.mode.Load() == 0 {
		return nil, status.Error(codes.NotFound, "row unavailable")
	}
	return &scheddpb.LivenessFailedAck{Ok: s.mode.Load() >= 1, Applied: s.mode.Load() == 2}, nil
}

func (s *failureAckServer) ReportWorkloadOOM(_ context.Context, r *scheddpb.ReportWorkloadOOMRequest) (*scheddpb.ReportWorkloadOOMAck, error) {
	s.calls.Add(1)
	if r.GetInstanceId() != "old-instance" || r.GetPeakMb() != 300 || r.GetPlanMb() != 256 || r.GetSourceNodeId() != "node-host" {
		return nil, status.Error(codes.InvalidArgument, "payload changed")
	}
	if s.mode.Load() == 0 {
		return nil, status.Error(codes.Unavailable, "schedd unavailable")
	}
	return &scheddpb.ReportWorkloadOOMAck{Ok: s.mode.Load() >= 1, Applied: s.mode.Load() == 2}, nil
}

func failureReportTarget(t *testing.T, service *failureAckServer) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "fdr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "schedd.sock")
	lis, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	scheddpb.RegisterScheddServer(server, service)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(func() { server.Stop(); _ = lis.Close() })
	return "unix://" + path
}

func TestFailureReportSenderRequiresAffirmativeAck(t *testing.T) {
	service := &failureAckServer{}
	target := failureReportTarget(t, service)
	for _, r := range []failureoutbox.Report{
		{InstanceID: "old-instance", SourceNodeID: "node-host", Kind: failureoutbox.Liveness, Reason: "timeout"},
		{InstanceID: "old-instance", SourceNodeID: "node-host", Kind: failureoutbox.WorkloadOOM, PeakMB: 300, PlanMB: 256},
	} {
		for mode := range int32(3) {
			service.mode.Store(mode)
			err := sendFailureReport(t.Context(), target, nil, r)
			if (err == nil) != (mode == 2) {
				t.Fatalf("kind=%s mode=%d err=%v", r.Kind, mode, err)
			}
		}
	}
}

func TestFailureReportWiringPersistsCanceledProducerContexts(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mgr := fcvm.NewManager(nil, nil, fcvm.Paths{}, "1.7.0", log, nil)
	root := filepath.Join(t.TempDir(), "spool")
	o, err := wireFailureReports("node-host", mgr, &Config{FailureReportDir: root}, runDeps{scheddTarget: "unix:///unused"}, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	mgr.ReportLivenessFailed(ctx, "old-instance", "timeout")
	mgr.ReportWorkloadOOM(ctx, "old-instance", 300, 256)
	if o.Pending() != 2 {
		t.Fatal("producer cancellation lost reports")
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	loaded, err := failureoutbox.Open(root, func(context.Context, failureoutbox.Report) error { return nil }, log)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = loaded.Close() }()
	if loaded.Pending() != 2 {
		t.Fatal("wiring returned before durable commit")
	}
}

func TestRecoveredFailureReportRequiresManagerOwnership(t *testing.T) {
	service := &failureAckServer{}
	service.mode.Store(2)
	target := failureReportTarget(t, service)
	mgr := fcvm.NewManager(nil, nil, fcvm.Paths{}, "1.7.0", slog.Default(), nil)
	sender := ownedFailureReportSender(mgr, target, func() *tls.Config { return nil })
	r := failureoutbox.Report{InstanceID: "old-instance", SourceNodeID: "node-host", Kind: failureoutbox.Liveness, Reason: "timeout", Recovered: true}
	if err := sender(t.Context(), r); err == nil || !strings.Contains(err.Error(), "ownership reconciliation") {
		t.Fatalf("recovered report: %v", err)
	}
	if service.calls.Load() != 0 {
		t.Fatal("report delivered with unknown guest ownership")
	}
	r.Recovered = false
	if err := sender(t.Context(), r); err != nil {
		t.Fatal(err)
	}
}

// Prod hunt #3: run() wires the outbox before the schedd client mTLS
// material is loaded, and the sender captured that nil config. Every report
// to a tcp:// schedd then failed with "mTLS required" on every retry (attempt
// 190+ on production-us), so cross-node liveness and OOM failures never
// reached their owner. The sender must read the config at delivery time.
func TestFailureReportSenderReadsTLSAtDeliveryTime(t *testing.T) {
	mgr := fcvm.NewManager(nil, nil, fcvm.Paths{}, "1.7.0", slog.Default(), nil)
	var current atomic.Pointer[tls.Config]
	var reads atomic.Int32
	sender := ownedFailureReportSender(mgr, "tcp://127.0.0.1:1", func() *tls.Config {
		reads.Add(1)
		return current.Load()
	})
	r := failureoutbox.Report{InstanceID: "old-instance", SourceNodeID: "node-host", Kind: failureoutbox.Liveness, Reason: "timeout"}
	err := sender(t.Context(), r)
	if err == nil || !strings.Contains(err.Error(), "mTLS required") {
		t.Fatalf("send before TLS load: %v, want the mTLS-required refusal", err)
	}
	current.Store(&tls.Config{MinVersion: tls.VersionTLS13})
	err = sender(t.Context(), r)
	if err != nil && strings.Contains(err.Error(), "mTLS required") {
		t.Fatalf("send after TLS load still refused for missing mTLS: %v", err)
	}
	if reads.Load() != 2 {
		t.Fatalf("TLS config read %d times, want once per delivery", reads.Load())
	}
}
