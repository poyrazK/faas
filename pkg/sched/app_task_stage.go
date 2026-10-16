package sched

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/state"
)

// appTaskStageError names the step at which app task preparation failed.
// Stages are fixed labels, so the coordinator can log them without logging
// error text: an operator can tell a placement or admission refusal from a
// vmmd restore failure (SB-8).
type appTaskStageError struct {
	stage string
	err   error
}

func (e *appTaskStageError) Error() string { return e.err.Error() }
func (e *appTaskStageError) Unwrap() error { return e.err }

// withAppTaskStage tags err with stage unless an inner step already tagged it.
func withAppTaskStage(stage string, err error) error {
	if err == nil {
		return nil
	}
	var tagged *appTaskStageError
	if errors.As(err, &tagged) {
		return err
	}
	return &appTaskStageError{stage: stage, err: err}
}

// appTaskFailureStage returns the stage label for a restore failure.
func appTaskFailureStage(err error) string {
	var tagged *appTaskStageError
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, state.ErrAppTaskDeploymentUnavailable):
		return "deployment_unavailable"
	case errors.As(err, &tagged):
		return tagged.stage
	default:
		return "unknown"
	}
}

// appTaskGRPCCode returns the gRPC status code carried by err, or "none" when
// the failure did not come from a gRPC call.
func appTaskGRPCCode(err error) string {
	if err == nil {
		return "none"
	}
	if _, ok := status.FromError(err); !ok {
		return "none"
	}
	if code := status.Code(err); code != codes.OK {
		return code.String()
	}
	return "none"
}
