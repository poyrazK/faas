package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

// VmmdExecutionTransport is the small surface implemented by
// fcvm.ExecutionSession. Keeping the backend on an interface makes the
// coordinator testable without KVM while preserving the production ordering:
// restore/cold boot, resume hook, protocol exchange, then destroy.
type VmmdExecutionTransport interface {
	Execute(context.Context, executionproto.Request) (executionproto.Result, error)
	Destroy(context.Context) error
}

// VmmdExecutionRestoreFunc is supplied by the vmmd client/server integration.
// It must return only after a fresh jail has been restored or cold-booted and
// the resume hook has completed. It must not receive caller source or input.
type VmmdExecutionRestoreFunc func(context.Context, ExecutionRestoreRequest) (VmmdExecutionTransport, error)

// ExecutionPayloadDecoder authenticates and decrypts the durable payload in
// host memory. The plaintext must be discarded by the implementation after it
// returns; no decoder error is exposed to the caller as raw detail.
type ExecutionPayloadDecoder func(context.Context, []byte, string) (source string, input json.RawMessage, err error)

// VmmdExecutionBackend bridges the scheduler's opaque durable payload to the
// vmmd transport. It is deliberately inert until both restore and decode
// functions are wired by schedd startup.
type VmmdExecutionBackend struct {
	restore VmmdExecutionRestoreFunc
	decode  ExecutionPayloadDecoder
}

func NewVmmdExecutionBackend(restore VmmdExecutionRestoreFunc, decode ExecutionPayloadDecoder) *VmmdExecutionBackend {
	return &VmmdExecutionBackend{restore: restore, decode: decode}
}

func (b *VmmdExecutionBackend) Restore(ctx context.Context, request ExecutionRestoreRequest) (ExecutionSession, error) {
	if b == nil || b.restore == nil || b.decode == nil {
		return nil, ErrExecutionCoordinatorNotWired
	}
	transport, err := b.restore(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("sched: vmmd restore execution %s: %w", request.ID, err)
	}
	if transport == nil {
		return nil, errors.New("sched: vmmd returned nil execution transport")
	}
	return &vmmdExecutionSession{transport: transport, request: request, decode: b.decode}, nil
}

type vmmdExecutionSession struct {
	transport VmmdExecutionTransport
	request   ExecutionRestoreRequest
	decode    ExecutionPayloadDecoder

	destroyMu sync.Mutex
	destroyed bool
}

func (s *vmmdExecutionSession) Execute(ctx context.Context, payload ExecutionPayload) (ExecutionOutcome, error) {
	if s == nil || s.transport == nil || s.decode == nil {
		return ExecutionOutcome{}, ErrExecutionCoordinatorNotWired
	}
	s.destroyMu.Lock()
	destroyed := s.destroyed
	s.destroyMu.Unlock()
	if destroyed {
		return ExecutionOutcome{}, errors.New("sched: execution session destroyed")
	}
	defer clear(payload.Sealed)

	source, input, err := s.decode(ctx, payload.Sealed, payload.KID)
	if err != nil {
		// Decoder errors may include key ids, storage paths, or partial
		// plaintext. The coordinator logs transport errors, so expose only a
		// fixed structural error at this boundary.
		return ExecutionOutcome{}, errors.New("sched: decode execution payload failed")
	}
	resolved := api.ResolvedExecutionRequest{
		Runtime: s.request.Runtime,
		Source:  source,
		Input:   append(json.RawMessage(nil), input...),
		Limits:  s.request.Limits,
		Network: api.ExecutionNetworkPolicy{Mode: s.request.NetworkMode},
	}
	wireRequest := executionproto.RequestFromResolvedExecution(s.request.ID, resolved)
	// The durable deadline includes queue and restore time. Never grant a
	// restored guest the full admitted timeout after a slow restore.
	if deadline, ok := ctx.Deadline(); ok {
		remainingMS := int(time.Until(deadline).Milliseconds())
		if remainingMS < wireRequest.TimeoutMS {
			wireRequest.TimeoutMS = remainingMS
		}
	}
	if wireRequest.TimeoutMS < api.ExecutionTimeoutMinMS {
		return ExecutionOutcome{Status: api.ExecutionStatusTimedOut, FailureCode: "timeout", FailureMessage: "execution timed out before guest dispatch"}, nil
	}
	if err := wireRequest.Validate(); err != nil {
		return ExecutionOutcome{}, errors.New("sched: execution request failed validation")
	}

	result, err := s.transport.Execute(ctx, wireRequest)
	if err != nil {
		return ExecutionOutcome{}, fmt.Errorf("sched: guest execution exchange: %w", err)
	}
	return outcomeFromProtocolResult(result), nil
}

func (s *vmmdExecutionSession) Destroy(ctx context.Context) error {
	if s == nil || s.transport == nil {
		return nil
	}
	s.destroyMu.Lock()
	if s.destroyed {
		s.destroyMu.Unlock()
		return nil
	}
	s.destroyed = true
	s.destroyMu.Unlock()
	return s.transport.Destroy(ctx)
}

func outcomeFromProtocolResult(result executionproto.Result) ExecutionOutcome {
	outcome := ExecutionOutcome{
		Status:          result.Status,
		Result:          append(json.RawMessage(nil), result.Result...),
		Stdout:          string(result.Stdout),
		Stderr:          string(result.Stderr),
		OutputTruncated: result.OutputTruncated,
		ExitCode:        result.ExitCode,
		Usage:           result.Usage,
	}
	if result.Status == api.ExecutionStatusSucceeded {
		return outcome
	}
	// Guest language errors are not safe public detail: they may contain source
	// snippets, filesystem paths, or secrets from a stack trace. Keep only a
	// closed code vocabulary and a fixed caller-facing message.
	switch result.Status {
	case api.ExecutionStatusTimedOut:
		outcome.FailureCode = "timeout"
		outcome.FailureMessage = "execution timed out"
	case api.ExecutionStatusOutOfMemory:
		outcome.FailureCode = "out_of_memory"
		outcome.FailureMessage = "execution exceeded its memory limit"
	case api.ExecutionStatusCancelled:
		outcome.FailureCode = "cancelled"
		outcome.FailureMessage = "execution was cancelled"
	default:
		outcome.FailureCode = "guest_error"
		outcome.FailureMessage = "execution failed inside the isolated guest"
	}
	return outcome
}
