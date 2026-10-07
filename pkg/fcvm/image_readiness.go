// adr:643
package fcvm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/healthcheckproto"
)

// waitApplicationReady applies the command gate after either network readiness
// or an allowed no-bind characterization, within the same startup budget.
func (v *JailerVMM) waitApplicationReady(ctx context.Context, l Lease, path string, grpc bool, service string, startupS int, mode string, required bool, receipt *characterizationReceipt) error {
	if required {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, readyTimeoutFor(v.readyTimeout, startupS))
		defer cancel()
	}
	var err error
	if receipt != nil {
		err = v.waitReadyOrCharacterized(ctx, l, path, grpc, service, startupS, mode, receipt)
	} else {
		err = v.waitReadyWithProbe(ctx, l, path, grpc, service, startupS)
	}
	if err != nil || !required {
		return err
	}
	return v.WaitImageHealthcheck(ctx, l, startupS)
}

// WaitImageHealthcheck executes a fresh challenge on this VM's private vsock
// proxy. DGRAM reports and cached/snapshotted passes never authorize readiness.
func (v *JailerVMM) WaitImageHealthcheck(ctx context.Context, l Lease, startupS int) error {
	ctx, cancel := context.WithTimeout(ctx, readyTimeoutFor(v.readyTimeout, startupS))
	defer cancel()
	for {
		resp, err := probeImageHealthcheck(ctx, v.VsockUDSSocketPath(l.Instance))
		if err == nil {
			if resp.Healthy && resp.Error == "" {
				if config, err := ReadImageHealthcheckConfig(ctx, v.VsockUDSSocketPath(l.Instance)); err != nil || config.RuntimeID != resp.RuntimeID {
					return imageHealthcheckProblem(l, "fresh runtime monitor configuration unavailable")
				}
				return nil
			}
			if resp.Error != "runtime_unavailable" {
				return imageHealthcheckProblem(l, "guest command check failed: "+resp.Error)
			}
		}
		// Listener/runtime publication can lag the network socket on cold boot.
		// Retry transport failures, but never fall back to TCP/HTTP success.
		select {
		case <-ctx.Done():
			return imageHealthcheckProblem(l, "fresh guest command result unavailable before the startup deadline")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func probeImageHealthcheck(ctx context.Context, socket string) (healthcheckproto.Response, error) {
	var response healthcheckproto.Response
	nonce, err := imageHealthcheckExchange(ctx, socket, healthcheckproto.Probe, healthcheckproto.Ack, "", &response)
	if err != nil {
		return response, err
	}
	if response.Nonce != nonce {
		return healthcheckproto.Response{}, fmt.Errorf("healthcheck challenge mismatch")
	}
	switch response.Error {
	case "", "invalid_request", "runtime_unavailable", "runtime_changed", "healthcheck_missing", "unhealthy", "deadline":
	default:
		return healthcheckproto.Response{}, fmt.Errorf("invalid healthcheck outcome")
	}
	return response, nil
}

func imageHealthcheckExchange(ctx context.Context, socket string, probe, ackKind uint32, runtimeID string, response any) (string, error) {
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("healthcheck challenge: %w", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) < time.Millisecond {
		return "", context.DeadlineExceeded
	}
	remaining := time.Until(deadline)
	budget := remaining.Milliseconds()
	if remaining%time.Millisecond != 0 && budget < healthcheckproto.MaxProbeBudgetMS {
		budget++
	}
	if probe == healthcheckproto.Probe {
		budget = min(budget, int64(api.MaxAppManifestStartupDeadlineS)*1000)
	}
	request := healthcheckproto.Request{Nonce: hex.EncodeToString(nonce[:]), BudgetMS: min(budget, healthcheckproto.MaxProbeBudgetMS), RuntimeID: runtimeID}
	conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "unix", socket)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := conn.SetDeadline(deadline); err != nil {
		return "", err
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %d\n", healthcheckproto.Port); err != nil {
		return "", err
	}
	ack, err := readConnectAck(conn)
	if err != nil || ack != "OK" {
		return "", fmt.Errorf("healthcheck CONNECT rejected")
	}
	if err := healthcheckproto.Write(conn, probe, request); err != nil {
		return "", err
	}
	if err := healthcheckproto.Read(conn, ackKind, response); err != nil {
		return "", err
	}
	return request.Nonce, nil
}

func imageHealthcheckProblem(l Lease, detail string) *api.Problem {
	return api.NewProblem(422, api.CodeAppStartupTimeout, "image healthcheck did not become ready",
		fmt.Sprintf("startup_phase=image_healthcheck: guest %s: %s", l.Instance, detail))
}
