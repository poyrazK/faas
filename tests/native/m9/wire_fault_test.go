// spec: §14.A
// adr: 1530
package m9

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const testInstance = "44444444-4444-4444-8444-444444444444"
const testAdmission = "55555555-5555-4555-8555-555555555555"
const testWake = "66666666-6666-4666-8666-666666666666"

func faultForTest(t *testing.T, events *[]string) *AckFault {
	t.Helper()
	record := func(name string) func(context.Context) error {
		return func(context.Context) error { *events = append(*events, name); return nil }
	}
	f, err := NewAckFault(testInstance, FaultHooks{
		PauseSource: record("pause"), ResumeSource: record("resume"),
		BeforeAck: func(ctx context.Context, instance, lease string) error {
			if instance != testInstance || lease != "source-lease" {
				return errors.New("wrong source binding")
			}
			return record("observe-retained")(ctx)
		},
		AfterAck: func(ctx context.Context, _, _ string) error { return record("observe-retired")(ctx) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func prepareFault(t *testing.T, f *AckFault, method vmmdpb.WakeMethod) error {
	t.Helper()
	ctx := t.Context()
	invoke := func(_ context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		return nil
	}
	if err := f.Source(ctx, vmmdpb.Vmmd_PrepareLiveMigration_FullMethodName,
		&vmmdpb.PrepareLiveMigrationRequest{InstanceId: testInstance},
		&vmmdpb.PrepareLiveMigrationResponse{LeaseToken: "source-lease"}, nil, invoke); err != nil {
		t.Fatal(err)
	}
	return f.Destination(ctx, vmmdpb.Vmmd_AdoptMigratedInstance_FullMethodName,
		&vmmdpb.AdoptMigratedInstanceRequest{InstanceId: testInstance, LeaseToken: "source-lease", AdmissionId: testAdmission},
		&vmmdpb.AdoptMigratedInstanceResponse{Method: method, AdmissionId: testAdmission, WakeId: testWake}, nil, invoke)
}

func TestAcknowledgementLossOccursAfterRealSuccessOnce(t *testing.T) {
	var events []string
	f := faultForTest(t, &events)
	if err := prepareFault(t, f, vmmdpb.WakeMethod_WAKE_RESTORE); err != nil {
		t.Fatal(err)
	}
	invoke := func(_ context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		events = append(events, "real-ack")
		return nil
	}
	request := &vmmdpb.AcknowledgeMigrationRequest{InstanceId: testInstance, LeaseToken: "source-lease"}
	err := f.Source(t.Context(), vmmdpb.Vmmd_AcknowledgeMigration_FullMethodName, request,
		&vmmdpb.AcknowledgeMigrationResponse{}, nil, invoke)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("lost response: %v", err)
	}
	lease, discarded, failure := f.Result()
	if lease != "source-lease" || !discarded || failure != nil {
		t.Fatalf("missing injection evidence: %q %t %v", lease, discarded, failure)
	}
	if !reflect.DeepEqual(events, []string{"pause", "observe-retained", "resume", "real-ack", "observe-retired"}) {
		t.Fatalf("fault violated durable observation order: %v", events)
	}
	if err := f.Source(t.Context(), vmmdpb.Vmmd_AcknowledgeMigration_FullMethodName, request,
		&vmmdpb.AcknowledgeMigrationResponse{}, nil, invoke); err != nil {
		t.Fatalf("replay must reach daemon: %v", err)
	}
	if len(events) != 6 || events[5] != "real-ack" {
		t.Fatalf("replay faulted twice: %v", events)
	}
}

func TestFailedAcknowledgementCannotQualifyAsLostResponse(t *testing.T) {
	var events []string
	f := faultForTest(t, &events)
	if err := prepareFault(t, f, vmmdpb.WakeMethod_WAKE_RESTORE); err != nil {
		t.Fatal(err)
	}
	want := status.Error(codes.FailedPrecondition, "source cleanup refused")
	invoke := func(_ context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		return want
	}
	err := f.Source(t.Context(), vmmdpb.Vmmd_AcknowledgeMigration_FullMethodName,
		&vmmdpb.AcknowledgeMigrationRequest{InstanceId: testInstance, LeaseToken: "source-lease"},
		&vmmdpb.AcknowledgeMigrationResponse{}, nil, invoke)
	_, discarded, failure := f.Result()
	if !errors.Is(err, want) || discarded || failure == nil {
		t.Fatalf("failed RPC was called lost success: %v %t %v", err, discarded, failure)
	}
	if !reflect.DeepEqual(events, []string{"pause", "observe-retained", "resume"}) {
		t.Fatalf("false retirement evidence: %v", events)
	}
}

func TestColdFallbackAndForeignLeasesCannotInjectFault(t *testing.T) {
	var events []string
	f := faultForTest(t, &events)
	if err := prepareFault(t, f, vmmdpb.WakeMethod_WAKE_COLD_BOOT); err == nil {
		t.Fatal("cold fallback qualified")
	}
	calls := 0
	invoke := func(_ context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		calls++
		return nil
	}
	err := f.Source(t.Context(), vmmdpb.Vmmd_AcknowledgeMigration_FullMethodName,
		&vmmdpb.AcknowledgeMigrationRequest{InstanceId: testInstance, LeaseToken: "foreign-lease"},
		&vmmdpb.AcknowledgeMigrationResponse{}, nil, invoke)
	if err != nil || calls != 1 || len(events) != 0 {
		t.Fatalf("unrelated RPC faulted: %v %d %v", err, calls, events)
	}
}

func TestConcurrentAcknowledgementsClaimTheFaultOnce(t *testing.T) {
	var events []string
	f := faultForTest(t, &events)
	if err := prepareFault(t, f, vmmdpb.WakeMethod_WAKE_RESTORE); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	f.hooks.BeforeAck = func(ctx context.Context, _, _ string) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var calls atomic.Int32
	invoke := func(_ context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		calls.Add(1)
		return nil
	}
	call := func() error {
		return f.Source(t.Context(), vmmdpb.Vmmd_AcknowledgeMigration_FullMethodName,
			&vmmdpb.AcknowledgeMigrationRequest{InstanceId: testInstance, LeaseToken: "source-lease"},
			&vmmdpb.AcknowledgeMigrationResponse{}, nil, invoke)
	}
	first := make(chan error, 1)
	go func() { first <- call() }()
	<-entered
	second := call()
	close(release)
	if status.Code(<-first) != codes.Unavailable || status.Code(second) != codes.Unavailable || calls.Load() != 1 {
		t.Fatalf("concurrent caller duplicated the faulted RPC: second=%v calls=%d", second, calls.Load())
	}
}
