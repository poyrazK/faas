package vmmdgrpc

// Interactive app-task attach (ADR-958). schedd's ExecuteAppTaskStream call
// registers a single-use rendezvous keyed by the task instance and the SHA-256
// digest of the client's attach token, then waits for the gateway to attach.
// The first valid AttachAppTask consumes the rendezvous; its stream supplies
// stdin and resize frames and receives output and the terminal result. Only
// the terminal metadata returns to schedd.

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"math"
	"sync"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultAppTaskAttachTimeout = 60 * time.Second
	maxAppTaskAttachTimeout     = 5 * time.Minute
	maxAppTaskAttachTokenBytes  = 256
	defaultAppTaskTerminalRows  = 24
	defaultAppTaskTerminalCols  = 80
	appTaskAttachInputDepth     = 32

	appTaskAttachTimeoutCode       = "attach_timeout"
	appTaskClientDisconnectedCode  = "client_disconnected"
	appTaskInteractiveUnsupported  = "interactive_unsupported"
	appTaskInteractiveTransportErr = "execution_transport_failed"
)

var (
	errAppTaskAttachNotPending  = errors.New("vmmdgrpc: no interactive app task is waiting for this instance")
	errAppTaskAttachTokenDenied = errors.New("vmmdgrpc: attach token rejected")
	errAppTaskAttachDuplicate   = errors.New("vmmdgrpc: interactive app task already awaits attach")
)

type appTaskAttachRegistry struct {
	mu      sync.Mutex
	pending map[string]*pendingAppTaskAttach
}

type pendingAppTaskAttach struct {
	digest  [sha256.Size]byte
	claimed chan *appTaskAttachClient
}

type appTaskAttachClient struct {
	stream vmmdpb.Vmmd_AttachAppTaskServer
	start  *vmmdpb.AttachAppTaskStart
	sendMu sync.Mutex
	done   chan struct{}
}

func (c *appTaskAttachClient) send(event *vmmdpb.AttachAppTaskEvent) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.stream.Send(event)
}

func (r *appTaskAttachRegistry) register(instance string, digest []byte) (*pendingAppTaskAttach, error) {
	if len(digest) != sha256.Size {
		return nil, errAppTaskAttachTokenDenied
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending == nil {
		r.pending = make(map[string]*pendingAppTaskAttach)
	}
	if _, exists := r.pending[instance]; exists {
		return nil, errAppTaskAttachDuplicate
	}
	entry := &pendingAppTaskAttach{claimed: make(chan *appTaskAttachClient, 1)}
	copy(entry.digest[:], digest)
	r.pending[instance] = entry
	return entry, nil
}

// unregister removes entry if it is still pending. false means a client
// already claimed it and is waiting in entry.claimed.
func (r *appTaskAttachRegistry) unregister(instance string, entry *pendingAppTaskAttach) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending[instance] != entry {
		return false
	}
	delete(r.pending, instance)
	return true
}

// claim consumes the rendezvous for instance when token matches. The client
// is handed over under the lock so unregister cannot miss it.
func (r *appTaskAttachRegistry) claim(instance, token string, client *appTaskAttachClient) error {
	digest := sha256.Sum256([]byte(token))
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.pending[instance]
	if !ok {
		return errAppTaskAttachNotPending
	}
	if subtle.ConstantTimeCompare(entry.digest[:], digest[:]) != 1 {
		return errAppTaskAttachTokenDenied
	}
	delete(r.pending, instance)
	entry.claimed <- client
	return nil
}

// AttachAppTask connects the gateway's client stream to a waiting
// interactive app task. The stream stays open until the session ends.
func (s *Server) AttachAppTask(stream vmmdpb.Vmmd_AttachAppTaskServer) error {
	const op = "AttachAppTask"
	started := time.Now()
	first, err := stream.Recv()
	if err != nil {
		s.ops.Observe(op, time.Since(started), err)
		return err
	}
	start := first.GetStart()
	if start == nil || start.GetInstance() == "" || start.GetAttachToken() == "" || len(start.GetAttachToken()) > maxAppTaskAttachTokenBytes {
		problem := api.NewProblem(int(codes.InvalidArgument), api.CodeValidation,
			"Invalid attach request", "the first frame must name the instance and carry an attach token")
		s.ops.Observe(op, time.Since(started), problem)
		return grpcerr.ToStatus(problem)
	}
	client := &appTaskAttachClient{stream: stream, start: start, done: make(chan struct{})}
	if err := s.appTaskAttach.claim(start.GetInstance(), start.GetAttachToken(), client); err != nil {
		var problem *api.Problem
		if errors.Is(err, errAppTaskAttachTokenDenied) {
			problem = api.NewProblem(int(codes.PermissionDenied), api.CodeUnauthorized,
				"Attach rejected", "the attach token does not match this session")
		} else {
			problem = api.NewProblem(int(codes.NotFound), api.CodeNotFound,
				"Session not waiting", "no interactive session is waiting for a client on this instance")
		}
		s.ops.Observe(op, time.Since(started), problem)
		return grpcerr.ToStatus(problem)
	}
	select {
	case <-client.done:
	case <-stream.Context().Done():
		<-client.done
	}
	s.ops.Observe(op, time.Since(started), nil)
	return nil
}

// executeInteractiveAppTaskStream is ExecuteAppTaskStream for interactive
// requests: wait for the attached client, run the session, report terminal
// metadata to schedd.
func (s *Server) executeInteractiveAppTaskStream(req *vmmdpb.ExecuteAppTaskRequest, stream vmmdpb.Vmmd_ExecuteAppTaskStreamServer) error {
	taskVMM, ok := s.vmm.(AppTaskInteractiveVMMAPI)
	if !ok {
		return grpcerr.ToStatus(api.NewProblem(int(codes.Unimplemented), api.CodeNotImplemented,
			"Interactive app tasks unavailable", "vmmd interactive app tasks are not configured"))
	}
	if err := validateInteractiveAppTaskRequest(req); err != nil {
		return grpcerr.ToStatus(err)
	}
	attachTimeout := defaultAppTaskAttachTimeout
	if seconds := req.GetAttachTimeoutSeconds(); seconds > 0 {
		attachTimeout = min(time.Duration(seconds)*time.Second, maxAppTaskAttachTimeout)
	}
	instance := req.GetInstance()
	entry, err := s.appTaskAttach.register(instance, req.GetAttachTokenSha256())
	if err != nil {
		code := codes.InvalidArgument
		if errors.Is(err, errAppTaskAttachDuplicate) {
			code = codes.AlreadyExists
		}
		return status.Error(code, err.Error())
	}
	waitStarted := time.Now()
	timer := time.NewTimer(attachTimeout)
	defer timer.Stop()
	var client *appTaskAttachClient
	select {
	case client = <-entry.claimed:
	case <-timer.C:
		if s.appTaskAttach.unregister(instance, entry) {
			return stream.Send(appTaskTerminalEvent(interactiveFailure(req.GetTaskId(), appTaskAttachTimeoutCode,
				"no client attached before the attach window closed")))
		}
		client = <-entry.claimed
	case <-stream.Context().Done():
		if s.appTaskAttach.unregister(instance, entry) {
			return stream.Context().Err()
		}
		client = <-entry.claimed
	}
	defer close(client.done)
	if err := stream.Context().Err(); err != nil {
		_ = client.send(attachTerminalEvent(interactiveFailure(req.GetTaskId(), "cancelled", "the session was cancelled")))
		return err
	}

	wireReq := interactiveWireRequest(req, client.start, time.Since(waitStarted))
	sessionCtx, cancelSession := context.WithCancel(stream.Context())
	defer cancelSession()
	clientCtx := client.stream.Context()
	go func() {
		select {
		case <-clientCtx.Done():
			cancelSession()
		case <-sessionCtx.Done():
		}
	}()
	input := make(chan apptaskproto.InputEvent, appTaskAttachInputDepth)
	go relayAppTaskAttachInput(sessionCtx, client.stream, input, cancelSession)

	if err := client.send(&vmmdpb.AttachAppTaskEvent{Frame: &vmmdpb.AttachAppTaskEvent_Attached{Attached: &vmmdpb.AttachAppTaskAttached{}}}); err != nil {
		cancelSession()
	}
	receive := func(ctx context.Context, outputStream string, chunk []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return client.send(&vmmdpb.AttachAppTaskEvent{Frame: &vmmdpb.AttachAppTaskEvent_Output{
			Output: &vmmdpb.ExecuteAppTaskOutputChunk{Stream: outputStream, Chunk: append([]byte(nil), chunk...)},
		}})
	}
	result, runErr := taskVMM.ExecuteInteractiveAppTask(sessionCtx, instance, wireReq, input, receive)
	if runErr != nil && stream.Context().Err() != nil {
		return stream.Context().Err()
	}
	terminal := interactiveTerminal(req.GetTaskId(), result, runErr, clientCtx.Err() != nil)
	if runErr != nil {
		s.log.Warn("vmmd: interactive app task ended without a guest result", "task_id", req.GetTaskId(), "failure_code", terminal.GetFailureCode())
	}
	_ = client.send(attachTerminalEvent(terminal))
	return stream.Send(appTaskTerminalEvent(terminal))
}

func validateInteractiveAppTaskRequest(req *vmmdpb.ExecuteAppTaskRequest) *api.Problem {
	invalid := func(detail string) *api.Problem {
		return api.NewProblem(int(codes.InvalidArgument), api.CodeValidation, "Invalid interactive app task", detail)
	}
	if req.GetInstance() == "" {
		return invalid("instance is required")
	}
	if len(req.GetAttachTokenSha256()) != sha256.Size {
		return invalid("attach_token_sha256 must be a SHA-256 digest")
	}
	if req.GetTimeoutSeconds() < apptaskproto.MinTimeoutSeconds || req.GetTimeoutSeconds() > apptaskproto.MaxTimeoutSeconds {
		return invalid("timeout_seconds is outside the hard range")
	}
	if req.GetAttachTimeoutSeconds() < 0 {
		return invalid("attach_timeout_seconds must not be negative")
	}
	probe := interactiveWireRequest(req, &vmmdpb.AttachAppTaskStart{}, 0)
	if probe.Validate() != nil {
		return invalid("request failed guest-boundary validation")
	}
	return nil
}

// interactiveWireRequest builds the version-2 guest request. The session
// time limit is what remains of the task timeout after waiting for attach.
func interactiveWireRequest(req *vmmdpb.ExecuteAppTaskRequest, start *vmmdpb.AttachAppTaskStart, waited time.Duration) apptaskproto.Request {
	remaining := int(math.Ceil((time.Duration(req.GetTimeoutSeconds())*time.Second - waited).Seconds()))
	remaining = max(remaining, apptaskproto.MinTimeoutSeconds)
	wire := apptaskproto.Request{
		Version: apptaskproto.InteractiveVersion, TaskID: req.GetTaskId(),
		Command: append([]string(nil), req.GetCommand()...), CommandShell: req.GetCommandShell(),
		TimeoutSeconds: remaining, Interactive: true, TTY: req.GetTty(),
	}
	if wire.TTY {
		wire.Rows = terminalDimension(start.GetRows(), defaultAppTaskTerminalRows, apptaskproto.MaxTerminalRows)
		wire.Cols = terminalDimension(start.GetCols(), defaultAppTaskTerminalCols, apptaskproto.MaxTerminalCols)
	}
	return wire
}

func terminalDimension(value uint32, fallback, limit uint16) uint16 {
	if value == 0 {
		return fallback
	}
	return uint16(min(value, uint32(limit)))
}

func relayAppTaskAttachInput(ctx context.Context, stream vmmdpb.Vmmd_AttachAppTaskServer, input chan<- apptaskproto.InputEvent, detach context.CancelFunc) {
	defer close(input)
	for {
		msg, err := stream.Recv()
		if err != nil {
			// Half-close or a broken stream both mean the client left.
			detach()
			return
		}
		var event apptaskproto.InputEvent
		switch frame := msg.GetFrame().(type) {
		case *vmmdpb.AttachAppTaskRequest_Stdin:
			for chunk := frame.Stdin; len(chunk) > 0; {
				n := min(len(chunk), apptaskproto.MaxStdinChunkBytes)
				if !pushAppTaskInput(ctx, input, apptaskproto.InputEvent{Kind: apptaskproto.InputStdin, Data: chunk[:n]}) {
					return
				}
				chunk = chunk[n:]
			}
			continue
		case *vmmdpb.AttachAppTaskRequest_Resize:
			rows, cols := frame.Resize.GetRows(), frame.Resize.GetCols()
			if rows == 0 || cols == 0 {
				continue
			}
			event = apptaskproto.InputEvent{Kind: apptaskproto.InputResize,
				Rows: uint16(min(rows, apptaskproto.MaxTerminalRows)), Cols: uint16(min(cols, apptaskproto.MaxTerminalCols))}
		case *vmmdpb.AttachAppTaskRequest_StdinClose:
			event = apptaskproto.InputEvent{Kind: apptaskproto.InputStdinClose}
		default:
			continue
		}
		if !pushAppTaskInput(ctx, input, event) {
			return
		}
	}
}

func pushAppTaskInput(ctx context.Context, input chan<- apptaskproto.InputEvent, event apptaskproto.InputEvent) bool {
	select {
	case input <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func interactiveTerminal(taskID string, result apptaskproto.Result, err error, clientGone bool) *vmmdpb.ExecuteAppTaskResponse {
	if err == nil {
		terminal := appTaskResponseFromResult(taskID, result)
		terminal.Stdout, terminal.Stderr = nil, nil
		return terminal
	}
	var guestErr *apptaskproto.GuestError
	switch {
	case clientGone:
		return interactiveFailure(taskID, appTaskClientDisconnectedCode, "the attached client disconnected")
	case errors.As(err, &guestErr) && guestErr.Code == appTaskInteractiveUnsupported:
		return interactiveFailure(taskID, appTaskInteractiveUnsupported,
			"this deployment's guest predates interactive sessions; redeploy the app and retry")
	case errors.Is(err, context.DeadlineExceeded):
		resp := interactiveFailure(taskID, "timeout", "session reached its time limit")
		resp.Status = string(apptaskproto.StatusTimedOut)
		return resp
	default:
		return interactiveFailure(taskID, appTaskInteractiveTransportErr, "the interactive session channel closed unexpectedly")
	}
}

func interactiveFailure(taskID, code, message string) *vmmdpb.ExecuteAppTaskResponse {
	return &vmmdpb.ExecuteAppTaskResponse{TaskId: taskID, Status: string(apptaskproto.StatusFailed), FailureCode: code, FailureMessage: message}
}

func appTaskTerminalEvent(terminal *vmmdpb.ExecuteAppTaskResponse) *vmmdpb.ExecuteAppTaskEvent {
	return &vmmdpb.ExecuteAppTaskEvent{Frame: &vmmdpb.ExecuteAppTaskEvent_Terminal{Terminal: terminal}}
}

func attachTerminalEvent(terminal *vmmdpb.ExecuteAppTaskResponse) *vmmdpb.AttachAppTaskEvent {
	return &vmmdpb.AttachAppTaskEvent{Frame: &vmmdpb.AttachAppTaskEvent_Terminal{Terminal: terminal}}
}
