//go:build !linux

package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

const VsockWorkloadIdentityHostPort uint32 = fcvm.VsockWorkloadIdentityHostPort

type WorkloadIdentityReceiver struct{}

func StartWorkloadIdentityReceiver(context.Context, *slog.Logger, *fcvm.Manager, *workloadidentity.Signer, *fcvm.JailerVMM) (*WorkloadIdentityReceiver, error) {
	return nil, fmt.Errorf("workload identity vsock requires Linux")
}

func (*WorkloadIdentityReceiver) Close() {}
