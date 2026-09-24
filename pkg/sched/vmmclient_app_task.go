package sched

import (
	"context"
	"errors"
	"io"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/wire"
)

// AppTaskRestoreSpec contains the command-free immutable runtime selected by
// schedd for one durable task.
type AppTaskRestoreSpec struct {
	Instance     string
	DeploymentID string
	App          AppSpec
}

type AppTaskRestoreOutcome struct {
	Instance string
	LeaseUID int
	Method   fcvm.WakeMethod
}

func (c *VMMClient) RestoreAppTask(ctx context.Context, spec AppTaskRestoreSpec) (*AppTaskRestoreOutcome, error) {
	if c == nil || c.cli == nil {
		return nil, errors.New("sched: nil vmmd app task client")
	}
	fields, _ := wire.FromContext(ctx)
	ctx = wire.WithCorrelationOutgoing(ctx, fields)
	resp, err := c.cli.RestoreAppTask(ctx, &vmmdpb.RestoreAppTaskRequest{
		Instance: spec.Instance, App: spec.App.toProto(), Plan: string(spec.App.Plan),
		AccountId: spec.App.AccountID, DeploymentId: spec.DeploymentID,
	})
	if err != nil {
		return nil, liftErr(err)
	}
	return &AppTaskRestoreOutcome{
		Instance: resp.GetInstance(), LeaseUID: int(resp.GetLeaseUid()), Method: fcvm.WakeMethod(resp.GetMethod()),
	}, nil
}

func (c *VMMClient) ExecuteAppTask(ctx context.Context, instance string, req apptaskproto.Request) (apptaskproto.Result, error) {
	var zero apptaskproto.Result
	if c == nil || c.cli == nil {
		return zero, errors.New("sched: nil vmmd app task client")
	}
	fields, _ := wire.FromContext(ctx)
	ctx = wire.WithCorrelationOutgoing(ctx, fields)
	resp, err := c.cli.ExecuteAppTask(ctx, appTaskRequestToProto(instance, req))
	if err != nil {
		return zero, liftErr(err)
	}
	result := appTaskResultFromResponse(resp)
	if err := result.Validate(req.MaxOutputBytes); err != nil {
		return zero, err
	}
	return result, nil
}

func (c *VMMClient) ExecuteAppTaskWithOutput(ctx context.Context, instance string, req apptaskproto.Request, receive apptaskproto.OutputReceiver) (apptaskproto.Result, error) {
	var zero apptaskproto.Result
	if c == nil || c.cli == nil {
		return zero, errors.New("sched: nil vmmd app task client")
	}
	fields, _ := wire.FromContext(ctx)
	ctx = wire.WithCorrelationOutgoing(ctx, fields)
	stream, err := c.cli.ExecuteAppTaskStream(ctx, appTaskRequestToProto(instance, req))
	if err != nil {
		return zero, liftErr(err)
	}
	var result apptaskproto.Result
	for {
		event, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			return zero, errors.New("sched: app task stream ended before terminal result")
		}
		if recvErr != nil {
			return zero, liftErr(recvErr)
		}
		if event == nil {
			return zero, errors.New("sched: app task stream returned a nil event")
		}
		if output := event.GetOutput(); output != nil {
			streamName := output.GetStream()
			if streamName != "stdout" && streamName != "stderr" {
				return zero, errors.New("sched: app task stream returned an invalid output stream")
			}
			chunk := output.GetChunk()
			if len(result.Stdout)+len(result.Stderr)+len(chunk) > req.MaxOutputBytes {
				return zero, apptaskproto.ErrOutputLimitExceeded
			}
			if streamName == "stdout" {
				result.Stdout = append(result.Stdout, chunk...)
			} else {
				result.Stderr = append(result.Stderr, chunk...)
			}
			if receive != nil {
				if err := receive(ctx, streamName, chunk); err != nil {
					return zero, err
				}
			}
			continue
		}
		if terminal := event.GetTerminal(); terminal != nil {
			result = mergeAppTaskResponse(result, terminal)
			if err := result.Validate(req.MaxOutputBytes); err != nil {
				return zero, err
			}
			return result, nil
		}
		return zero, errors.New("sched: app task stream returned an empty event")
	}
}

func appTaskRequestToProto(instance string, req apptaskproto.Request) *vmmdpb.ExecuteAppTaskRequest {
	return &vmmdpb.ExecuteAppTaskRequest{
		Instance: instance, Version: uint32(req.Version), TaskId: req.TaskID,
		Command: append([]string(nil), req.Command...), CommandShell: req.CommandShell,
		TimeoutSeconds: int32(req.TimeoutSeconds), MaxOutputBytes: int32(req.MaxOutputBytes),
	}
}

func appTaskResultFromResponse(resp *vmmdpb.ExecuteAppTaskResponse) apptaskproto.Result {
	return mergeAppTaskResponse(apptaskproto.Result{}, resp)
}

func mergeAppTaskResponse(result apptaskproto.Result, resp *vmmdpb.ExecuteAppTaskResponse) apptaskproto.Result {
	result.Status = apptaskproto.Status(resp.GetStatus())
	result.OutputTruncated = resp.GetOutputTruncated()
	result.FailureCode = resp.GetFailureCode()
	result.FailureMessage = resp.GetFailureMessage()
	result.Stdout = append(result.Stdout, resp.GetStdout()...)
	result.Stderr = append(result.Stderr, resp.GetStderr()...)
	if exitCode := resp.GetExitCode(); exitCode != nil {
		value := int(exitCode.GetValue())
		result.ExitCode = &value
	}
	return result
}
