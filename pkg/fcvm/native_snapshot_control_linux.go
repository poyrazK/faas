//go:build linux

// adr: 568 — capture control targets only the original pinned kernel process.
package fcvm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"golang.org/x/sys/unix"
)

type nativeSnapshotControlAction uint8

const (
	nativeSnapshotPause nativeSnapshotControlAction = iota + 1
	nativeSnapshotCreate
	nativeSnapshotResume
)

type linuxNativeSnapshotControl struct{}

func newNativeSnapshotControlBackend() nativeSnapshotControlBackend {
	return linuxNativeSnapshotControl{}
}

func (linuxNativeSnapshotControl) Request(ctx context.Context, socket string, owner nativeLaunchRecord, method, path string, body any) error {
	return nativeSnapshotProcessRequest(ctx, socket, owner, method, path, body)
}

// This single-effect primitive grants no capture-completion or publication
// evidence. The complete producer must own its pause/freeze/resume sequencing
// and storage publication. The ordinary snapshot entry points remain gated.
func (v *JailerVMM) controlNativeQualificationSnapshot(ctx context.Context, lease Lease, action nativeSnapshotControlAction) (err error) {
	r := v.nativeRecovery
	permit, ok := ctx.Value(nativeSnapshotCaptureContextKey{}).(nativeSnapshotCapturePermit)
	if r == nil || r.journal == nil || r.imageSources == nil || !ok || permit.Incoming.Execution.InstanceID != lease.Instance || !sameNativePhysicalLease(lease, permit.Incoming.NativeLease) {
		return fmt.Errorf("native snapshot control: original capture capability is required: %w", state.ErrConflict)
	}
	if _, _, _, err := nativeSnapshotControlRequest(permit.Capture.CaptureID, action); err != nil {
		return err
	}
	ctx, cancel := context.WithDeadline(ctx, permit.Incoming.Deadline)
	defer cancel()
	if r.generation(lease.Instance) != permit.Incoming.NativeGeneration {
		return fmt.Errorf("native snapshot control: original local producer is required: %w", state.ErrConflict)
	}
	if err := r.checkDaemonOwnership(); err != nil {
		return err
	}
	lock, err := r.journal.lock(ctx, lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	owner, err := r.journal.read(lease.Instance)
	if err != nil {
		return err
	}
	return v.controlNativeQualificationSnapshotLocked(ctx, lease, permit, owner, action)
}

// The complete producer holds the physical lock through control, freezing and
// uploads. This helper must never acquire that lock again or retry an effect.
func (v *JailerVMM) controlNativeQualificationSnapshotLocked(ctx context.Context, lease Lease, permit nativeSnapshotCapturePermit, owner nativeLaunchRecord, action nativeSnapshotControlAction) error {
	r := v.nativeRecovery
	method, path, body, err := nativeSnapshotControlRequest(permit.Capture.CaptureID, action)
	if err != nil {
		return err
	}
	if r.generation(lease.Instance) != permit.Incoming.NativeGeneration {
		return errors.New("native snapshot control: original local producer is required")
	}
	if err := r.checkDaemonOwnership(); err != nil {
		return err
	}
	images := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources}
	if err := images.captureOutputAuthority(ctx, permit.Physical, owner, permit); err != nil {
		return err
	}
	if err := images.requireSnapshotControlBindings(owner, v.chrootRoot(lease.Instance), permit.Capture.CaptureID, action); err != nil {
		return err
	}
	if r.snapshotControl == nil {
		return errors.New("native snapshot control: original startup control adapter is required")
	}
	if err := r.snapshotControl.Request(ctx, v.socketPath(lease.Instance), owner, method, path, body); err != nil {
		return err // A lost response never retries a possibly completed effect.
	}
	if r.generation(lease.Instance) != permit.Incoming.NativeGeneration {
		return errors.New("native snapshot control: original local producer changed during the effect")
	}
	if err := r.checkDaemonOwnership(); err != nil {
		return err
	}
	return images.captureOutputAuthority(ctx, permit.Physical, owner, permit)
}

func nativeSnapshotControlRequest(capture string, action nativeSnapshotControlAction) (method, path string, body any, err error) {
	if !canonicalNativeHelperID(capture) {
		return "", "", nil, errors.New("native snapshot control: original capture identity is required")
	}
	switch action {
	case nativeSnapshotPause:
		return http.MethodPatch, "/vm", map[string]any{"state": "Paused"}, nil
	case nativeSnapshotResume:
		return http.MethodPatch, "/vm", map[string]any{"state": "Resumed"}, nil
	case nativeSnapshotCreate:
		memory, _ := nativeSnapshotOutputName(capture, "mem")
		vmstate, _ := nativeSnapshotOutputName(capture, "vmstate")
		return http.MethodPut, "/snapshot/create", map[string]any{"snapshot_type": "Full", "snapshot_path": vmstate, "mem_file_path": memory}, nil
	default:
		return "", "", nil, errors.New("native snapshot control: unsupported effect")
	}
}

func (j *nativeImageSourceJournal) requireSnapshotControlBindings(owner nativeLaunchRecord, root, capture string, action nativeSnapshotControlAction) error {
	drive, ref, err := j.snapshotDrive(owner, root)
	if err != nil {
		return err
	}
	if err := errors.Join(j.backend.CheckAnchor(drive, j.anchor(drive)), j.backend.CheckReference(drive, ref)); err != nil {
		return err
	}
	if action != nativeSnapshotCreate {
		return nil
	}
	for _, kind := range []string{"mem", "vmstate"} {
		name, err := nativeSnapshotOutputName(capture, kind)
		if err != nil {
			return err
		}
		record, ref, err := j.captureOutput(owner, root, name)
		if err != nil {
			return err
		}
		if err := errors.Join(j.backend.CheckAnchor(record, j.anchor(record)), j.backend.CheckReference(record, ref)); err != nil {
			return err
		}
	}
	return nil
}

// Keep a pidfd through the complete one-shot request. No cached client,
// redirects, connection reuse or transport retries can substitute an API peer.
func nativeSnapshotProcessRequest(ctx context.Context, socket string, owner nativeLaunchRecord, method, path string, body any) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !liveNativeSnapshotOwner(owner) {
		return errors.New("native snapshot control: original live process is required")
	}
	handle, err := openNativeProcess(owner.PID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, handle.Close()) }()
	pinned := handle.(*nativePIDFD)
	if err := checkNativeSnapshotProcess(pinned, owner); err != nil {
		return err
	}
	connection, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", socket)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := connection.Close(); !errors.Is(closeErr, net.ErrClosed) {
			err = errors.Join(err, closeErr)
		}
	}()
	if err := checkNativeSnapshotPeer(connection, owner); err != nil {
		return err
	}
	if err := checkNativeSnapshotProcess(pinned, owner); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Close = true
	request.Header.Set("Content-Type", "application/json")
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := request.Write(connection); err != nil {
		return errors.Join(err, ctx.Err())
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), request)
	if err != nil {
		return errors.Join(err, ctx.Err())
	}
	defer func() { err = errors.Join(err, connection.Close(), response.Body.Close()) }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		return errors.Join(readErr, &fcAPIError{method: method, path: path, statusCode: response.StatusCode, statusText: response.Status, body: string(bytes.TrimSpace(message))})
	}
	return errors.Join(checkNativeSnapshotProcess(pinned, owner), ctx.Err())
}

func checkNativeSnapshotProcess(pinned *nativePIDFD, owner nativeLaunchRecord) error {
	fds := []unix.PollFd{{Fd: int32(pinned.fd), Events: unix.POLLIN}}
	if _, err := unix.Poll(fds, 0); err != nil {
		return err
	}
	if fds[0].Revents != 0 {
		return errors.New("native snapshot control: original pinned process exited or is unavailable")
	}
	start, err := nativeHostHelperStartTime(owner.PID)
	if err != nil || start != owner.StartTime {
		return errors.Join(err, errors.New("native snapshot control: original kernel process identity changed"))
	}
	return nil
}

func checkNativeSnapshotPeer(connection net.Conn, owner nativeLaunchRecord) error {
	peer, ok := connection.(*net.UnixConn)
	if !ok {
		return errors.New("native snapshot control: original Unix API connection is required")
	}
	raw, err := peer.SyscallConn()
	if err != nil {
		return err
	}
	var credentials *unix.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if socketErr != nil || credentials == nil || int(credentials.Pid) != owner.PID || int(credentials.Uid) != owner.Lease.UID || int(credentials.Gid) != owner.Lease.GID {
		return errors.Join(socketErr, errors.New("native snapshot control: API peer differs from the original physical owner"))
	}
	return nil
}
