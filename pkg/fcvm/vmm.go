package fcvm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
)

// JailerVMM is the production VMM. It provisions a jail chroot, launches
// firecracker under the jailer, and drives park/wake via the Firecracker API over
// the jail's unix socket. It is validated on metal (`make test-metal`); the
// orchestration around it (Manager) is proven cross-platform with fakes.
//
// Chroot model (Appendix B): jailer builds the chroot at
// <base>/firecracker/<id>/root and execs firecracker inside it, so every path the
// VM references must live within that root. We hardlink the shared read-only
// kernel/base rootfs in (cheap) and link the per-app layer / snapshot files, then
// reference them by their in-chroot basenames.
type JailerVMM struct {
	chrootBase     string        // /srv/fc/jail
	fcName         string        // chroot dir name jailer derives from the exec-file basename
	readyTimeout   time.Duration // WAKING/cold-boot readiness budget (spec §6)
	destroyWait    time.Duration // cap for DestroyWithExport's wait-for-exit; 0 => 10m
	exportMaxBytes int64         // cap for build-artifact copy-out; 0 => api.MaxExportedLayerBytes
	// storage is the artifact backend where snapshot blobs live per
	// #96 / ADR-025 axis 2. Restore resolves StorageKey → local tmp;
	// Snapshot Streams the produced mem blob back through Storage.Put.
	// Optional — when nil, Restore/Snapshot fall back to the legacy
	// MemPath/VMStatePath branch unchanged.
	storage storage.StorageBackend

	mu      sync.Mutex
	proc    map[string]*exec.Cmd // instance -> running jailer process
	clients map[string]*http.Client
	recs    map[string]*instanceRecord // instance -> per-process bookkeeping (M6 builder VMs)
	// materialisedTmp tracks tmp files materializeFromStorage created for
	// each instance so Kill/DestroyWithExport can Remove them on teardown.
	// Without this, the tmp files (in /tmp) outlive the chroot and leak
	// across thousands of wakes on a busy box.
	materialisedTmp map[string][]string
}

// instanceRecord tracks one firecracker child + build-specific options so
// DestroyWithExport can wait for exit, capture the code, and copy artifacts.
// The exited/exitCode fields are written exactly once by the watchdog goroutine
// started in startJailer; reads in DestroyWithExport block until the watchdog
// signals done via the cond.
type instanceRecord struct {
	cmd      *exec.Cmd
	exited   bool          // set by the watchdog when cmd.Wait completes
	exitCode int           // captured from cmd.Wait's ProcessState.ExitCode()
	done     chan struct{} // closed by the watchdog; readers <-done to wake
}

// NewJailerVMM constructs a JailerVMM. readyTimeout of 0 defaults to 30s (the
// COLD_BOOTING ceiling, spec §6.1).
func NewJailerVMM(chrootBase string, readyTimeout time.Duration) *JailerVMM {
	if readyTimeout <= 0 {
		readyTimeout = 30 * time.Second
	}
	return &JailerVMM{
		chrootBase:      chrootBase,
		fcName:          resolveFCChrootName(),
		readyTimeout:    readyTimeout,
		destroyWait:     10 * time.Minute, // builder timeout (spec §1 BuildTimeoutSeconds) + headroom
		exportMaxBytes:  0,                // resolved to api.MaxExportedLayerBytes at first export
		proc:            make(map[string]*exec.Cmd),
		clients:         make(map[string]*http.Client),
		recs:            make(map[string]*instanceRecord),
		materialisedTmp: make(map[string][]string),
	}
}

// WithStorage wires the artifact backend the VMM uses for snapshot blob
// (de)serialization. Issue #96 / ADR-025 axis 2 — when Restore carries a
// StorageKey, the VMM streams the bytes through Storage.Get into a tmp
// file and uses the absolute path for the chroot staging step. On
// Snapshot, the produced mem blob is Put under the configured StorageKey
// after the move-out. Calling WithStorage(nil) clears the override and
// restores the legacy MemPath-only contract (one-release deprecation
// window).
func (v *JailerVMM) WithStorage(s storage.StorageBackend) *JailerVMM {
	v.storage = s
	return v
}

// resolveFCChrootName returns the directory name jailer will use for the chroot:
// jailer resolves the --exec-file symlink and uses the REAL binary's basename, so
// a `firecracker -> firecracker-v1.7.0` symlink (both the ansible role and the
// Lima loop ship one) makes jailer build .../firecracker-v1.7.0/<id>/root. The
// Manager must place the config/drives in that same dir, so it tracks the same
// resolved basename here. Falls back to the plain name off the metal path.
func resolveFCChrootName() string {
	p, err := exec.LookPath(FirecrackerBin)
	if err != nil {
		return FirecrackerBin
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Base(real)
	}
	return filepath.Base(p)
}

// DetectFirecrackerVersion runs `firecracker --version` and returns the version
// string (e.g. "1.7.0"). Snapshots are pinned to this value (ADR-005); on a
// change every snapshot goes stale and apps re-snapshot via cold boot.
func DetectFirecrackerVersion(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, FirecrackerBin, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("vmm: firecracker --version: %w", err)
	}
	// First line looks like "Firecracker v1.7.0".
	line := out
	if i := bytes.IndexByte(out, '\n'); i >= 0 {
		line = out[:i]
	}
	fields := bytes.Fields(line)
	if len(fields) == 0 {
		return "", fmt.Errorf("vmm: unexpected version output %q", out)
	}
	return string(bytes.TrimPrefix(fields[len(fields)-1], []byte("v"))), nil
}

func (v *JailerVMM) chrootRoot(instance string) string {
	return filepath.Join(v.chrootBase, v.fcName, instance, "root")
}

func (v *JailerVMM) socketPath(instance string) string {
	return filepath.Join(v.chrootRoot(instance), APISockName)
}

// Boot provisions the chroot, starts the jailed firecracker with a full config,
// and blocks until the guest is ready. On error it kills whatever it started.
func (v *JailerVMM) Boot(ctx context.Context, l Lease, cfg VMConfig) (err error) {
	root, err := v.mkChroot(l.Instance)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = v.Kill(context.WithoutCancel(ctx), l)
		}
	}()

	jailed, err := v.provision(root, cfg, l.UID, l.GID)
	if err != nil {
		return fmt.Errorf("vmm: provision chroot: %w", err)
	}
	cfgBytes, err := json.Marshal(jailed)
	if err != nil {
		return fmt.Errorf("vmm: marshal config: %w", err)
	}
	cfgPath := filepath.Join(root, VMConfigName)
	if err = os.WriteFile(cfgPath, cfgBytes, 0o640); err != nil {
		return fmt.Errorf("vmm: write config: %w", err)
	}
	// The jailed firecracker reads --config-file as the unprivileged uid; hand it
	// ownership so a 0640 root-owned file isn't unreadable inside the jail.
	if err = chownJail(cfgPath, l.UID, l.GID); err != nil {
		return fmt.Errorf("vmm: chown config: %w", err)
	}
	if err = v.ownChrootRoot(root, l); err != nil {
		return err
	}
	if err = v.startJailer(ctx, l, "--config-file", VMConfigName); err != nil {
		return err
	}
	// Vsock is configured via the config-file (top-level `vsock:` field,
	// see VMConfig). Firecracker attaches it pre-start; the UDS at
	// vsockUDSSock is created by the time startJailer returns. No
	// post-start PUT needed.
	if err = v.waitReady(ctx, l); err != nil {
		return fmt.Errorf("vmm: readiness: %w", err)
	}
	return nil
}

// Restore starts a bare jailed firecracker and loads a snapshot into it, resuming
// the guest (spec §4.4, mem_backend File). The netns/tap already exist (the
// Manager set them up); the restored net device references tap0 by name.
func (v *JailerVMM) Restore(ctx context.Context, l Lease, spec RestoreSpec) (err error) {
	root, err := v.mkChroot(l.Instance)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = v.Kill(context.WithoutCancel(ctx), l)
		}
	}()

	// #96 / ADR-025 axis 2 — materialise the mem blob from the configured
	// StorageBackend into a vmmd-allocated tmp file. After slice 3 the
	// staging tmp path is purely internal: no caller-supplied MemPath is
	// honoured. The local driver on the production box maps "snap/" to
	// /srv/fc/snap and the resolution is essentially a stat; the OCI
	// driver streams the bytes over HTTP. Tmp cleanup happens via the
	// deferred Kill (chroot lives on tmpfs and disappears with it).
	memSrc := spec.VMStatePath
	if spec.StorageKey != "" && v.storage != nil {
		memTmp, gerr := v.materializeFromStorage(ctx, l.Instance, spec.StorageKey)
		if gerr != nil {
			return gerr
		}
		if memTmp != "" {
			memSrc = memTmp
		}
	}
	if memSrc == "" {
		return fmt.Errorf("vmm: restore spec missing mem source (storage_key=%q)", spec.StorageKey)
	}

	// #121 / ADR-025 axis 2 slice 4 — materialise the vmstate blob
	// independently of mem. The branch selector is the new VMStateStorageKey
	// field's emptiness, NOT v.storage != nil: production default-local
	// wires a non-nil LocalStorageBackend (cmd/vmmd/main.go) so a
	// storage-nil gate would route vmstate through the local backend
	// even on default-local and break the host-path branch the engine
	// relies on for single-box wakes (the documented "every wake pays
	// the dial cost" model only ships vmstate via storage on remote
	// nodes). When the key is non-empty the configured backend is the
	// authoritative carrier and spec.VMStatePath is logged-only metadata.
	// When the key is empty we fall back to spec.VMStatePath byte-for-bit
	// (the existing single-box behaviour).
	stateSrc := spec.VMStatePath
	if spec.VMStateStorageKey != "" && v.storage != nil {
		stateTmp, gerr := v.materializeFromStorage(ctx, l.Instance, spec.VMStateStorageKey)
		if gerr != nil {
			return gerr
		}
		if stateTmp != "" {
			stateSrc = stateTmp
		}
	}
	if stateSrc == "" {
		return fmt.Errorf("vmm: restore spec missing vmstate source (vmstate_storage_key=%q vmstate_path=%q)",
			spec.VMStateStorageKey, spec.VMStatePath)
	}

	// Re-stage everything the snapshot's recorded VM state still references.
	// Park→Kill (vmm.Kill) wiped the prior chroot, so the chroot-relative
	// basenames in the snapshot (kernel + drive backings) must be restored
	// before /snapshot/load, otherwise Firecracker 400s when it tries to
	// open the backing file. Drive 0 (base) is shared RO — hardlink; drive 1
	// (per-app layer, RW overlay upper) is per-instance — copy + chown.
	if spec.KernelPath == "" || spec.BasePath == "" || spec.LayerPath == "" {
		return fmt.Errorf("vmm: restore spec missing kernel/base/layer: %+v", spec)
	}
	if _, err := stageReadOnly(root, spec.KernelPath); err != nil {
		return fmt.Errorf("vmm: stage kernel: %w", err)
	}
	if _, err := stageReadOnly(root, spec.BasePath); err != nil {
		return fmt.Errorf("vmm: stage base: %w", err)
	}
	if _, err := stageWritable(root, spec.LayerPath, l.UID, l.GID); err != nil {
		return fmt.Errorf("vmm: stage layer: %w", err)
	}

	// Snapshot files are read-only inputs shared across the N instances a single
	// snapshot may restore (invariant §6.2-5): hardlink them in and widen for read
	// rather than chown, which would rewrite the shared inode owner.
	memName, err := stageReadOnly(root, memSrc)
	if err != nil {
		return fmt.Errorf("vmm: stage mem file: %w", err)
	}
	stateName, err := stageReadOnly(root, stateSrc)
	if err != nil {
		return fmt.Errorf("vmm: stage vmstate: %w", err)
	}
	// firecracker (as the jailer uid) writes the API socket and, later, snapshot
	// output into the chroot root — it must own that directory.
	if err = v.ownChrootRoot(root, l); err != nil {
		return err
	}

	// Start firecracker with only the API socket, then load + resume.
	if err = v.startJailer(ctx, l); err != nil {
		return err
	}
	body := map[string]any{
		"snapshot_path": stateName,
		"mem_backend":   map[string]any{"backend_type": "File", "backend_path": memName},
		"resume_vm":     true,
	}
	if err = v.apiPut(ctx, l.Instance, "/snapshot/load", body); err != nil {
		return fmt.Errorf("vmm: load snapshot: %w", err)
	}
	// Vsock is in the config-file (set at config-write time before
	// startJailer), so the UDS is live by the time /snapshot/load
	// completes. Trigger the resume hook now to re-seed entropy and step
	// the clock before the app can bind :8080 (spec §11 V6).
	if err = v.TriggerResumeHook(ctx, l, time.Now().UnixNano()); err != nil {
		return fmt.Errorf("vmm: resume hook: %w", err)
	}
	if err = v.waitReady(ctx, l); err != nil {
		return fmt.Errorf("vmm: readiness after restore: %w", err)
	}
	return nil
}

// vsockUDSSock is the host-side path the TriggerResumeHook dialer reaches.
// It's the chroot-local UDS the jailer creates; vmmd dials it from the
// chroot root because the firecracker process is unprivileged and only its
// jailer uid can read the socket file.
func (v *JailerVMM) vsockUDSSock(instance string) string {
	return filepath.Join(v.chrootRoot(instance), VsockUDSSocketName)
}

// resumeHookDialDeadline bounds the TriggerResumeHook wait. The jailer
// creates the vsock UDS a few ms after firecracker accepts the /vsock PUT; on
// a slow nested-KVM guest this can take ~50 ms. Five seconds is well above
// the realistic ceiling and well below the spec §6.1 readyTimeout (30 s).
const resumeHookDialDeadline = 5 * time.Second

// resumeHookDialStep is the per-attempt backoff between dial retries.
const resumeHookDialStep = 20 * time.Millisecond

// resumeHookMsgResume is the wire-format discriminator for a resume request.
// Wire: 4-byte BE msg type + 4-byte BE body length + JSON body (ADR-022).
// The length prefix lets the guest read exactly N bytes instead of waiting
// for EOF — some AF_VSOCK proxies don't propagate CloseWrite promptly, so
// depending on it produced EOF-mid-ack in the V6 metal test.
const resumeHookMsgResume uint32 = 1

// resumeHookGuestPort is the AF_VSOCK port the guest-init resume
// listener binds. Must match guest/init/listen_resume_linux.go's
// VsockResumePort.
const resumeHookGuestPort = 1024

// resumeHookEntropyBytes is the count of host-supplied CSPRNG bytes sent
// in every resume payload. The guest injects them into /dev/urandom via
// ioctl(RNDADDENTROPY) BEFORE reading /proc/sys/kernel/random/uuid, so
// each restore's draw is unique even when virtio-rng state is identical
// (it is snapshotted, so /dev/hwrng returns the same bytes per restore).
// 256 bits of entropy is enough to fully re-key the pool for UUID
// generation. ADR-022 §"Why the host ships entropy".
const resumeHookEntropyBytes = 256

// resumeHookMaxBodyBytes is the upper bound on the JSON-marshaled body of
// the resume-hook payload. The body is constructed from exactly
// resumeHookEntropyBytes of CSPRNG output (base64 → 4/3 expansion) plus
// the JSON envelope; 8 KiB is comfortably above the current ~400 B
// observed size and well under int32/2, so a future bump to
// resumeHookEntropyBytes can never push 8+len(body) into overflow
// territory. The guest's VsockResumeMaxEntropyBytes is the matching cap
// on the receiving side. CodeQL go/allocation-size-overflow guards.
const resumeHookMaxBodyBytes = 8 * 1024

// readConnectAck consumes the "OK <hostside_port>\n" reply from
// Firecracker. Returns the first whitespace-delimited token. Reads
// until newline so the byte count doesn't matter (FC's host-assigned
// port is a 32-bit integer — variable digit count).
func readConnectAck(conn net.Conn) (string, error) {
	const max = 64
	buf := make([]byte, 0, max)
	one := make([]byte, 1)
	for len(buf) < max {
		if _, err := conn.Read(one); err != nil {
			return "", fmt.Errorf("read CONNECT reply: %w", err)
		}
		if one[0] == '\n' || one[0] == '\r' {
			break
		}
		buf = append(buf, one[0])
	}
	if len(buf) == 0 {
		return "", fmt.Errorf("empty CONNECT reply")
	}
	// Return the first whitespace-delimited token.
	for i := 0; i < len(buf); i++ {
		if buf[i] == ' ' {
			return string(buf[:i]), nil
		}
	}
	return string(buf), nil
}

// TriggerResumeHook dials the guest's vsock UDS and asks it to run its
// post-restore side effects (re-seed entropy + step clock). Must be called
// from Restore after /snapshot/load and before waitReady. Spec §11 V6 is the
// acceptance gate: two instances from one snapshot must produce distinct
// /proc/sys/kernel/random/uuid immediately post-resume.
//
// Wire format (Firecracker vsock host-initiated, FC docs/vsock.md):
//
//  1. Host connects to <chroot>/vsock.sock.
//  2. Host writes ASCII "CONNECT <port>\n" (e.g. "CONNECT 1024\n").
//  3. Firecracker replies with "OK <assigned_hostside_port>\n".
//  4. Bidirectional byte stream — host writes the resume-hook payload,
//     guest writes back a 1-byte ack.
//
// Payload format (ADR-022): 4-byte big-endian msg type (= 1 =
// MSG_RESUME) + JSON body {"hostTimeUnixNano": N, "entropy": "<base64>"}
// where entropy is 256 bytes of fresh CSPRNG output from the host.
// The guest's listenResumeHook (guest/init/listen_resume_linux.go) reads
// the same shape and writes back ack=0 (ok) or ack=1 (nack).
//
// We fail closed: any error (dial timeout, CONNECT failure, payload
// write failure, nack) returns wrapped. A restored VM with snapshot-
// shared entropy is exactly the failure mode V6 rejects, so we refuse
// to declare it ready.
func (v *JailerVMM) TriggerResumeHook(ctx context.Context, l Lease, hostTimeUnixNano int64) error {
	// Defense-in-depth: refuse to dial with a half-built VMM or empty instance.
	// Without this guard, a refactor that passes an uninitialised JailerVMM
	// (test seam, future caller) would dial a malformed UDS path and return a
	// cryptic ENOENT — fails closed, but the operator gets no clue. With the
	// guard, the failure mode is a clear "this VM was never set up right".
	if v == nil {
		return fmt.Errorf("vmm: TriggerResumeHook: nil receiver")
	}
	if l.Instance == "" {
		return fmt.Errorf("vmm: TriggerResumeHook: empty instance")
	}
	if v.chrootBase == "" {
		return fmt.Errorf("vmm: TriggerResumeHook: chrootBase not configured")
	}
	sock := v.vsockUDSSock(l.Instance)
	deadline := time.Now().Add(resumeHookDialDeadline)
	var conn net.Conn
	var lastErr error
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var err error
		conn, err = net.DialTimeout("unix", sock, 200*time.Millisecond)
		if err == nil {
			break
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(resumeHookDialStep):
		}
	}
	if conn == nil {
		return fmt.Errorf("vmm: dial vsock uds %s: %w", sock, lastErr)
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(resumeHookDialDeadline))

	// Step 1: FC CONNECT-port handshake. "CONNECT <port>\n" — ASCII,
	// newline-terminated. Guest listens on port VsockResumePort (1024).
	connectCmd := fmt.Sprintf("CONNECT %d\n", resumeHookGuestPort)
	if _, err := conn.Write([]byte(connectCmd)); err != nil {
		return fmt.Errorf("vmm: write CONNECT %d: %w", resumeHookGuestPort, err)
	}

	// Step 2: read "OK <hostside_port>\n". FC prefixes the host-assigned
	// ephemeral port with "OK ". We don't care about the value (it's
	// for connection-multiplexing bookkeeping on the FC side), only
	// that the response starts with "OK ".
	connectAck, err := readConnectAck(conn)
	if err != nil {
		return fmt.Errorf("vmm: read CONNECT ack: %w", err)
	}
	if connectAck != "OK" {
		return fmt.Errorf("vmm: CONNECT rejected: %q", connectAck)
	}

	// Step 3: write the resume-hook payload. 4-byte BE msg type + 4-byte BE
	// body length + JSON body. The length prefix lets the guest read exactly
	// N bytes; we deliberately do NOT close our write half — the connection
	// stays open for the ack roundtrip (some AF_VSOCK proxies don't propagate
	// CloseWrite promptly, so depending on it produced EOF-mid-ack in the
	// V6 metal test).
	//
	// The body carries fresh entropy bytes from the host's CSPRNG. The guest
	// injects them into /dev/urandom via ioctl(RNDADDENTROPY) BEFORE reading
	// /proc/sys/kernel/random/uuid. Without this, both restores from one
	// snapshot read the SAME 256 bytes from /dev/hwrng (virtio-rng state is
	// captured in the snapshot), inject the same input into the pool, and
	// draw the same UUID — spec §11 V6 fails on every concurrent restore.
	// See ADR-022 §"Why the host ships entropy".
	entropy := make([]byte, resumeHookEntropyBytes)
	if _, err := rand.Read(entropy); err != nil {
		return fmt.Errorf("vmm: read host entropy: %w", err)
	}
	body, err := json.Marshal(struct {
		HostTimeUnixNano int64  `json:"hostTimeUnixNano"`
		Entropy          string `json:"entropy"` // base64; guest decodes + ioctl(RNDADDENTROPY)
	}{HostTimeUnixNano: hostTimeUnixNano, Entropy: base64.StdEncoding.EncodeToString(entropy)})
	if err != nil {
		return fmt.Errorf("vmm: marshal resume body: %w", err)
	}
	// Bound the marshal output defensively. The body is constructed from
	// exactly resumeHookEntropyBytes bytes of CSPRNG + a JSON envelope, so
	// in practice it stays under ~400 B — but a future bump of the entropy
	// constant or a hostile build tag could push len(body) into overflow
	// territory, and `make([]byte, 8+len(body))` would panic with
	// "makeslice: len out of range". CodeQL go/allocation-size-overflow
	// flags this; the cap is the actual defense.
	if len(body) > resumeHookMaxBodyBytes {
		return fmt.Errorf("vmm: resume body %d bytes exceeds %d cap", len(body), resumeHookMaxBodyBytes)
	}
	msg := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(msg[:4], resumeHookMsgResume)
	binary.BigEndian.PutUint32(msg[4:8], uint32(len(body)))
	copy(msg[8:], body)
	if _, err := conn.Write(msg); err != nil {
		return fmt.Errorf("vmm: write resume request: %w", err)
	}

	// Step 4: read the 1-byte ack from the guest.
	ack := make([]byte, 1)
	if _, err := io.ReadFull(conn, ack); err != nil {
		return fmt.Errorf("vmm: read resume ack: %w", err)
	}
	if ack[0] != 0 {
		return fmt.Errorf("vmm: resume hook failed (ack=%d)", ack[0])
	}
	return nil
}

// Snapshot pauses the running VM, writes a full snapshot, copies the files out to
// spec's durable paths, and destroys the VM (spec §4.4).
//
// #96 / ADR-025 axis 2 — when spec.StorageKey is set, the produced mem
// blob is Put under it via the configured StorageBackend AFTER the
// successful moveOut. The local driver maps "snap/<depID>/mem" to the
// canonical /srv/fc/snap path so the Put is effectively a no-op bytewise
// move; a future remote driver would actually stream the bytes. On Put
// failure we leave MemPath populated so the legacy code path can still
// recover the snapshot on the next read — the storage publish is best-
// effort with a Warn log rather than a hard error.
func (v *JailerVMM) Snapshot(ctx context.Context, l Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	root := v.chrootRoot(l.Instance)
	if err := v.apiPatch(ctx, l.Instance, "/vm", map[string]any{"state": "Paused"}); err != nil {
		return SnapshotInfo{}, fmt.Errorf("vmm: pause: %w", err)
	}
	const memName, stateName = "mem", "vmstate"
	create := map[string]any{
		"snapshot_type": "Full",
		"snapshot_path": stateName,
		"mem_file_path": memName,
	}
	if err := v.apiPut(ctx, l.Instance, "/snapshot/create", create); err != nil {
		return SnapshotInfo{}, fmt.Errorf("vmm: create snapshot: %w", err)
	}

	// #96 / ADR-025 axis 2 — after slice 3 the mem destination is
	// vmmd-allocated. Firecracker dumps the paused mem at <chroot>/mem;
	// moveOut copies it into a tmp and we stream that into the
	// configured StorageBackend at the StorageKey.
	//
	// #121 / ADR-025 axis 2 slice 4 — vmstate parallels mem. When
	// VMStateStorageKey is non-empty the configured StorageBackend is
	// authoritative and we stream the vmstate bytes straight from the
	// chroot-resident file (no moveOut, no host-path allocation).
	// When the key is empty we keep the legacy moveOut(spec.VMStatePath)
	// behaviour byte-for-bit so single-box / default-local is
	// unaffected. As with mem, Put failure is Warn-level (best-effort
	// durability in this slice; later slices may tighten) so a
	// transient backend hiccup doesn't fail a successful pause.
	memTmp, err := os.CreateTemp("", "faas-snap-*.mem")
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("vmm: alloc snapshot mem tmp: %w", err)
	}
	memTmpPath := memTmp.Name()
	_ = memTmp.Close()
	defer func() { _ = os.Remove(memTmpPath) }()

	memBytes, err := moveOut(filepath.Join(root, memName), memTmpPath)
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("vmm: export mem: %w", err)
	}
	// Vmstate publication branches on VMStateStorageKey exactly like mem
	// does on StorageKey. Same predicate shape (key + non-nil storage);
	// the value selector is the new field's emptiness, not storage nil,
	// so default-local still wires a LocalStorageBackend and routes
	// vmstate through the legacy path because the key is empty.
	var stateBytes int64
	vmstateSrcInChroot := filepath.Join(root, stateName)
	if spec.VMStateStorageKey != "" && v.storage != nil {
		// nolint:forbidigo // vmstateSrcInChroot is the chroot-resident
		// tmp Firecracker just wrote; not a customer-supplied location,
		// so the openCustomerFile guard does not apply.
		f, oerr := os.Open(vmstateSrcInChroot)
		if oerr != nil {
			slog.Default().Warn("vmm: snapshot vmstate storage open failed",
				"key", spec.VMStateStorageKey, "err", oerr)
		} else {
			if perr := v.storage.Put(ctx, spec.VMStateStorageKey, f); perr != nil {
				slog.Default().Warn("vmm: snapshot vmstate storage put failed",
					"key", spec.VMStateStorageKey, "err", perr)
			}
			_ = f.Close()
		}
		// stateBytes still needs the chroot file size for telemetry;
		// the bytes were published via storage rather than copied to a
		// host path, so we stat the chroot-resident original.
		if fi, serr := os.Stat(vmstateSrcInChroot); serr == nil {
			stateBytes = fi.Size()
		}
	} else {
		// Legacy host-path branch (default-local / single-box):
		// moveOut from chroot to the caller-supplied VMStatePath.
		stateBytes, err = moveOut(vmstateSrcInChroot, spec.VMStatePath)
		if err != nil {
			return SnapshotInfo{}, fmt.Errorf("vmm: export vmstate: %w", err)
		}
	}

	if spec.StorageKey != "" && v.storage != nil {
		// nolint:forbidigo // memTmpPath is a vmmd-allocated tmp under
		// os.TempDir(); not a customer-supplied location, so the
		// openCustomerFile guard does not apply.
		f, oerr := os.Open(memTmpPath)
		if oerr != nil {
			slog.Default().Warn("vmm: snapshot storage open failed",
				"key", spec.StorageKey, "err", oerr)
		} else {
			if perr := v.storage.Put(ctx, spec.StorageKey, f); perr != nil {
				slog.Default().Warn("vmm: snapshot storage put failed",
					"key", spec.StorageKey, "err", perr)
			}
			_ = f.Close()
		}
	}

	// Snapshot semantics: the VM is destroyed after a successful snapshot.
	_ = v.Kill(ctx, l)
	return SnapshotInfo{MemBytes: memBytes, VMStateBytes: stateBytes}, nil
}

// Kill stops the jailer process (if any) and removes the chroot. Idempotent.
// SIGKILL'd instances don't get an artifact export — that's Builderd's path
// (use DestroyWithExport).
func (v *JailerVMM) Kill(_ context.Context, l Lease) error {
	v.mu.Lock()
	cmd, hasCmd := v.proc[l.Instance]
	rec, hasRec := v.recs[l.Instance]
	if hasCmd {
		delete(v.proc, l.Instance)
	}
	v.mu.Unlock()

	if hasCmd && cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if hasRec && rec.done != nil {
		// Wait for the watchdog to finish (it always does, since cmd.Process.Wait
		// is observed by Go's runtime even on signal-induced exit). Bound by the
		// same destroyWait so a wedged firecracker can't pin us.
		select {
		case <-rec.done:
		case <-time.After(v.destroyWait):
		}
		v.mu.Lock()
		delete(v.recs, l.Instance)
		v.mu.Unlock()
	}
	v.closeClient(l.Instance)
	// Chroot lives in tmpfs (spec §Gotchas); removing it frees the RAM it holds.
	if err := os.RemoveAll(filepath.Join(v.chrootBase, v.fcName, l.Instance)); err != nil {
		return fmt.Errorf("vmm: remove chroot: %w", err)
	}
	// Tmp files materialized from a StorageKey live in /tmp (not the chroot
	// root) — sweep them explicitly so they don't leak across thousands of
	// wakes.
	v.sweepMaterialised(l.Instance)
	// Remove the per-VM cgroup scope jailer created (--cgroup cpu.weight=…).
	// Required by spec §6.2-4 ("parked = zero RAM") — a populated cgroup dir
	// holds page-cache references. The scope name equals jailer --id
	// (= Lease.Instance); see pkg/fcvm/cgroup.go for the matching write path.
	// Idempotent; missing dir is fine.
	//
	// EBUSY (or any other non-IsNotExist error) is logged and swallowed: the
	// jailer process is already gone at this point, so we cannot rewind the
	// teardown. A leftover cgroup dir leaks RAM only until the next cgroup
	// pressure event reaps it; failing the whole call would mask the real
	// teardown success.
	scopePath := filepath.Join(cgroupRoot, ParentCgroup, PerInstanceScope(l.Instance))
	if err := os.RemoveAll(scopePath); err != nil && !os.IsNotExist(err) {
		slog.Default().Warn("cgroup scope remove failed; continuing teardown",
			"path", scopePath, "instance", l.Instance, "err", err)
	}
	return nil
}

// DestroyWithExport is the build-VM teardown path (M6 / spec §4.5). It blocks
// until the firecracker child exits (capped by v.destroyWait — default 10m,
// comfortable above spec §1 BuildTimeoutSeconds), captures the exit code, and
// only if exportDir != "" loopback-mounts the chroot-local drive1 to copy out
// /etc/faas/build-done.json and /build/out/* before removing the chroot.
//
// exportDir="" means "app VM", and the method becomes Kill-equivalent: wait
// for the child, drop the chroot, return (0, nil). Existing app-VM callers
// (Manager.Destroy) keep their contract — only builderd opts in via the
// BuildSpec.ExportDir field.
func (v *JailerVMM) DestroyWithExport(ctx context.Context, l Lease, exportDir string) (int, error) {
	v.mu.Lock()
	rec, ok := v.recs[l.Instance]
	v.mu.Unlock()
	if !ok {
		// Unknown / already-torn-down instance: idempotent, no exit code to report.
		v.closeClient(l.Instance)
		_ = os.RemoveAll(filepath.Join(v.chrootBase, v.fcName, l.Instance))
		return 0, nil
	}

	// 1. Wait for the firecracker child to exit. The watchdog goroutine started
	//    by startJailer is the single point that calls cmd.Process.Wait;
	//    DestroyWithExport just blocks on rec.done and reads rec.exitCode.
	deadline := time.NewTimer(v.destroyWait)
	defer deadline.Stop()
	select {
	case <-rec.done:
	case <-deadline.C:
		// Force-kill and re-wait with a shorter budget. A builder that ignores
		// the spec's BuildTimeoutSeconds is misbehaving; refuse to hold vmmd
		// forever, but don't tear down the chroot before the export either.
		v.mu.Lock()
		if proc := v.proc[l.Instance]; proc != nil && proc.Process != nil {
			_ = proc.Process.Kill()
		}
		v.mu.Unlock()
		select {
		case <-rec.done:
		case <-time.After(5 * time.Second):
			return -1, fmt.Errorf("vmm: %s did not exit within %s", l.Instance, v.destroyWait)
		}
	}

	v.mu.Lock()
	exitCode := rec.exitCode
	v.mu.Unlock()

	// 2. Artifact export (build VMs only). Loopback-mount the chroot-local
	//    drive1.ext4 and copy out /etc/faas/build-done.json + /build/out/*.
	//    The mount uses root privileges (vmmd is the only root component, §11).
	if exportDir != "" {
		if err := v.exportBuildArtifacts(l.Instance, exportDir); err != nil {
			// Don't fail Destroy — the build is dead either way; log + return
			// the exit code so the caller can still classify.
			return exitCode, fmt.Errorf("vmm: export build artifacts: %w", err)
		}
	}

	// 3. Tear down the chroot + per-instance state.
	v.mu.Lock()
	delete(v.recs, l.Instance)
	delete(v.proc, l.Instance)
	v.mu.Unlock()
	v.closeClient(l.Instance)
	if err := os.RemoveAll(filepath.Join(v.chrootBase, v.fcName, l.Instance)); err != nil {
		return exitCode, fmt.Errorf("vmm: remove chroot: %w", err)
	}
	v.sweepMaterialised(l.Instance)
	return exitCode, nil
}

// exportBuildArtifacts loopback-mounts the chroot-local drive1 image and
// copies /etc/faas/build-done.json and /build/out/* into exportDir. Files
// larger than exportMaxBytes are skipped + counted as failures (best-effort
// — never blocks the caller). vmmd is the only root component, so the mount
// is fine; the chroot-local drive1.ext4 is owned by root after provision
// (pkg/fcvm/vmm.go:stageWritable).
func (v *JailerVMM) exportBuildArtifacts(instance, exportDir string) error {
	if err := os.MkdirAll(exportDir, 0o755); err != nil {
		return fmt.Errorf("mkdir export: %w", err)
	}
	drive1 := filepath.Join(v.chrootBase, v.fcName, instance, layerImageName)
	if _, err := os.Stat(drive1); err != nil {
		return fmt.Errorf("stat drive1: %w", err)
	}
	mp, err := os.MkdirTemp("", "faas-vmm-export-")
	if err != nil {
		return fmt.Errorf("mkdir mountpoint: %w", err)
	}
	defer func() { _ = os.RemoveAll(mp) }()

	// mount -o loop,ro — read-only is enough; the VM is dead by this point.
	if out, err := exec.Command("mount", "-o", "loop,ro", drive1, mp).CombinedOutput(); err != nil {
		return fmt.Errorf("mount loop: %w (%s)", err, bytes.TrimSpace(out))
	}
	defer func() { _ = exec.Command("umount", mp).Run() }()

	// build-done.json is the canonical manifest builderd reads.
	srcDone := filepath.Join(mp, "etc", "faas", "build-done.json")
	if data, err := os.ReadFile(srcDone); err == nil {
		if err := os.WriteFile(filepath.Join(exportDir, "build-done.json"), data, 0o644); err != nil {
			return fmt.Errorf("write build-done.json: %w", err)
		}
	} // else: VM died before guest-init wrote it — caller falls back to exit-code class.

	// /build/out/ holds the produced OCI tarball. Walk + copy with the size
	// cap enforced. A build that overruns the cap is logged as infra failure
	// via the caller's classification (no error returned — best-effort).
	srcOut := filepath.Join(mp, "build", "out")
	if _, err := os.Stat(srcOut); err == nil {
		dstOut := filepath.Join(exportDir, "build", "out")
		if err := os.MkdirAll(dstOut, 0o755); err != nil {
			return fmt.Errorf("mkdir out: %w", err)
		}
		return copyTree(srcOut, dstOut, v.exportMax())
	}
	return nil
}

// layerImageName is the in-chroot basename vmmd provisions for drive1 (see
// provision / stageWritable — copy preserves basename, so the chroot always
// sees "layer.ext4").
const layerImageName = "layer.ext4"

// secretsEnvPath is the in-guest location guest-init reads after pivot_root
// (spec §11/G2). JSON-encoded envelope shape is documented on secretbox.Open.
// The same file is written once per wake — overwriting any prior content —
// so a secret rotation propagates without re-provisioning the layer.
const secretsEnvPath = "etc/faas/secrets.env"

// StageSecretsEnv loopback-mounts drive1 (the per-app layer, the only fs
// the VM can write at runtime), writes /etc/faas/secrets.env with mode
// 0400, and umounts. The plaintext is read off the chroot-local image
// only for the duration of this call. vmmd is the only root component,
// so the loopback mount is permitted by the §11 threat model.
//
// Mirrors exportBuildArtifacts (read side) — same chroot layout, same
// mountpoint-handling pattern, write-vs-read swap. The function is a no-op
// when jsonBlob is empty: no file is written, no mount attempted. This
// short-circuit is what lets an app with zero secrets proceed without any
// extra mount/umount cost.
func (v *JailerVMM) StageSecretsEnv(instance string, jsonBlob []byte) error {
	if len(jsonBlob) == 0 {
		return nil
	}
	drive1 := filepath.Join(v.chrootBase, v.fcName, instance, layerImageName)
	if _, err := os.Stat(drive1); err != nil {
		return fmt.Errorf("stat drive1: %w", err)
	}
	mp, err := os.MkdirTemp("", "faas-vmm-secrets-")
	if err != nil {
		return fmt.Errorf("mkdir mountpoint: %w", err)
	}
	defer func() { _ = os.RemoveAll(mp) }()

	// rw,noexec,nosuid — drive1 is a vfat-less ext4; noexec would still
	// work but we don't need it and rw alone is the minimum.
	if out, err := exec.Command("mount", "-o", "loop,rw", drive1, mp).CombinedOutput(); err != nil {
		return fmt.Errorf("mount loop: %w (%s)", err, bytes.TrimSpace(out))
	}
	defer func() { _ = exec.Command("umount", mp).Run() }()

	target := filepath.Join(mp, secretsEnvPath)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("mkdir etc/faas: %w", err)
	}
	if err := os.WriteFile(target, jsonBlob, 0o400); err != nil {
		return fmt.Errorf("write secrets.env: %w", err)
	}
	return nil
}

// exportMax resolves the per-export byte cap. Zero means "unset" — fall back
// to api.MaxExportedLayerBytes. We read via a tiny helper so tests can
// inject a tighter cap.
func (v *JailerVMM) exportMax() int64 {
	if v.exportMaxBytes > 0 {
		return v.exportMaxBytes
	}
	return api.MaxExportedLayerBytes
}

// copyTree copies a directory tree from src to dst, skipping any single file
// whose size exceeds maxBytes. Best-effort by design — partial copies are OK
// for a build that overshot the cap.
func copyTree(src, dst string, maxBytes int64) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if maxBytes > 0 && info.Size() > maxBytes {
			return nil // skip the oversize file
		}
		return copyFile(path, target)
	})
}

// --- helpers ---------------------------------------------------------------

func (v *JailerVMM) mkChroot(instance string) (string, error) {
	root := v.chrootRoot(instance)
	// Wipe any leftover state from a prior failed Boot/Restore — jailer's
	// chroot-creation step (mknod /dev/net/tun, mkdir -p /dev/net, etc.)
	// is NOT idempotent and panics with EEXIST on a half-built chroot.
	// RemoveAll on a non-existent path is a no-op, so this is safe for
	// the common case too.
	//
	// Concurrency contract: caller must hold the per-instance Lease (and
	// therefore the unique-while-live invariant on `instance`) for the
	// duration of Boot/Restore. The only race surface is a retry-after-
	// failure that fires before the prior call's defer-cleanup ran; in
	// that window the second RemoveAll nukes the first's freshly-built
	// chroot mid-boot. Boot/Restore's deferred Kill on failure makes
	// this self-correcting on the next retry. If we ever call Boot/
	// Restore from a path that does NOT go through Lease uniqueness,
	// gate this with v.mu (held for the whole Boot/Restore).
	if err := os.RemoveAll(root); err != nil {
		return "", fmt.Errorf("vmm: wipe stale chroot: %w", err)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", fmt.Errorf("vmm: mkdir chroot: %w", err)
	}
	return root, nil
}

// startJailer launches jailer→firecracker for the instance with any extra
// firecracker args appended, and records the process.
func (v *JailerVMM) startJailer(ctx context.Context, l Lease, extraFCArgs ...string) error {
	execFile, err := exec.LookPath(FirecrackerBin)
	if err != nil {
		return fmt.Errorf("vmm: locate firecracker binary: %w", err)
	}
	// Pass the resolved real binary so jailer's chroot basename matches v.fcName
	// (jailer follows the symlink); see resolveFCChrootName.
	if real, rErr := filepath.EvalSymlinks(execFile); rErr == nil {
		execFile = real
	}
	argv := append(JailerCommand(JailerSpec{
		Instance: l.Instance, UID: l.UID, GID: l.GID, Netns: l.Netns, ExecFile: execFile,
	}), extraFCArgs...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("vmm: start jailer: %w", err)
	}
	v.mu.Lock()
	v.proc[l.Instance] = cmd
	rec := &instanceRecord{cmd: cmd, done: make(chan struct{})}
	v.recs[l.Instance] = rec
	v.mu.Unlock()
	// Watchdog: cmd.Wait must be called exactly once per process (stdlib
	// contract). Run it here so DestroyWithExport can later read the captured
	// exit code without racing the actual process termination.
	go func() {
		state, _ := cmd.Process.Wait()
		v.mu.Lock()
		if state != nil {
			rec.exitCode = state.ExitCode()
		}
		rec.exited = true
		close(rec.done)
		v.mu.Unlock()
	}()
	return nil
}

// provision stages the kernel and rootfs images into the chroot for the jailer
// uid and returns a copy of cfg with paths rewritten to the chroot-relative
// basenames. Read-only images (kernel, drive0 base) are hard-linked in and widened
// for read; the writable drive (drive1, the overlay upper) is copied to a private
// per-instance file owned by the uid — see stageReadOnly / stageWritable.
func (v *JailerVMM) provision(root string, cfg VMConfig, uid, gid int) (VMConfig, error) {
	out := cfg
	kname, err := stageReadOnly(root, cfg.BootSource.KernelImagePath)
	if err != nil {
		return out, err
	}
	out.BootSource.KernelImagePath = kname
	out.Drives = make([]Drive, len(cfg.Drives))
	for i, d := range cfg.Drives {
		var name string
		var err error
		if d.IsReadOnly {
			name, err = stageReadOnly(root, d.PathOnHost)
		} else {
			name, err = stageWritable(root, d.PathOnHost, uid, gid)
		}
		if err != nil {
			return out, err
		}
		d.PathOnHost = name
		out.Drives[i] = d
	}
	return out, nil
}

// ownChrootRoot hands the chroot root directory to the jailer uid so the jailed
// firecracker — which chroots into it and then runs unprivileged — can create the
// API socket there and, on Snapshot, write the mem/vmstate files it later exports.
func (v *JailerVMM) ownChrootRoot(root string, l Lease) error {
	if err := chownJail(root, l.UID, l.GID); err != nil {
		return fmt.Errorf("vmm: chown chroot root: %w", err)
	}
	return nil
}

// waitReady polls the guest's routable identity for a :8080 accept.
func (v *JailerVMM) waitReady(ctx context.Context, l Lease) error {
	deadline := time.Now().Add(v.readyTimeout)
	addr := net.JoinHostPort(l.HostIP.String(), "8080")
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("guest %s not ready after %s", l.Instance, v.readyTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fcClient returns an HTTP client bound to the instance's Firecracker API socket.
// Clients are cached per instance because http.Transport's connection pool is
// the expensive part; rebuilding per request would re-resolve the socket every
// time.
func (v *JailerVMM) fcClient(instance string) *http.Client {
	v.mu.Lock()
	defer v.mu.Unlock()
	if c, ok := v.clients[instance]; ok {
		return c
	}
	sock := v.socketPath(instance)
	c := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		},
	}
	v.clients[instance] = c
	return c
}

// closeClient drops any cached http.Client for instance. Called by Kill so a
// subsequent Boot of the same instance name gets a fresh client (and thus a
// fresh transport pool pointed at the new socket).
func (v *JailerVMM) closeClient(instance string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.clients, instance)
}

func (v *JailerVMM) apiPut(ctx context.Context, instance, path string, body any) error {
	return v.apiCall(ctx, http.MethodPut, instance, path, body)
}

func (v *JailerVMM) apiPatch(ctx context.Context, instance, path string, body any) error {
	return v.apiCall(ctx, http.MethodPatch, instance, path, body)
}

func (v *JailerVMM) apiCall(ctx context.Context, method, instance, path string, body any) error {
	return v.apiCallWithClient(ctx, v.fcClient(instance), method, path, body)
}

// apiCallWithClient is the seam that drives a single Firecracker API request.
// Split out from apiCall so tests can inject a client backed by an httptest
// server without needing the unix-socket machinery.
func (v *JailerVMM) apiCallWithClient(ctx context.Context, client *http.Client, method, path string, body any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// startJailer returns as soon as the jailer process is forked — the
	// Firecracker API socket is created by firecracker itself a few ms
	// later. On a slow nested-KVM guest (Lima arm64) the first POST
	// races the socket creation; retry briefly before giving up so the
	// snapshot-restore path isn't held hostage to the boot timing.
	const maxAttempts = 20
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Each attempt needs a fresh body reader because http.Client.Do
		// consumes the body on send.
		req.Body = io.NopCloser(bytes.NewReader(buf))
		req.ContentLength = int64(len(buf))
		resp, err := client.Do(req)
		if err == nil {
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode >= 300 {
				msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				return fmt.Errorf("firecracker %s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(msg))
			}
			return nil
		}
		lastErr = err
		// Short backoff: 5ms × 20 = 100ms total. The socket appears in
		// single-digit ms on bare metal; nested KVM needs ~50ms.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	return lastErr
}

// stageReadOnly hardlinks a shared read-only source (kernel, drive0 base, or a
// snapshot file) into the chroot and widens its mode so the unprivileged jailer
// uid can read it. These files are shared across instances via hardlink (cheap —
// Appendix B), so we must NOT chown them: that would rewrite the shared inode's
// owner and break every other instance holding the same link. They are non-secret,
// read-only, and visible only inside this instance's chroot, so o+r is safe.
func stageReadOnly(root, src string) (string, error) {
	name, err := linkInto(root, src)
	if err != nil {
		return "", err
	}
	if err := ensureOtherReadable(filepath.Join(root, name)); err != nil {
		return "", err
	}
	return name, nil
}

// stageWritable copies a source image into the chroot as a private, per-instance
// file owned by the jailer uid. drive1 (the overlay upper — guest/init) is opened
// read-write by firecracker, and two instances must never share it (invariant
// §6.2-5), so it is copied — never hard-linked — and chowned to the uid. A hardlink
// would alias the shared source inode and corrupt it under concurrent writers.
func stageWritable(root, src string, uid, gid int) (string, error) {
	name := filepath.Base(src)
	dst := filepath.Join(root, name)
	// A read-only sibling drive may already have hard-linked this basename in (the
	// M0 fixture points drive0 and drive1 at the same image); drop that link first
	// so the copy below can't truncate the shared source through it.
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stage writable %s: %w", src, err)
	}
	if err := copyFile(src, dst); err != nil {
		return "", fmt.Errorf("copy writable %s: %w", src, err)
	}
	if err := os.Chmod(dst, 0o600); err != nil {
		return "", fmt.Errorf("chmod writable %s: %w", dst, err)
	}
	if err := chownJail(dst, uid, gid); err != nil {
		return "", err
	}
	return name, nil
}

// ensureOtherReadable widens path's mode to add group+other read if it isn't there
// already. Used for shared read-only chroot files that the unprivileged jailer uid
// (never the owner, never in a matching group) must be able to open.
func ensureOtherReadable(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if perm := fi.Mode().Perm(); perm&0o004 == 0 {
		if err := os.Chmod(path, perm|0o044); err != nil {
			return fmt.Errorf("widen readable %s: %w", path, err)
		}
	}
	return nil
}

// chownJail gives path to the jailer uid/gid. Chowning to an arbitrary uid needs
// CAP_CHOWN, i.e. root; vmmd is the only root component (spec §11) and owns all
// jail staging. Off the box the unit suite runs unprivileged, where chowning to a
// 20000+ uid would EPERM, so we skip when not root: those tests never launch a
// real jailed firecracker, and the metal suite runs as root (test-metal /
// metal-lima are sudo).
func chownJail(path string, uid, gid int) error {
	if os.Geteuid() != 0 {
		return nil
	}
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("chown jail %s -> %d:%d: %w", path, uid, gid, err)
	}
	return nil
}

// linkInto hardlinks src into dir (falling back to copy on cross-device) and
// returns its basename for chroot-relative reference.
func linkInto(dir, src string) (string, error) {
	name := filepath.Base(src)
	dst := filepath.Join(dir, name)
	_ = os.Remove(dst)
	if err := os.Link(src, dst); err != nil {
		if cErr := copyFile(src, dst); cErr != nil {
			return "", fmt.Errorf("link/copy %s: %w", src, cErr)
		}
	}
	return name, nil
}

// moveOut moves src to dst (across filesystems if needed) and returns the size.
func moveOut(src, dst string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return 0, err
	}
	if err := os.Rename(src, dst); err != nil {
		if cErr := copyFile(src, dst); cErr != nil {
			return 0, cErr
		}
		_ = os.Remove(src)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// materializeFromStorage pulls the bytes for key via the configured
// StorageBackend and writes them into a fresh tmp file. Returns the
// absolute path the caller should substitute into MemPath. The tmp
// path is registered against instanceID so Kill / DestroyWithExport
// Remove it during teardown; without the registration the file
// would outlive the chroot (which lives on tmpfs) and leak across
// thousands of wakes on a busy box. When storage is nil or key is
// empty, the helper is a no-op and reports the keys were unused —
// Restore proceeds on MemPath unchanged.
//
// #96 / ADR-025 axis 2 — single seam that lets the local driver satisfy
// the call from /srv/fc/snap and a future remote driver from a registry.
// The chroot staging layer never has to learn about storage.
func (v *JailerVMM) materializeFromStorage(ctx context.Context, instanceID, key string) (string, error) {
	if v.storage == nil || key == "" {
		return "", nil
	}
	rc, err := v.storage.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("vmm: storage get %q: %w", key, err)
	}
	defer func() { _ = rc.Close() }()
	tmp, err := os.CreateTemp("", "faas-snap-*.bin")
	if err != nil {
		return "", fmt.Errorf("vmm: create tmp for %q: %w", key, err)
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, rc); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("vmm: copy %q: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("vmm: close tmp for %q: %w", key, err)
	}
	v.trackMaterialised(instanceID, tmpPath)
	return tmpPath, nil
}

// trackMaterialised records tmpPath against instanceID so Kill /
// DestroyWithExport can Remove it during teardown. Per-instance
// lifecycle: registered on materialize, cleared on the next Kill.
func (v *JailerVMM) trackMaterialised(instanceID, tmpPath string) {
	if instanceID == "" || tmpPath == "" {
		return
	}
	v.mu.Lock()
	v.materialisedTmp[instanceID] = append(v.materialisedTmp[instanceID], tmpPath)
	v.mu.Unlock()
}

// sweepMaterialised Removes every tmp path tracked against instanceID
// and clears the slot. Best-effort: a missing tmp file is not an error;
// anything else is logged so a leak is observable but never blocks the
// chroot teardown.
func (v *JailerVMM) sweepMaterialised(instanceID string) {
	v.mu.Lock()
	paths := v.materialisedTmp[instanceID]
	delete(v.materialisedTmp, instanceID)
	v.mu.Unlock()
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Default().Warn("vmm: remove materialised tmp",
				"path", p, "instance", instanceID, "err", err)
		}
	}
}

//nolint:forbidigo // src/dst are vetted slot/instance-id paths under /srv/fc — vmmd is the sole writer of this directory; the tmpfs jail root means symlink-attack would require root (which vmmd already has, by spec §11). Copy is an internal migration helper, not a customer-path surface.
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		if cErr := in.Close(); cErr != nil && err == nil {
			err = cErr
		}
	}()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	defer func() {
		if cErr := out.Close(); cErr != nil && err == nil {
			err = cErr
		}
	}()
	_, err = io.Copy(out, in)
	return err
}
