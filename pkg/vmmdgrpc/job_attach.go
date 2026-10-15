package vmmdgrpc

// Interactive image runs (ADR-1330). JobColdBoot registers ADR-1037's
// single-use rendezvous for the job instance before the VM boots, so the
// instance is attachable by the time schedd publishes it as running. The
// first valid AttachAppTask for the instance starts the session: vmmd dials
// the guest's session port and relays stdin, resize and output. The guest's
// job supervisor reports the task's exit over the ordinary job receipt; the
// terminal result here goes only to the client.

import (
	"context"
	"errors"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

const (
	// jobInteractiveSessionSlack covers the supervisor's SIGTERM grace and
	// result delivery after the task timeout.
	jobInteractiveSessionSlack = 60 * time.Second
	jobSessionEndedCode        = "session_ended"
)

// jobInteractiveRendezvousSlack keeps the rendezvous open past the guest's
// attach window, which starts only once guest-init runs. A var so tests can
// expire an unclaimed rendezvous quickly.
var jobInteractiveRendezvousSlack = 60 * time.Second

// jobSessionCommand names the request for validation only: the guest runs
// the command staged in job.json, never one sent over the session.
var jobSessionCommand = []string{"job"}

// serveInteractiveJob waits for the run's one client and relays its session.
// It returns when the session ends or the rendezvous expires unclaimed. ctx
// carries request values only: the session outlives the JobColdBoot call.
func (s *Server) serveInteractiveJob(ctx context.Context, jobVMM JobInteractiveVMMAPI, instance string, entry *pendingAppTaskAttach, in *fcvm.JobInteractive, taskTimeoutSec int) {
	timer := time.NewTimer(time.Duration(in.AttachTimeoutSec)*time.Second + jobInteractiveRendezvousSlack)
	defer timer.Stop()
	var client *appTaskAttachClient
	select {
	case client = <-entry.claimed:
	case <-timer.C:
		if s.appTaskAttach.unregister(instance, entry) {
			return
		}
		client = <-entry.claimed
	}
	defer close(client.done)

	sessionCtx, cancelSession := context.WithTimeout(ctx,
		time.Duration(taskTimeoutSec)*time.Second+jobInteractiveSessionSlack)
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
	result, runErr := jobVMM.ExecuteInteractiveJob(sessionCtx, instance, jobSessionRequest(instance, in, client.start, taskTimeoutSec), input, receive)
	terminal := jobSessionTerminal(instance, result, runErr, clientCtx.Err() != nil)
	if runErr != nil {
		s.log.Warn("vmmd: interactive job session ended without a guest result", "instance", instance,
			"failure_code", terminal.GetFailureCode(), "err", runErr)
	}
	_ = client.send(attachTerminalEvent(terminal))
}

// jobSessionRequest is the version-2 guest request. The session limit is the
// task timeout: the supervisor's clock starts when the command does.
func jobSessionRequest(instance string, in *fcvm.JobInteractive, start *vmmdpb.AttachAppTaskStart, taskTimeoutSec int) apptaskproto.Request {
	req := apptaskproto.Request{
		Version: apptaskproto.InteractiveVersion, TaskID: instance,
		Command:        append([]string(nil), jobSessionCommand...),
		TimeoutSeconds: min(max(taskTimeoutSec, apptaskproto.MinTimeoutSeconds), apptaskproto.MaxTimeoutSeconds),
		Interactive:    true, TTY: in.TTY,
	}
	if req.TTY {
		req.Rows = terminalDimension(start.GetRows(), defaultAppTaskTerminalRows, apptaskproto.MaxTerminalRows)
		req.Cols = terminalDimension(start.GetCols(), defaultAppTaskTerminalCols, apptaskproto.MaxTerminalCols)
	}
	return req
}

func jobSessionTerminal(instance string, result apptaskproto.Result, err error, clientGone bool) *vmmdpb.ExecuteAppTaskResponse {
	if errors.Is(err, fcvm.ErrJobSessionInstanceNotFound) {
		return interactiveFailure(instance, jobSessionEndedCode, "the task VM has already exited")
	}
	return interactiveTerminal(instance, result, err, clientGone)
}
