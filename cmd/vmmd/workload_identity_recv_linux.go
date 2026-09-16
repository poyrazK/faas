//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// VsockWorkloadIdentityHostPort is the host endpoint guest-init connects to
// when an app asks for a fresh assertion. It is intentionally distinct from
// the existing telemetry ports (1024-1029).
const VsockWorkloadIdentityHostPort uint32 = fcvm.VsockWorkloadIdentityHostPort

const workloadIdentityMaxFrame = 4096

type workloadIdentityRequest struct {
	Audience string `json:"audience"`
}

type workloadIdentityResponse struct {
	workloadidentity.Token
	Error string `json:"error,omitempty"`
}

// WorkloadIdentityReceiver handles Firecracker's per-instance Unix bridge. The
// listener association is the credential: a guest cannot request a token for
// another app/account because the instance id is never supplied on the wire.
type WorkloadIdentityReceiver struct {
	ctx    context.Context
	log    *slog.Logger
	mgr    *fcvm.Manager
	signer *workloadidentity.Signer
	now    func() time.Time
}

// StartWorkloadIdentityReceiver binds the host endpoint. A nil signer keeps
// the endpoint available but returns 503 until the operator configures a
// workload identity key; this lets hosts roll out guest-init before enabling
// federation.
func StartWorkloadIdentityReceiver(ctx context.Context, log *slog.Logger, mgr *fcvm.Manager, signer *workloadidentity.Signer, jailer *fcvm.JailerVMM) (*WorkloadIdentityReceiver, error) {
	if ctx == nil {
		return nil, errors.New("workload identity vsock: context is required")
	}
	if jailer == nil {
		return nil, errors.New("workload identity vsock: jailer is required")
	}
	if log == nil {
		log = slog.Default()
	}
	r := &WorkloadIdentityReceiver{ctx: ctx, log: log, mgr: mgr, signer: signer, now: time.Now}
	if err := jailer.RegisterGuestVsockStreamHandler(VsockWorkloadIdentityHostPort, r.handleGuestStream); err != nil {
		return nil, fmt.Errorf("workload identity receiver register port %d: %w", VsockWorkloadIdentityHostPort, err)
	}
	log.Info("workload identity receiver registered", "vsock_host_port", VsockWorkloadIdentityHostPort, "transport", "firecracker_uds", "enabled", signer != nil)
	return r, nil
}

func (r *WorkloadIdentityReceiver) Close() {}

func (r *WorkloadIdentityReceiver) handleGuestStream(instance string, conn net.Conn) (string, error) {
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return "read", fmt.Errorf("workload identity set deadline: %w", err)
	}
	body, err := readIdentityFrame(conn)
	if err != nil {
		_ = r.writeResponse(conn, workloadIdentityResponse{Error: "invalid_request"})
		return "read", fmt.Errorf("workload identity read: %w", err)
	}
	var req workloadIdentityRequest
	if err := json.Unmarshal(body, &req); err != nil {
		_ = r.writeResponse(conn, workloadIdentityResponse{Error: "invalid_request"})
		return "protocol", fmt.Errorf("workload identity request: %w", err)
	}
	if r.signer == nil {
		return responseResult(r.writeResponse(conn, workloadIdentityResponse{Error: "identity_not_configured"}))
	}
	if r.mgr == nil {
		return responseResult(r.writeResponse(conn, workloadIdentityResponse{Error: "instance_not_found"}))
	}
	appID, accountID, err := r.mgr.InstanceIdentity(instance)
	if err != nil || appID == "" || accountID == "" {
		return responseResult(r.writeResponse(conn, workloadIdentityResponse{Error: "instance_not_found"}))
	}
	tok, err := r.signer.Mint(r.now(), accountID, appID, instance, req.Audience)
	if err != nil {
		return responseResult(r.writeResponse(conn, workloadIdentityResponse{Error: "invalid_audience"}))
	}
	return responseResult(r.writeResponse(conn, workloadIdentityResponse{Token: tok}))
}

func responseResult(err error) (string, error) {
	if err != nil {
		return "write", err
	}
	return "", nil
}

func (r *WorkloadIdentityReceiver) writeResponse(w io.Writer, response workloadIdentityResponse) error {
	body, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("workload identity response marshal: %w", err)
	}
	if len(body) > workloadIdentityMaxFrame {
		return fmt.Errorf("workload identity response exceeds %d bytes", workloadIdentityMaxFrame)
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return fmt.Errorf("workload identity response write: %w", err)
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

func readIdentityFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > workloadIdentityMaxFrame {
		return nil, fmt.Errorf("invalid frame length %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}
