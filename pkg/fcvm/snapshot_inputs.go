package fcvm

// adr: 778
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type SnapshotArtifactIdentity struct {
	Key    string `json:"key"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// SnapshotInputs names measured capture dimensions and the exact completed
// three-part artifact set. It grants neither capture nor runtime admission.
type SnapshotInputs struct {
	Version   int                      `json:"version"`
	FCVersion string                   `json:"fc_version"`
	Machine   Machine                  `json:"machine"`
	Memory    SnapshotArtifactIdentity `json:"memory"`
	VMState   SnapshotArtifactIdentity `json:"vmstate"`
	Drive     SnapshotArtifactIdentity `json:"drive"`
}

func (s SnapshotInputs) Validate(memKey string) error {
	snap := state.Snapshot{StorageKey: memKey}
	if s.Version != 1 || s.FCVersion == "" || strings.TrimSpace(s.FCVersion) != s.FCVersion ||
		s.Machine.MemSizeMib <= 0 || int64(s.Machine.MemSizeMib) > math.MaxInt64>>20 || s.Machine.VcpuCount <= 0 ||
		!state.IsSnapshotCaptureKey(memKey) || state.SnapshotDriveKey(snap) == "" || s.Memory.Key != memKey ||
		s.VMState.Key != state.SnapshotVMStateKey(snap) || s.Drive.Key != state.SnapshotDriveKey(snap) ||
		s.Memory.Bytes != int64(s.Machine.MemSizeMib)<<20 {
		return ErrSnapshotBackingUnverified
	}
	for _, part := range []SnapshotArtifactIdentity{s.Memory, s.VMState, s.Drive} {
		if part.Bytes <= 0 || !snapshotDigestValid(part.SHA256) {
			return ErrSnapshotBackingUnverified
		}
	}
	return nil
}

func snapshotDigestValid(digest string) bool {
	b, err := hex.DecodeString(digest)
	return err == nil && len(b) == sha256.Size && hex.EncodeToString(b) == digest
}

func decodeSnapshotBacking(raw []byte) (BackingIdentity, error) {
	var identity BackingIdentity
	if len(raw) == 0 || len(raw) > api.SnapshotBackingMaxBytes {
		return identity, ErrSnapshotBackingUnverified
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&identity) != nil || !errors.Is(d.Decode(&struct{}{}), io.EOF) || !identity.complete() ||
		identity.Inputs != nil && identity.Inputs.Validate(identity.Inputs.Memory.Key) != nil {
		return BackingIdentity{}, ErrSnapshotBackingUnverified
	}
	canonical, err := json.Marshal(identity)
	if err != nil || !bytes.Equal(canonical, raw) {
		return BackingIdentity{}, ErrSnapshotBackingUnverified
	}
	return identity, nil
}

func readSnapshotBacking(ctx context.Context, backend storage.StorageBackend, memKey string) (BackingIdentity, error) {
	key := state.SnapshotBackingKey(state.Snapshot{StorageKey: memKey})
	if backend == nil || key == "" {
		return BackingIdentity{}, ErrSnapshotBackingUnverified
	}
	r, err := backend.Get(ctx, key)
	if err != nil {
		return BackingIdentity{}, err
	}
	if r == nil {
		return BackingIdentity{}, ErrSnapshotBackingUnverified
	}
	raw, readErr := io.ReadAll(io.LimitReader(r, api.SnapshotBackingMaxBytes+1))
	err = errors.Join(readErr, r.Close(), ctx.Err())
	if err != nil {
		return BackingIdentity{}, err
	}
	return decodeSnapshotBacking(raw)
}

// Inspect the selected capture before any chroot, file staging or task start.
// Native restores require original dimensions, including historical gaps.
func (v *JailerVMM) restoreSnapshotInputs(ctx context.Context, owner nativeLaunchRecord, l Lease, spec RestoreSpec) (*SnapshotInputs, error) {
	identity, err := readSnapshotBacking(ctx, v.storage, spec.StorageKey)
	if err != nil {
		if v.nativeRecovery == nil && (v.storage == nil || errors.Is(err, storage.ErrNotFound)) {
			return nil, nil
		}
		return nil, fmt.Errorf("snapshot input: backing identity: %w", err)
	}
	inputs := identity.Inputs
	if inputs == nil {
		if v.nativeRecovery != nil {
			return nil, ErrSnapshotBackingUnverified
		}
		return nil, nil
	}
	if inputs.Validate(spec.StorageKey) != nil || spec.FCVersion == "" || inputs.FCVersion != spec.FCVersion ||
		l.MemoryMaxMiB > 0 && inputs.Machine.MemSizeMib != l.MemoryMaxMiB ||
		spec.ExpectedMachine != nil && inputs.Machine != *spec.ExpectedMachine {
		return nil, ErrSnapshotBackingChanged
	}
	if v.nativeRecovery != nil {
		if spec.VMStateStorageKey != inputs.VMState.Key {
			return nil, ErrSnapshotBackingUnverified
		}
		if err := checkNativeBootDimensions(owner, inputs.Machine); err != nil {
			return nil, err
		}
	}
	copy := *inputs
	return &copy, ctx.Err()
}

// The legacy producer reads its paused private API. Native capture remains
// gated until its pinned mutation/capacity producer is implemented.
func (v *JailerVMM) snapshotMachine(ctx context.Context, l Lease) (Machine, string, error) {
	var machine Machine
	read := func(path string) (map[string]json.RawMessage, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost"+path, nil)
		if err != nil {
			return nil, err
		}
		resp, err := v.fcClient(l.Instance).Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, api.SnapshotMachineResponseMaxBytes+1))
		if err != nil || resp.StatusCode != http.StatusOK || len(raw) > api.SnapshotMachineResponseMaxBytes {
			return nil, errors.Join(err, ErrSnapshotBackingUnverified)
		}
		return nativeMachineObject(raw)
	}
	info, err := read("/")
	if err != nil {
		return machine, "", err
	}
	var id, app, version, vmState string
	if err := errors.Join(nativeMachineField(info, "id", &id), nativeMachineField(info, "app_name", &app), nativeMachineField(info, "vmm_version", &version), nativeMachineField(info, "state", &vmState)); err != nil ||
		id != l.Instance || app != "Firecracker" || version == "" || vmState != "Paused" {
		return machine, "", errors.Join(err, ErrSnapshotBackingUnverified)
	}
	config, err := read("/machine-config")
	if err != nil {
		return machine, "", err
	}
	if err := errors.Join(nativeMachineField(config, "vcpu_count", &machine.VcpuCount), nativeMachineField(config, "mem_size_mib", &machine.MemSizeMib), nativeMachineField(config, "smt", &machine.Smt)); err != nil ||
		machine.VcpuCount <= 0 || machine.MemSizeMib <= 0 || l.MemoryMaxMiB > 0 && machine.MemSizeMib != l.MemoryMaxMiB {
		return machine, "", errors.Join(err, ErrSnapshotBackingUnverified)
	}
	after, err := read("/")
	if err != nil {
		return machine, "", err
	}
	for _, field := range []string{"id", "app_name", "vmm_version", "state"} {
		if !bytes.Equal(info[field], after[field]) {
			return machine, "", ErrSnapshotBackingUnverified
		}
	}
	return machine, version, nil
}

type snapshotContextReader struct {
	ctx context.Context
	io.Reader
}

func (r snapshotContextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(b)
}

func snapshotFileIdentity(ctx context.Context, path, key string) (part SnapshotArtifactIdentity, err error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return part, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	before, err := snapshotFileStamp(f)
	if err != nil {
		return part, err
	}
	h := sha256.New()
	size, err := io.Copy(h, snapshotContextReader{ctx: ctx, Reader: f})
	if err != nil {
		return part, err
	}
	after, err := snapshotFileStamp(f)
	if err != nil || after != before || size != before.Size {
		return part, errors.Join(err, ErrSnapshotBackingUnverified)
	}
	return SnapshotArtifactIdentity{Key: key, Bytes: size, SHA256: hex.EncodeToString(h.Sum(nil))}, ctx.Err()
}

func (v *JailerVMM) captureSnapshotInputs(ctx context.Context, l Lease, spec SnapshotSpec, machine Machine, version, frozenDrive string) (*SnapshotInputs, error) {
	if version == "" {
		return nil, nil
	}
	root := v.chrootRoot(l.Instance)
	inputs := SnapshotInputs{Version: 1, FCVersion: version, Machine: machine}
	var err error
	inputs.Memory, err = snapshotFileIdentity(ctx, filepath.Join(root, "mem"), spec.StorageKey)
	if err != nil {
		return nil, err
	}
	inputs.VMState, err = snapshotFileIdentity(ctx, filepath.Join(root, "vmstate"), spec.VMStateStorageKey)
	if err != nil {
		return nil, err
	}
	inputs.Drive, err = snapshotFileIdentity(ctx, frozenDrive, state.SnapshotDriveKey(state.Snapshot{StorageKey: spec.StorageKey}))
	if err != nil {
		return nil, err
	}
	after, afterVersion, err := v.snapshotMachine(ctx, l)
	if err != nil || after != machine || afterVersion != version {
		return nil, errors.Join(err, ErrSnapshotBackingUnverified)
	}
	return &inputs, inputs.Validate(spec.StorageKey)
}

// Descriptors and their exact metadata remain pinned through synchronous load.
// No verified inputs are cached or reused as current native task authority.
func (v *JailerVMM) withSnapshotInputs(ctx context.Context, l Lease, root, memName, stateName, driveSource string, inputs *SnapshotInputs, consume func() error) (err error) {
	if consume == nil {
		return ErrSnapshotBackingUnverified
	}
	if inputs == nil {
		if v.nativeRecovery != nil {
			return ErrSnapshotBackingUnverified
		}
		return consume()
	}
	if inputs.Validate(inputs.Memory.Key) != nil || l.MemoryMaxMiB > 0 && inputs.Machine.MemSizeMib != l.MemoryMaxMiB {
		return ErrSnapshotBackingChanged
	}
	parts := []SnapshotArtifactIdentity{inputs.Memory, inputs.VMState, inputs.Drive}
	paths := []string{filepath.Join(root, memName), filepath.Join(root, stateName), driveSource}
	files := make([]*os.File, 0, len(parts))
	stamps := make([]snapshotInputFileStamp, 0, len(parts))
	defer func() {
		for _, f := range files {
			err = errors.Join(err, f.Close())
		}
	}()
	for i, part := range parts {
		path := paths[i]
		f, openErr := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if openErr != nil {
			return openErr
		}
		files = append(files, f)
		stamp, statErr := snapshotFileStamp(f)
		if statErr != nil {
			return statErr
		}
		if stamp.Size != part.Bytes {
			return ErrSnapshotBackingChanged
		}
		h := sha256.New()
		if _, copyErr := io.Copy(h, snapshotContextReader{ctx: ctx, Reader: f}); copyErr != nil {
			return copyErr
		}
		after, statErr := snapshotFileStamp(f)
		if statErr != nil || after != stamp || hex.EncodeToString(h.Sum(nil)) != part.SHA256 {
			return errors.Join(statErr, ErrSnapshotBackingChanged)
		}
		stamps = append(stamps, stamp)
	}
	check := func() error {
		for i, f := range files {
			stamp, statErr := snapshotFileStamp(f)
			current, pathErr := os.Lstat(paths[i])
			original, fileErr := f.Stat()
			if statErr != nil || pathErr != nil || fileErr != nil || stamp != stamps[i] || !os.SameFile(current, original) {
				return errors.Join(statErr, pathErr, fileErr, ErrSnapshotBackingChanged)
			}
		}
		return ctx.Err()
	}
	if err := check(); err != nil {
		return err
	}
	return errors.Join(consume(), check())
}
