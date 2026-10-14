package dbvm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

// Spares (ADR-1027 wake latency). A wake is a cold boot: jail, Firecracker,
// guest kernel and guest-init take most of a second before PostgreSQL even
// starts. A spare has done all of that already. It is booted with a
// placeholder behind its volume drive and waits. A database it can serve
// (same major and VM size) adopts it: the database's volume is hard-linked
// into a directory bound into the spare's jail, Firecracker is pointed at
// it, and guest-init mounts it and starts PostgreSQL. A spare serves one
// database once and is destroyed when it parks, like any database VM, so
// no state ever passes between tenants.

// SpareShape is what a spare boots as. A database adopts only a spare of
// its own shape.
type SpareShape struct {
	Major      int
	VcpuCount  int
	MemSizeMiB int
}

const (
	// spareRoot holds each spare's directory, under the volume root so a
	// volume can be hard-linked into it.
	spareRoot = ".spares"
	// spareJailDir is that directory's place inside the jail.
	spareJailDir = "spare"
	// sparePlaceholder backs a spare's volume drive until it adopts one.
	sparePlaceholder      = "placeholder.img"
	sparePlaceholderBytes = 1 << 20
)

// FillSpares boots spares, one at a time, until each configured shape has
// its count waiting, and reports how many it booted. Only one fill runs at
// a time; a concurrent call returns at once.
func (r *Runtime) FillSpares(ctx context.Context) (int, error) {
	if len(r.cfg.Spares) == 0 || !r.filling.TryLock() {
		return 0, nil
	}
	defer r.filling.Unlock()
	if r.sparesStopped {
		return 0, nil
	}
	booted := 0
	for shape, want := range r.cfg.Spares {
		for r.waitingSpares(shape) < want {
			if err := ctx.Err(); err != nil {
				return booted, err
			}
			if err := r.bootSpare(ctx, shape); err != nil {
				return booted, fmt.Errorf("dbvm: boot spare %+v: %w", shape, err)
			}
			booted++
		}
	}
	return booted, nil
}

func (r *Runtime) waitingSpares(shape SpareShape) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, vm := range r.vms {
		if vm.waiting && vm.spare == shape {
			n++
		}
	}
	return n
}

// bootSpare boots one spare of shape and waits until it is ready to adopt.
func (r *Runtime) bootSpare(ctx context.Context, shape SpareShape) (err error) {
	base, ok := r.base(shape.Major)
	if !ok {
		return fmt.Errorf("%w %d", ErrUnknownMajor, shape.Major)
	}
	id := uuid.NewString()
	vm, err := r.reserve(id)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			r.teardown(id, vm)
		}
	}()
	vm.spareDir = filepath.Join(r.cfg.VolumeRoot, spareRoot, id)
	if err = r.prepareSpareDir(vm); err != nil {
		return err
	}
	if err = r.prepareSpareJail(vm, base); err != nil {
		return err
	}
	req := BootRequest{ID: id, Major: shape.Major, VcpuCount: shape.VcpuCount, MemSizeMiB: shape.MemSizeMiB}
	if err = r.writeSpecConfig(vm, Spec{
		KernelPath: jailKernel, BasePath: jailBase, VolumePath: spareJailDir + "/" + sparePlaceholder, Major: shape.Major,
		VcpuCount: shape.VcpuCount, MemSizeMiB: shape.MemSizeMiB,
		GuestCID: fcvm.GuestVsockCID(vm.lease.Slot), VsockUDS: fcvm.VsockUDSSocketName,
		Archive: r.cfg.ArchiveSocket != "", Spare: true,
	}); err != nil {
		return err
	}
	if err = r.startArchiveRelay(vm, id, req); err != nil {
		return err
	}
	if err = r.launch(vm, req); err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, r.cfg.BootTimeout)
	defer cancel()
	alive, stop := context.WithCancel(waitCtx)
	defer stop()
	go func() {
		select {
		case <-vm.exited:
			stop()
		case <-alive.Done():
		}
	}()
	if _, err = WaitState(alive, r.udsPath(vm), dbguest.StateSpare); err != nil {
		return err
	}
	r.mu.Lock()
	vm.spare, vm.waiting = shape, true
	r.mu.Unlock()
	return nil
}

// prepareSpareDir creates the spare's directory and its placeholder,
// owned by the spare's jail uid.
func (r *Runtime) prepareSpareDir(vm *databaseVM) error {
	if err := os.MkdirAll(vm.spareDir, 0o700); err != nil {
		return fmt.Errorf("dbvm: spare dir: %w", err)
	}
	placeholder := filepath.Join(vm.spareDir, sparePlaceholder)
	f, err := os.OpenFile(placeholder, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // under the volume root
	if err != nil {
		return fmt.Errorf("dbvm: spare placeholder: %w", err)
	}
	err = f.Truncate(sparePlaceholderBytes)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("dbvm: spare placeholder: %w", err)
	}
	for _, p := range []string{vm.spareDir, placeholder} {
		if err := os.Chown(p, vm.lease.UID, vm.lease.GID); err != nil {
			return fmt.Errorf("dbvm: spare ownership: %w", err)
		}
	}
	return nil
}

// startFromSpare starts req's database in a waiting spare of its shape and
// reports whether it did. A spare that fails to adopt is destroyed and the
// caller cold-boots instead.
func (r *Runtime) startFromSpare(ctx context.Context, req BootRequest) bool {
	shape := SpareShape{Major: req.Major, VcpuCount: req.VcpuCount, MemSizeMiB: req.MemSizeMiB}
	vm, spareID := r.claimSpare(shape)
	if vm == nil {
		return false
	}
	key, err := r.adopt(ctx, vm, spareID, req)
	if err != nil {
		r.teardown(key, vm)
	}
	// Refill once this wake is done with the CPU.
	go func() { _, _ = r.FillSpares(context.WithoutCancel(ctx)) }()
	return err == nil
}

// claimSpare takes a waiting spare of shape out of the pool.
func (r *Runtime) claimSpare(shape SpareShape) (*databaseVM, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, vm := range r.vms {
		if !vm.waiting || vm.spare != shape {
			continue
		}
		select {
		case <-vm.exited:
			continue
		default:
		}
		vm.waiting = false
		return vm, id
	}
	return nil, ""
}

// adopt hands the spare to req's database and returns the key the VM is
// registered under: the database's once adopted, the spare's before.
func (r *Runtime) adopt(ctx context.Context, vm *databaseVM, spareID string, req BootRequest) (string, error) {
	volume := r.VolumePath(req.ID)
	r.mu.Lock()
	if _, ok := r.vms[req.ID]; ok {
		r.mu.Unlock()
		return spareID, ErrRunning
	}
	if r.transfers[req.ID] {
		r.mu.Unlock()
		return spareID, ErrTransferring
	}
	delete(r.vms, spareID)
	r.vms[req.ID] = vm
	vm.volume = volume
	r.mu.Unlock()
	link := req.ID + ".img"
	if err := os.Link(volume, filepath.Join(vm.spareDir, link)); err != nil {
		return req.ID, fmt.Errorf("dbvm: link volume into spare: %w", err)
	}
	if err := os.Chown(volume, vm.lease.UID, vm.lease.GID); err != nil {
		return req.ID, fmt.Errorf("dbvm: chown volume: %w", err)
	}
	if err := os.Chmod(volume, 0o600); err != nil {
		return req.ID, err
	}
	header := dbarchive.Header{DatabaseID: req.ID, RestoreWindowSeconds: req.RestoreWindowSeconds}
	if err := header.Validate(); err != nil {
		return req.ID, err
	}
	vm.header.Store(&header)
	if err := patchDrive(ctx, filepath.Join(vm.root, fcvm.APISockName), driveUpdate{
		DriveID: DriveVolume, PathOnHost: spareJailDir + "/" + link, RateLimiter: req.VolumeRateLimiter,
	}); err != nil {
		return req.ID, err
	}
	resp, err := Control(ctx, r.udsPath(vm), dbguest.Request{Op: dbguest.OpAdopt})
	if err == nil && resp.Error != "" {
		err = fmt.Errorf("dbvm: adopt: guest reports %s", resp.Error)
	}
	return req.ID, err
}

// driveUpdate is Firecracker's PATCH /drives/{id} body.
type driveUpdate struct {
	DriveID     string            `json:"drive_id"`
	PathOnHost  string            `json:"path_on_host"`
	RateLimiter *fcvm.RateLimiter `json:"rate_limiter,omitempty"`
}

// patchDrive points a running VM's drive at a new backing file. Firecracker
// reopens it and tells the guest the drive changed.
func patchDrive(ctx context.Context, apiSock string, update driveUpdate) error {
	body, err := json.Marshal(update)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", apiSock)
	}}, Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, "http://firecracker/drives/"+update.DriveID, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("dbvm: patch drive: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("dbvm: patch drive: %s: %s", resp.Status, msg)
	}
	return nil
}

// StopSpares stops every spare no database has adopted and ends filling,
// for shutdown: a fill in flight finishes first, and no later one boots
// anything. It reports how many it stopped.
func (r *Runtime) StopSpares(ctx context.Context) int {
	r.filling.Lock()
	r.sparesStopped = true
	r.filling.Unlock()
	r.mu.Lock()
	var ids []string
	for id, vm := range r.vms {
		if vm.waiting {
			vm.waiting = false
			ids = append(ids, id)
		}
	}
	r.mu.Unlock()
	for _, id := range ids {
		_ = r.Stop(ctx, id)
	}
	return len(ids)
}
