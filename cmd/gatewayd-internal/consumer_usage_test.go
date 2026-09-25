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

type usageReceiverForTest struct {
	apidpb.UnimplementedRequestTelemetryServer
	mu       sync.Mutex
	attempts int
	events   map[string]int
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
	return &apidpb.ConsumerUsageReceipt{Applied: r.events[event.GetEventId()] == 1}, nil
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

type receiverWithoutAuditAck struct {
	apidpb.UnimplementedRequestTelemetryServer
	called chan struct{}
}

func (r *receiverWithoutAuditAck) RecordConsumerUsage(_ context.Context, _ *apidpb.ConsumerUsageEvent) (*apidpb.ConsumerUsageReceipt, error) {
	select {
	case r.called <- struct{}{}:
	default:
	}
	return &apidpb.ConsumerUsageReceipt{Applied: true}, nil
}

func TestAuditEvidenceIsNotAcknowledgedByOldReceiver(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "faas-audit-old-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "usage.sock")
	lis, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	receiver := &receiverWithoutAuditAck{called: make(chan struct{}, 1)}
	server := grpc.NewServer()
	apidpb.RegisterRequestTelemetryServer(server, receiver)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)
	q, err := usageoutbox.Open(filepath.Join(dir, "spool"), 4096)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(usageoutbox.Event{
		EventID: uuid.NewString(), AccountID: uuid.NewString(), AppID: uuid.NewString(),
		WindowStart: time.Now().UTC().Truncate(time.Minute), RequestCount: 1, BillableUnits: 1,
		Audit: &usageoutbox.AuditEvidence{RouteTemplate: "GET /orders/{id}", Method: "GET", HTTPStatus: 200, OccurredAt: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go deliverConsumerUsage(ctx, q, socket, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	select {
	case <-receiver.called:
	case <-time.After(3 * time.Second):
		t.Fatal("receiver was not called")
	}
	time.Sleep(100 * time.Millisecond)
	// The old receiver can acknowledge financial usage but cannot claim it
	// recorded audit evidence, so the gateway must retain the durable item.
	if got := q.Stats().PendingRecords; got != 1 {
		t.Fatalf("audit item acknowledged without audit receipt: pending=%d", got)
	}
}
