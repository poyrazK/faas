//go:build linux

package fcvm

import (
	"context"
	"errors"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

type linuxNativeQualificationRestoreChannelBackend struct{}
type linuxNativeQualificationRestoreChannelPeer struct {
	*linuxNativeQualificationRestoreFence
	owner nativeLaunchRecord
}

func newNativeQualificationRestoreChannelBackend() nativeQualificationRestoreChannelBackend {
	return linuxNativeQualificationRestoreChannelBackend{}
}
func (linuxNativeQualificationRestoreChannelBackend) Pin(ctx context.Context, owner nativeLaunchRecord) (nativeQualificationRestoreChannelPeer, error) {
	fence, err := (linuxNativeQualificationRestoreFenceBackend{}).Pin(ctx, owner)
	if err != nil {
		return nil, err
	}
	return &linuxNativeQualificationRestoreChannelPeer{linuxNativeQualificationRestoreFence: fence.(*linuxNativeQualificationRestoreFence), owner: owner}, nil
}
func (p *linuxNativeQualificationRestoreChannelPeer) RequirePeer(ctx context.Context, conn net.Conn) error {
	return errors.Join(p.Require(ctx), checkNativeSnapshotPeer(conn, p.owner), ctx.Err())
}

func nativeRestoreChannelPorts() [3]uint32 {
	return [3]uint32{VsockGuestEventHostPort, VsockWorkloadIdentityHostPort, VsockRuntimeConfigHostPort}
}

// Open only after the live producer observed the durable hook acknowledgement.
// A completed on-disk record cannot recreate that capability after a lost ACK,
// daemon restart, cancellation or any partial listener publication.
func (v *JailerVMM) prepareNativeQualificationRestoreChannels(ctx context.Context, lease Lease, handlers map[uint32]nativeQualificationRestoreStreamHandler) (channels *nativeQualificationRestoreChannels, result error) {
	r := v.nativeRecovery
	ports := nativeRestoreChannelPorts()
	permit, _ := ctx.Value(nativeQualificationRestoreLoadContextKey{}).(*nativeQualificationRestoreLoadPermit)
	if r == nil || r.journal == nil || r.restoreChannels == nil || permit == nil || permit.instance != lease.Instance ||
		!permit.used.Load() || !permit.completed.Load() || len(handlers) != len(ports) {
		return nil, errors.New("native restore channels: acknowledged original producer and explicit private handlers are required")
	}
	for _, port := range ports {
		if handlers[port] == nil {
			return nil, errors.New("native restore channels: unsupported or missing private platform handler")
		}
	}
	lock, producer, err := r.journal.lockQualificationProducer(ctx, lease.Instance)
	if err != nil {
		return nil, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	if producer == nil || producer.restore == nil || permit.generation != producer.restore.Generation || !sameNativePhysicalLease(lease, producer.NativeLease) {
		return nil, errors.New("native restore channels: original target binding is required")
	}
	physicalLock, err := r.journal.lock(ctx, lease.Instance)
	if err != nil {
		return nil, err
	}
	defer func() { result = errors.Join(result, physicalLock.Close()) }()
	owner, err := r.journal.read(lease.Instance)
	if err != nil {
		return nil, err
	}
	loaded, err := r.journal.qualifications(producer.Execution.NodeID).restores().loads().read(lease.Instance)
	if err != nil || loaded.Phases[nativeRestoreHookCompleted].IsZero() {
		return nil, errors.Join(err, errors.New("native restore channels: original hook completion is unavailable"))
	}
	ctx, cancel := context.WithDeadline(ctx, producer.restore.Deadline)
	c := &nativeQualificationRestoreChannels{v: v, ctx: ctx, cancel: cancel, target: *producer.restore, owner: owner, loaded: loaded, permit: permit,
		endpoints: make(map[uint32]nativeQualificationRestoreEndpoint)}
	defer func() {
		if result != nil {
			c.Close()
		}
	}()
	if err := c.requireOwner(ctx); err != nil {
		return nil, err
	}
	peer, err := r.restoreChannels.Pin(ctx, owner)
	if err != nil {
		return nil, err
	}
	defer func() { result = errors.Join(result, peer.Close()) }()
	if err := errors.Join(peer.Require(ctx), requireNativeRestoreFenceGroup(loaded.Cgroup, peer.Group())); err != nil {
		return nil, err
	}
	fd, err := unix.Open(v.chrootRoot(lease.Instance), unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	c.root = os.NewFile(uintptr(fd), "native-restore-channel-root")
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, err
	}
	c.identity = nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}
	if err := c.requireRoot(); err != nil {
		return nil, err
	}
	if !permit.channelsUsed.CompareAndSwap(false, true) {
		return nil, errors.New("native restore channels: original endpoint producer was consumed")
	}
	for _, port := range ports {
		if err := peer.Require(ctx); err != nil {
			return nil, err
		}
		if err := c.prepareEndpoint(port); err != nil {
			return nil, err
		}
	}
	v.mu.Lock()
	if v.nativeRestoreChannels == nil {
		v.nativeRestoreChannels = make(map[string]*nativeQualificationRestoreChannels)
	}
	if v.nativeRestoreChannels[lease.Instance] != nil {
		v.mu.Unlock()
		return nil, errors.New("native restore channels: target already owns private endpoints")
	}
	v.nativeRestoreChannels[lease.Instance] = c
	v.mu.Unlock()
	for _, port := range ports {
		c.wg.Add(1)
		go c.serve(ctx, port, handlers[port])
	}
	go func() { <-ctx.Done(); c.Close() }()
	return c, nil
}

func (c *nativeQualificationRestoreChannels) requireOwner(ctx context.Context) error {
	r := c.v.nativeRecovery
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.permit == nil || c.permit.instance != c.target.Execution.InstanceID || c.permit.generation != c.target.Generation ||
		!c.permit.used.Load() || !c.permit.completed.Load() || !liveNativeSnapshotOwner(c.owner) || c.owner.Generation != c.target.NativeGeneration ||
		c.owner.KernelBootID != c.target.KernelBootID || !sameNativePhysicalLease(c.owner.Lease, c.target.NativeLease) || r.generation(c.owner.Lease.Instance) != c.owner.Generation {
		return errors.New("native restore channels: original live target changed")
	}
	if err := r.checkDaemonOwnership(); err != nil {
		return err
	}
	current, err := r.journal.read(c.owner.Lease.Instance)
	if err != nil || current != c.owner {
		return errors.Join(err, errors.New("native restore channels: original process record changed"))
	}
	j := r.journal.qualifications(c.target.Execution.NodeID).restores()
	producer, err := j.producer(ctx, c.owner.Lease.Instance, c.target)
	if err != nil || producer == nil || producer.restore == nil || *producer.restore != c.target {
		return errors.Join(err, errors.New("native restore channels: original target was changed or revoked"))
	}
	loaded, err := j.loads().read(c.owner.Lease.Instance)
	if err != nil || loaded != c.loaded || loaded.Phases[nativeRestoreHookCompleted].IsZero() {
		return errors.Join(err, errors.New("native restore channels: original effect evidence changed"))
	}
	capture, err := j.requireCapture(ctx, c.target)
	if err != nil {
		return err
	}
	backings, err := j.incoming.readBackings(capture)
	if err != nil {
		return err
	}
	images := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources}
	return errors.Join(loaded.requireOriginal(c.target, capture, backings, c.owner),
		images.requireRestoreLoadWitnesses(loaded, c.owner, c.v.chrootRoot(c.owner.Lease.Instance), false), ctx.Err())
}

func (c *nativeQualificationRestoreChannels) requireRoot() error {
	var pinned, current unix.Stat_t
	if c.root == nil {
		return errors.New("native restore channels: original jail directory is unavailable")
	}
	if err := unix.Fstat(int(c.root.Fd()), &pinned); err != nil {
		return err
	}
	if err := unix.Lstat(c.v.chrootRoot(c.owner.Lease.Instance), &current); err != nil {
		return err
	}
	if pinned.Mode&unix.S_IFMT != unix.S_IFDIR || current.Mode&unix.S_IFMT != unix.S_IFDIR ||
		(nativeLoopIdentity{Device: uint64(pinned.Dev), Inode: pinned.Ino}) != c.identity ||
		(nativeLoopIdentity{Device: uint64(current.Dev), Inode: current.Ino}) != c.identity ||
		int(current.Uid) != c.owner.Lease.UID || int(current.Gid) != c.owner.Lease.GID {
		return errors.New("native restore channels: original jail namespace changed")
	}
	return nil
}

func (c *nativeQualificationRestoreChannels) endpointIdentity(port uint32) (nativeLoopIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Lstat(c.v.guestVsockUDSSock(c.owner.Lease.Instance, port), &stat); err != nil {
		return nativeLoopIdentity{}, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFSOCK || stat.Mode&0o7777 != 0o600 || int(stat.Uid) != c.owner.Lease.UID || int(stat.Gid) != c.owner.Lease.GID {
		return nativeLoopIdentity{}, errors.New("native restore channels: original private endpoint permissions changed")
	}
	if endpoint := c.endpoints[port]; endpoint.file != nil {
		var pinned unix.Stat_t
		if err := unix.Fstat(int(endpoint.file.Fd()), &pinned); err != nil {
			return nativeLoopIdentity{}, err
		}
		if pinned.Dev != stat.Dev || pinned.Ino != stat.Ino || pinned.Nlink != 1 {
			return nativeLoopIdentity{}, errors.New("native restore channels: pinned endpoint inode changed")
		}
	}
	return nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, nil
}

func (c *nativeQualificationRestoreChannels) prepareEndpoint(port uint32) error {
	if err := errors.Join(c.requireOwner(c.ctx), c.requireRoot()); err != nil {
		return err
	}
	c.v.mu.Lock()
	ordinary := c.v.guestVsockListeners[guestVsockListenerKey{instance: c.owner.Lease.Instance, port: port}]
	c.v.mu.Unlock()
	if ordinary != nil {
		return errors.New("native restore channels: ordinary receiver already owns target endpoint")
	}
	path := c.v.guestVsockUDSSock(c.owner.Lease.Instance, port)
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(err, errors.New("native restore channels: existing endpoint requires original retirement"))
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return err
	}
	listener.SetUnlinkOnClose(false)
	c.endpoints[port] = nativeQualificationRestoreEndpoint{listener: listener}
	fd, err := unix.Open(path, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), "native-restore-channel-endpoint")
	c.endpoints[port] = nativeQualificationRestoreEndpoint{listener: listener, file: file}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFSOCK || stat.Nlink != 1 || int(stat.Uid) != os.Geteuid() || int(stat.Gid) != os.Getegid() {
		return errors.New("native restore channels: newly bound host endpoint was replaced")
	}
	// Metadata effects target the pinned socket inode, never a replaceable
	// pathname. Hosts without Fchmodat2/AT_EMPTY_PATH fail closed.
	if err := unix.Fchmodat(fd, "", 0o600, unix.AT_EMPTY_PATH); err != nil {
		return err
	}
	if err := unix.Fchownat(fd, "", c.owner.Lease.UID, c.owner.Lease.GID, unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	identity, err := c.endpointIdentity(port)
	if err != nil {
		return err
	}
	c.endpoints[port] = nativeQualificationRestoreEndpoint{listener: listener, file: file, identity: identity}
	return errors.Join(c.requireRoot(), c.requireOwner(c.ctx))
}

func (c *nativeQualificationRestoreChannels) require(ctx context.Context, port uint32) error {
	if err := errors.Join(c.requireOwner(ctx), c.requireRoot()); err != nil {
		return err
	}
	c.v.mu.Lock()
	current := c.v.nativeRestoreChannels[c.owner.Lease.Instance]
	c.v.mu.Unlock()
	if current != c || !c.permit.channelsUsed.Load() {
		return errors.New("native restore channels: original listener cohort is unavailable")
	}
	endpoint, ok := c.endpoints[port]
	identity, err := c.endpointIdentity(port)
	if !ok || err != nil || identity != endpoint.identity {
		return errors.Join(err, errors.New("native restore channels: original endpoint changed"))
	}
	return ctx.Err()
}
