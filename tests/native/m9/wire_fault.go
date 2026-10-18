// spec: §14.A
// adr: 1530
package m9

import (
	"context"
	"errors"
	"fmt"
	"sync"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AckFault changes only the qualification client's view of one successful RPC.
// Production daemons and their network configuration have no fault switches.
type AckFault struct {
	mu        sync.Mutex
	instance  string
	lease     string
	admission string
	wake      string
	adopted   bool
	discarded bool
	attempted bool
	failure   error
	hooks     FaultHooks
}

type FaultHooks struct {
	PauseSource  func(context.Context) error
	BeforeAck    func(context.Context, string, string) error
	ResumeSource func(context.Context) error
	AfterAck     func(context.Context, string, string) error
}

func NewAckFault(instance string, hooks FaultHooks) (*AckFault, error) {
	if !CanonicalUUID(instance) || hooks.PauseSource == nil || hooks.BeforeAck == nil ||
		hooks.ResumeSource == nil || hooks.AfterAck == nil {
		return nil, errors.New("M9 acknowledgement fault requires an instance and all observation hooks")
	}
	return &AckFault{instance: instance, hooks: hooks}, nil
}

func (f *AckFault) Result() (lease string, discarded bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lease, f.discarded, f.failure
}

func (f *AckFault) DestinationIdentity() (admission, wake string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.admission, f.wake
}

func (f *AckFault) remember(err error) error {
	if err != nil {
		f.mu.Lock()
		f.failure = errors.Join(f.failure, err)
		f.mu.Unlock()
	}
	return err
}

func (f *AckFault) Source(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if method == vmmdpb.Vmmd_PrepareLiveMigration_FullMethodName {
		r, ok := req.(*vmmdpb.PrepareLiveMigrationRequest)
		if !ok || r.GetInstanceId() != f.instance {
			return invoke(ctx, method, req, reply, cc, opts...)
		}
		if err := invoke(ctx, method, req, reply, cc, opts...); err != nil {
			return f.remember(err)
		}
		prepared, ok := reply.(*vmmdpb.PrepareLiveMigrationResponse)
		if !ok || prepared.GetLeaseToken() == "" {
			return f.remember(errors.New("M9 source returned no durable migration lease"))
		}
		f.mu.Lock()
		if f.lease == "" {
			f.lease = prepared.GetLeaseToken()
		}
		f.mu.Unlock()
		return nil
	}
	if method != vmmdpb.Vmmd_AcknowledgeMigration_FullMethodName {
		return invoke(ctx, method, req, reply, cc, opts...)
	}
	r, ok := req.(*vmmdpb.AcknowledgeMigrationRequest)
	f.mu.Lock()
	target := ok && !f.discarded && f.adopted && r.GetInstanceId() == f.instance && r.GetLeaseToken() == f.lease
	if target {
		if f.attempted {
			f.mu.Unlock()
			return status.Error(codes.Unavailable, "M9 acknowledgement observation is already claimed")
		}
		f.attempted = true
	}
	f.mu.Unlock()
	if !target {
		return invoke(ctx, method, req, reply, cc, opts...)
	}
	if err := f.hooks.BeforeAck(ctx, f.instance, r.GetLeaseToken()); err != nil {
		return f.remember(err)
	}
	if err := f.hooks.ResumeSource(ctx); err != nil {
		return f.remember(err)
	}
	if err := invoke(ctx, method, req, reply, cc, opts...); err != nil {
		return f.remember(fmt.Errorf("M9 real source acknowledgement failed: %w", err))
	}
	if err := f.hooks.AfterAck(ctx, f.instance, r.GetLeaseToken()); err != nil {
		return f.remember(err)
	}
	f.mu.Lock()
	f.discarded = true
	f.mu.Unlock()
	return status.Error(codes.Unavailable, "native M9 discarded the successful source acknowledgement response")
}

func (f *AckFault) Destination(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if err := invoke(ctx, method, req, reply, cc, opts...); err != nil {
		return err
	}
	if method != vmmdpb.Vmmd_AdoptMigratedInstance_FullMethodName {
		return nil
	}
	r, ok := req.(*vmmdpb.AdoptMigratedInstanceRequest)
	f.mu.Lock()
	target := ok && !f.adopted && f.lease != "" && r.GetInstanceId() == f.instance && r.GetLeaseToken() == f.lease
	f.mu.Unlock()
	if !target {
		return nil
	}
	adopted, ok := reply.(*vmmdpb.AdoptMigratedInstanceResponse)
	if !ok || adopted.GetMethod() != vmmdpb.WakeMethod_WAKE_RESTORE ||
		!CanonicalUUID(adopted.GetAdmissionId()) || adopted.GetAdmissionId() != r.GetAdmissionId() ||
		!CanonicalUUID(adopted.GetWakeId()) {
		return f.remember(errors.New("M9 requires real snapshot restoration, not cold-boot fallback"))
	}
	f.mu.Lock()
	f.admission, f.wake = adopted.GetAdmissionId(), adopted.GetWakeId()
	f.mu.Unlock()
	// Stop the source daemon only after adoption succeeded, before the scheduler
	// commits ownership. Its reconciliation sweep cannot retire the source before the
	// BeforeAck hook observes the retained physical charge.
	if err := f.hooks.PauseSource(ctx); err != nil {
		return f.remember(err)
	}
	f.mu.Lock()
	f.adopted = true
	f.mu.Unlock()
	return nil
}
