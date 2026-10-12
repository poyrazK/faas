//go:build !linux

package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

const VsockOutboundRelayHostPort uint32 = fcvm.VsockOutboundRelayHostPort

type OutboundRelayReceiver struct{}

func StartOutboundRelayReceiver(context.Context, *slog.Logger, *fcvm.Manager, *workloadidentity.Signer, outboundRelayDialer, *fcvm.JailerVMM) (*OutboundRelayReceiver, error) {
	return nil, fmt.Errorf("outbound relay vsock requires Linux")
}

func (*OutboundRelayReceiver) Close() {}
