package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/usageoutbox"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// adr: 239

type usageReceiverForTest struct {
	apidpb.UnimplementedRequestTelemetryServer
	mu             sync.Mutex
	attempts       int
	events         map[string]int
	surfaceID      string
	surfaceSupport bool
}

func (r *usageReceiverForTest) RecordConsumerUsage(_ context.Context, event *apidpb.ConsumerUsageEvent) (*apidpb.ConsumerUsageReceipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if event.GetEventId() == "" {
		return nil, status.Error(codes.InvalidArgument, "event_id required")
	}
	r.attempts++
	if r.attempts == 1 {
		return nil, status.Error(codes.Unavailable, "test outage")
	}
	r.events[event.GetEventId()]++
	r.surfaceID = event.GetPlatformTenantSurfaceId()
	return &apidpb.ConsumerUsageReceipt{
		Applied: r.events[event.GetEventId()] == 1, SurfaceAttributionSupported: r.surfaceSupport,
	}, nil
}

func TestConsumerUsageDeliveryRetainsOnFailureAndAcknowledgesRetry(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "faas-usage-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "usage.sock")
	lis, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	receiver := &usageReceiverForTest{events: make(map[string]int)}
	apidpb.RegisterRequestTelemetryServer(server, receiver)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)
	if err := confirmConsumerUsageReceiver(context.Background(), socket, nil); err != nil {
		t.Fatalf("receiver compatibility: %v", err)
	}
	q, err := usageoutbox.Open(filepath.Join(dir, "spool"), 4096)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if err := q.Enqueue(usageoutbox.Event{EventID: id, AccountID: uuid.NewString(), AppID: uuid.NewString(), WindowStart: time.Now().UTC().Truncate(time.Minute), RequestCount: 1, BillableUnits: 1}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go deliverConsumerUsage(ctx, q, socket, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if q.Stats().PendingRecords == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := q.Stats().PendingRecords; got != 0 {
		t.Fatalf("pending=%d after retry", got)
	}
	receiver.mu.Lock()
	attempts, recorded := receiver.attempts, receiver.events[id]
	receiver.mu.Unlock()
	if attempts < 2 || recorded != 1 {
		t.Fatalf("attempts=%d recorded=%d", attempts, recorded)
	}
}

func TestConsumerUsageCompatibilityProbeRejectsOldReceiver(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "faas-usage-old-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "usage.sock")
	lis, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	apidpb.RegisterRequestTelemetryServer(server, &apidpb.UnimplementedRequestTelemetryServer{})
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)
	if err := confirmConsumerUsageReceiver(context.Background(), socket, nil); err == nil {
		t.Fatal("older apid was accepted")
	}
}

func TestSurfaceUsageDeliveryRetainsUntilReceiverAcknowledgesAttribution(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "faas-usage-surface-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "usage.sock")
	lis, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	receiver := &usageReceiverForTest{events: make(map[string]int)}
	apidpb.RegisterRequestTelemetryServer(server, receiver)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)
	q, err := usageoutbox.Open(filepath.Join(dir, "spool"), 4096)
	if err != nil {
		t.Fatal(err)
	}
	id, surfaceID := uuid.NewString(), uuid.NewString()
	if err := q.Enqueue(usageoutbox.Event{
		EventID: id, AccountID: uuid.NewString(), AppID: uuid.NewString(),
		PlatformTenantID: uuid.NewString(), PlatformTenantSurfaceID: surfaceID,
		WindowStart: time.Now().UTC().Truncate(time.Minute), RequestCount: 1, BillableUnits: 1,
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go deliverConsumerUsage(ctx, q, socket, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		receiver.mu.Lock()
		attempts, gotSurface := receiver.attempts, receiver.surfaceID
		receiver.mu.Unlock()
		if attempts >= 2 {
			if gotSurface != surfaceID {
				t.Fatalf("surface ID sent = %q, want %q", gotSurface, surfaceID)
			}
			if pending := q.Stats().PendingRecords; pending != 1 {
				t.Fatalf("unsupported receiver acknowledged surface event: pending=%d", pending)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("surface event was not retried")
}
