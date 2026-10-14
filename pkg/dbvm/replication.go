package dbvm

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// Streaming replication (ADR-1027 production class). A production
// database's standby runs on another node than its primary, with the same
// database id. The standby's guest opens replication streams to the host;
// the runtime relays each to a stream the BootRequest's Primary dials,
// which vmmd carries to the primary's node, where DialReplication hands it
// to the primary's guest. The guests authenticate nothing to each other:
// the relay is the binding, and it only ever joins a database to itself.

// ReplicationDialer opens a replication stream to the database's primary.
type ReplicationDialer func(ctx context.Context) (net.Conn, error)

const (
	// replicationDialTimeout bounds reaching the primary; the guest's
	// walreceiver retries a failed stream.
	replicationDialTimeout = 10 * time.Second
	// replicationStreamsPerVM caps a standby's concurrent streams: its
	// walreceiver, or a seed's two connections.
	replicationStreamsPerVM = 4
)

// replicationUDSPath is where Firecracker delivers the guest's
// connections to the replication port: <uds>_<port>, inside the jail.
func (r *Runtime) replicationUDSPath(vm *databaseVM) string {
	return r.udsPath(vm) + "_" + strconv.FormatUint(uint64(dbguest.ReplicationPort), 10)
}

// startReplicationRelay relays a standby's replication streams to its
// primary. A VM booted without a primary gets no relay.
func (r *Runtime) startReplicationRelay(vm *databaseVM, primary ReplicationDialer) error {
	if primary == nil {
		return nil
	}
	path := r.replicationUDSPath(vm)
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("dbvm: replication relay: %w", err)
	}
	// Firecracker runs as the jail's uid and connects to this socket.
	if err := os.Chown(path, vm.lease.UID, vm.lease.GID); err != nil {
		_ = ln.Close()
		return fmt.Errorf("dbvm: replication relay: %w", err)
	}
	vm.replication = ln
	slots := make(chan struct{}, replicationStreamsPerVM)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case slots <- struct{}{}:
			default:
				_ = conn.Close()
				continue
			}
			go func() {
				defer func() { <-slots }()
				relayReplication(conn, primary)
			}()
		}
	}()
	return nil
}

func relayReplication(guest net.Conn, primary ReplicationDialer) {
	defer func() { _ = guest.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), replicationDialTimeout)
	remote, err := primary(ctx)
	cancel()
	if err != nil {
		return
	}
	defer func() { _ = remote.Close() }()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, guest); done <- struct{}{} }()
	go func() { _, _ = io.Copy(guest, remote); done <- struct{}{} }()
	<-done
}

// DialReplication opens a replication stream to a running primary's
// guest, for a standby's relayed stream.
func (r *Runtime) DialReplication(ctx context.Context, id string) (net.Conn, error) {
	vm, err := r.running(id)
	if err != nil {
		return nil, err
	}
	return Dial(ctx, r.udsPath(vm), dbguest.ReplicationPort)
}
