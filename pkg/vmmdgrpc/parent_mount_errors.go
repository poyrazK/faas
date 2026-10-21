package vmmdgrpc

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func parentMountStatus(err error) error {
	if errors.Is(err, vmmdmount.ErrMountCapacity) {
		return grpcerr.ToStatus(api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"Parent mount capacity exhausted", "retry after an active materialization completes"))
	}
	if errors.Is(err, vmmdmount.ErrMountBusy) {
		return status.Error(codes.FailedPrecondition, "parent mount has an active materialization owner")
	}
	return grpcerr.ToStatus(toProblem(err))
}
