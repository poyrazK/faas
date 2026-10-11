// adr: 568 — restored channel identity belongs to a live original target.
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Callbacks are private qualification adapters, selected before opening any
// endpoint. A frame field, captured CID or ordinary Manager identity cannot
// select a serving receiver or credential source.
type nativeQualificationRestoreStreamHandler func(context.Context, state.EnvironmentQualificationExecution, net.Conn) error

func nativeRestoreChannelPorts() [3]uint32 {
	return [3]uint32{VsockGuestEventHostPort, VsockWorkloadIdentityHostPort, VsockRuntimeConfigHostPort}
}

// RegisterEnvironmentQualificationRestoreStreamHandler installs one
// qualification-only platform callback. The dedicated restore transport
// never adapts or falls back to a serving receiver.
func (v *JailerVMM) RegisterEnvironmentQualificationRestoreStreamHandler(port uint32, handler func(context.Context, state.EnvironmentQualificationExecution, net.Conn) error) error {
	if v == nil || port == 0 || handler == nil {
		return errors.New("native restore channels: VMM, supported port, and handler are required")
	}
	supported := false
	for _, candidate := range nativeRestoreChannelPorts() {
		if port == candidate {
			supported = true
			break
		}
	}
	if !supported {
		return fmt.Errorf("native restore channels: port %d is not a supported platform endpoint", port)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.nativeQualificationRestoreStreamHandlers == nil {
		v.nativeQualificationRestoreStreamHandlers = make(map[uint32]nativeQualificationRestoreStreamHandler)
	}
	if _, exists := v.nativeQualificationRestoreStreamHandlers[port]; exists {
		return fmt.Errorf("native restore channels: handler already registered on port %d", port)
	}
	v.nativeQualificationRestoreStreamHandlers[port] = handler
	return nil
}

// nativeQualificationRestoreHandlers binds the registered platform handlers
// to the first live restore producer. The channel server still supplies the
// execution frame, but it cannot choose a handler or an instance identity;
// both come from the persisted restore authority and daemon registration.
func (v *JailerVMM) nativeQualificationRestoreHandlers(ctx context.Context, lease Lease) (handlers map[uint32]nativeQualificationRestoreStreamHandler, result error) {
	if v == nil || ctx == nil {
		return nil, errors.New("native restore channels: VMM and context are required")
	}
	r := v.nativeRecovery
	target, ok := ctx.Value(nativeQualificationRestoreContextKey{}).(nativeQualificationRestoreRecord)
	if r == nil || r.journal == nil || !ok || target.Execution.InstanceID != lease.Instance ||
		target.validate(target.Execution.NodeID) != nil || ctx.Err() != nil {
		return nil, errors.Join(ctx.Err(), errors.New("native restore channels: original target capability is required"))
	}
	lock, producer, err := r.journal.lockQualificationProducer(ctx, lease.Instance)
	if err != nil {
		return nil, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	if producer == nil || producer.restore == nil || *producer.restore != target || producer.NativeGeneration == "" ||
		!sameNativePhysicalLease(lease, producer.NativeLease) || r.generation(lease.Instance) != producer.NativeGeneration {
		return nil, errors.New("native restore channels: original live target producer changed")
	}

	v.mu.Lock()
	ports := nativeRestoreChannelPorts()
	registered := make(map[uint32]nativeQualificationRestoreStreamHandler, len(ports))
	for _, port := range ports {
		if handler := v.nativeQualificationRestoreStreamHandlers[port]; handler != nil {
			registered[port] = handler
		}
	}
	v.mu.Unlock()
	if len(registered) != len(ports) {
		return nil, errors.New("native restore channels: all registered platform handlers are required")
	}

	handlers = make(map[uint32]nativeQualificationRestoreStreamHandler, len(registered))
	for port, handler := range registered {
		port, handler := port, handler
		handlers[port] = func(streamCtx context.Context, execution state.EnvironmentQualificationExecution, conn net.Conn) error {
			if execution != target.Execution {
				return errors.New("native restore channels: stream execution differs from original target")
			}
			if err := streamCtx.Err(); err != nil {
				return err
			}
			if err := handler(streamCtx, execution, conn); err != nil {
				return fmt.Errorf("native restore platform handler %d: %w", port, err)
			}
			return nil
		}
	}
	return handlers, nil
}

type nativeQualificationRestoreChannelPeer interface {
	nativeQualificationRestoreFence
	RequirePeer(context.Context, net.Conn) error
}
type nativeQualificationRestoreChannelBackend interface {
	Pin(context.Context, nativeLaunchRecord) (nativeQualificationRestoreChannelPeer, error)
}

type nativeQualificationRestoreEndpoint struct {
	listener *net.UnixListener
	file     *os.File
	identity nativeLoopIdentity
}

// This process-local object is never reconstructed by inventory. The sockets
// retain their names on close for original jail retirement; no late accept
// loop unlinks a replacement endpoint.
type nativeQualificationRestoreChannels struct {
	v         *JailerVMM
	ctx       context.Context
	cancel    context.CancelFunc
	target    nativeQualificationRestoreRecord
	owner     nativeLaunchRecord
	loaded    nativeQualificationRestoreLoadRecord
	permit    *nativeQualificationRestoreLoadPermit
	root      *os.File
	identity  nativeLoopIdentity
	endpoints map[uint32]nativeQualificationRestoreEndpoint
	wg        sync.WaitGroup
	closeOnce sync.Once
}

func (c *nativeQualificationRestoreChannels) Close() {
	c.closeOnce.Do(func() {
		c.cancel()
		for _, endpoint := range c.endpoints {
			_ = endpoint.listener.Close()
		}
		c.wg.Wait()
		for _, endpoint := range c.endpoints {
			if endpoint.file != nil {
				_ = endpoint.file.Close()
			}
		}
		_ = c.root.Close()
		c.v.mu.Lock()
		if c.v.nativeRestoreChannels[c.owner.Lease.Instance] == c {
			delete(c.v.nativeRestoreChannels, c.owner.Lease.Instance)
		}
		c.v.mu.Unlock()
	})
}

func (v *JailerVMM) closeNativeQualificationRestoreChannels(instance string) {
	v.mu.Lock()
	channels := v.nativeRestoreChannels[instance]
	v.mu.Unlock()
	if channels != nil {
		channels.Close()
	}
}

func (c *nativeQualificationRestoreChannels) serve(ctx context.Context, port uint32, handler nativeQualificationRestoreStreamHandler) {
	defer c.wg.Done()
	endpoint := c.endpoints[port]
	sem := make(chan struct{}, api.NativeQualificationRestoreMaxStreams)
	for {
		conn, err := endpoint.listener.AcceptUnix()
		if err != nil {
			c.cancel() // Terminal listener failure closes the entire private group.
			return
		}
		select {
		case sem <- struct{}{}:
			c.wg.Add(1)
			go func() {
				defer c.wg.Done()
				defer func() { <-sem }()
				defer func() { _ = conn.Close() }()
				_ = c.handle(ctx, port, conn, handler)
			}()
		default:
			_ = conn.Close()
		}
	}
}

func (c *nativeQualificationRestoreChannels) handle(ctx context.Context, port uint32, conn net.Conn, handler nativeQualificationRestoreStreamHandler) (result error) {
	ctx, cancel := context.WithTimeout(ctx, api.NativeQualificationRestoreStreamTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	peer, err := c.v.nativeRecovery.restoreChannels.Pin(ctx, c.owner)
	if err != nil {
		return err
	}
	stream := &nativeQualificationRestoreConn{Conn: conn, ctx: ctx, require: func() error {
		return errors.Join(c.require(ctx, port), requireNativeRestoreFenceGroup(c.loaded.Cgroup, peer.Group()), peer.RequirePeer(ctx, conn))
	}}
	defer func() {
		cancel()
		_ = conn.Close()
		result = errors.Join(result, stream.closeAuthority(peer.Close))
	}()
	if err := stream.requireCurrent(); err != nil {
		return err
	}
	deadline, _ := ctx.Deadline()
	if err := stream.SetDeadline(deadline); err != nil {
		return err
	}
	if err := handler(ctx, c.target.Execution, stream); err != nil {
		return err
	}
	return stream.requireCurrent()
}

// Check again around every I/O, and never return consumed request bytes after
// revocation. Handlers still revalidate their reviewed graph/runtime contract
// before each state or binding effect; transport authority is not graph proof.
type nativeQualificationRestoreConn struct {
	net.Conn
	ctx     context.Context
	require func() error
	mu      sync.Mutex // cgroup limit reads share a seekable pinned descriptor
	closed  bool
}

func (c *nativeQualificationRestoreConn) requireCurrent() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	return errors.Join(c.ctx.Err(), c.require())
}

// A callback cannot retain descriptor authority after returning, including
// through asynchronous reads/writes it started while its stream was live.
func (c *nativeQualificationRestoreConn) closeAuthority(closePeer func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return closePeer()
}

func (c *nativeQualificationRestoreConn) Read(body []byte) (int, error) {
	if err := c.requireCurrent(); err != nil {
		return 0, err
	}
	n, err := c.Conn.Read(body)
	if authorityErr := c.requireCurrent(); authorityErr != nil {
		clear(body[:n])
		return 0, errors.Join(err, authorityErr)
	}
	return n, err
}
func (c *nativeQualificationRestoreConn) Write(body []byte) (int, error) {
	if err := c.requireCurrent(); err != nil {
		return 0, err
	}
	n, err := c.Conn.Write(body)
	return n, errors.Join(err, c.requireCurrent()) // An uncertain write is never retried here.
}
func (c *nativeQualificationRestoreConn) boundedDeadline(deadline time.Time) time.Time {
	if original, ok := c.ctx.Deadline(); ok && (deadline.IsZero() || deadline.After(original)) {
		return original
	}
	return deadline
}
func (c *nativeQualificationRestoreConn) SetDeadline(deadline time.Time) error {
	return c.Conn.SetDeadline(c.boundedDeadline(deadline))
}
func (c *nativeQualificationRestoreConn) SetReadDeadline(deadline time.Time) error {
	return c.Conn.SetReadDeadline(c.boundedDeadline(deadline))
}
func (c *nativeQualificationRestoreConn) SetWriteDeadline(deadline time.Time) error {
	return c.Conn.SetWriteDeadline(c.boundedDeadline(deadline))
}
