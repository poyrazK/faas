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
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
	"golang.org/x/sys/unix"
)

// VsockWorkloadIdentityHostPort is the host endpoint guest-init connects to
// when an app asks for a fresh assertion. It is intentionally distinct from
// the existing telemetry ports (1024-1029).
const VsockWorkloadIdentityHostPort uint32 = 1030

const workloadIdentityMaxFrame = 4096
const workloadIdentityMaxConnections = 64

type workloadIdentityRequest struct {
	Audience string `json:"audience"`
}

type workloadIdentityResponse struct {
	workloadidentity.Token
	Error string `json:"error,omitempty"`
}

// WorkloadIdentityReceiver is a host-CID vsock listener. The peer CID is the
// only credential supplied by the guest; it resolves to a live instance in
// Manager, so a token cannot be requested for another app/account.
type WorkloadIdentityReceiver struct {
	ctx    context.Context
	fd     atomic.Int32
	log    *slog.Logger
	mgr    *fcvm.Manager
	signer *workloadidentity.Signer
	now    func() time.Time
	sem    chan struct{}
}

// StartWorkloadIdentityReceiver binds the host endpoint. A nil signer keeps
// the endpoint available but returns 503 until the operator configures a
// workload identity key; this lets hosts roll out guest-init before enabling
// federation.
func StartWorkloadIdentityReceiver(ctx context.Context, log *slog.Logger, mgr *fcvm.Manager, signer *workloadidentity.Signer) (*WorkloadIdentityReceiver, error) {
	if ctx == nil {
		return nil, errors.New("workload identity vsock: context is required")
	}
	if log == nil {
		log = slog.Default()
	}
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("workload identity vsock socket: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: VsockWorkloadIdentityHostPort}); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("workload identity vsock bind port %d: %w", VsockWorkloadIdentityHostPort, err)
	}
	if err := unix.Listen(fd, 32); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("workload identity vsock listen: %w", err)
	}
	r := &WorkloadIdentityReceiver{ctx: ctx, log: log, mgr: mgr, signer: signer, now: time.Now, sem: make(chan struct{}, workloadIdentityMaxConnections)}
	r.fd.Store(int32(fd))
	go r.loop()
	go func() {
		<-ctx.Done()
		r.Close()
	}()
	log.Info("workload identity receiver started", "vsock_host_port", VsockWorkloadIdentityHostPort, "enabled", signer != nil)
	return r, nil
}

func (r *WorkloadIdentityReceiver) Close() {
	if r == nil {
		return
	}
	old := r.fd.Swap(-1)
	if old >= 0 {
		_ = unix.Close(int(old))
	}
}

func (r *WorkloadIdentityReceiver) loop() {
	for r.fd.Load() >= 0 {
		conn, _, err := unix.Accept4(int(r.fd.Load()), unix.SOCK_CLOEXEC)
		if err != nil {
			if r.fd.Load() >= 0 && !errors.Is(err, unix.EBADF) {
				r.log.Debug("workload identity accept ended", "err", err)
			}
			return
		}
		select {
		case r.sem <- struct{}{}:
			go func() {
				defer func() { <-r.sem }()
				r.handle(conn)
			}()
		default:
			_ = unix.Close(conn)
		}
	}
}

func (r *WorkloadIdentityReceiver) handle(fd int) {
	defer func() { _ = unix.Close(fd) }()
	// An app can open the host-vsock endpoint directly, so bound each
	// connection even though the normal guest proxy has its own deadline.
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Sec: 5})
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &unix.Timeval{Sec: 5})
	peer, err := unix.Getpeername(fd)
	if err != nil {
		r.log.Debug("workload identity peer lookup failed", "err", err)
		return
	}
	sa, ok := peer.(*unix.SockaddrVM)
	if !ok {
		return
	}
	body, err := readIdentityFrame(fd)
	if err != nil {
		r.writeResponse(fd, workloadIdentityResponse{Error: "invalid_request"})
		return
	}
	var req workloadIdentityRequest
	if err := json.Unmarshal(body, &req); err != nil {
		r.writeResponse(fd, workloadIdentityResponse{Error: "invalid_request"})
		return
	}
	if r.signer == nil {
		r.writeResponse(fd, workloadIdentityResponse{Error: "identity_not_configured"})
		return
	}
	if r.mgr == nil {
		r.writeResponse(fd, workloadIdentityResponse{Error: "instance_not_found"})
		return
	}
	instance, appID, accountID, err := r.mgr.InstanceIdentityByCID(sa.CID)
	if err != nil || appID == "" || accountID == "" {
		r.writeResponse(fd, workloadIdentityResponse{Error: "instance_not_found"})
		return
	}
	tok, err := r.signer.Mint(r.now(), accountID, appID, instance, req.Audience)
	if err != nil {
		r.writeResponse(fd, workloadIdentityResponse{Error: "invalid_audience"})
		return
	}
	r.writeResponse(fd, workloadIdentityResponse{Token: tok})
}

func (r *WorkloadIdentityReceiver) writeResponse(fd int, response workloadIdentityResponse) {
	body, err := json.Marshal(response)
	if err != nil || len(body) > workloadIdentityMaxFrame {
		return
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	for len(frame) > 0 {
		n, err := unix.Write(fd, frame)
		if err != nil {
			return
		}
		if n == 0 {
			return
		}
		frame = frame[n:]
	}
}

func readIdentityFrame(fd int) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(identityFDReader{fd: fd}, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > workloadIdentityMaxFrame {
		return nil, fmt.Errorf("invalid frame length %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(identityFDReader{fd: fd}, body); err != nil {
		return nil, err
	}
	return body, nil
}

type identityFDReader struct{ fd int }

func (r identityFDReader) Read(p []byte) (int, error) {
	n, err := unix.Read(r.fd, p)
	if err != nil {
		return n, err
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}
