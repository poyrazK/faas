package fcvm

// adr: 779
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
)

// The source inode is deliberately not the running inode: jailer copies the
// selected binary. Its original bytes bind that copy to the prepared owner.
type nativeExecutableRecord struct {
	Version int                `json:"version"`
	Owner   nativeLaunchRecord `json:"owner"`
	Bytes   int64              `json:"bytes"`
	SHA256  string             `json:"sha256"`
}

// NativeRuntimeExecutable is freshly measured from a pinned original task,
// after comparison with its retained launch source. It grants no effects.
type NativeRuntimeExecutable struct {
	Device, Inode uint64
	Bytes         int64
	SHA256        string
}

func (e NativeRuntimeExecutable) valid() bool {
	digest, err := hex.DecodeString(e.SHA256)
	return e.Device != 0 && e.Inode != 0 && e.Bytes > 0 && err == nil && len(digest) == sha256.Size && hex.EncodeToString(digest) == e.SHA256
}

func nativeExecutableOwner(owner nativeLaunchRecord) nativeLaunchRecord {
	owner.Authorized, owner.Revoked, owner.ExitConfirmed, owner.ResourcesRemoved = false, false, false, false
	owner.PID, owner.StartTime = 0, 0
	owner.Lease.processGeneration = 0
	return owner
}

func (r *nativeExecutableRecord) UnmarshalJSON(raw []byte) error {
	if _, err := nativeJournalObjectFields(raw, []string{"version", "owner", "bytes", "sha256"}); err != nil {
		return err
	}
	type plain nativeExecutableRecord
	return json.Unmarshal(raw, (*plain)(r))
}

func (r nativeExecutableRecord) validate(owner nativeLaunchRecord) error {
	if r.Version != 1 || r.Owner.validate(owner.Lease.Instance) != nil || !reflect.DeepEqual(r.Owner, nativeExecutableOwner(owner)) ||
		!(NativeRuntimeExecutable{Device: 1, Inode: 1, Bytes: r.Bytes, SHA256: r.SHA256}).valid() {
		return errors.New("native executable: original source binding is invalid")
	}
	return nil
}

func (j *nativeLaunchJournal) executablePath(owner nativeLaunchRecord) string {
	return filepath.Join(j.root, "executables", owner.Generation+".json")
}

// Caller holds the owner lock. Missing historical input stays unknown; it is
// never reconstructed from a currently running task or a replacement binary.
func (j *nativeLaunchJournal) readExecutable(owner nativeLaunchRecord) (record *nativeExecutableRecord, err error) {
	path := j.executablePath(owner)
	if _, err := os.Lstat(filepath.Dir(path)); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	if err := checkNativeJournalPath(filepath.Dir(path), true); err != nil {
		return nil, err
	}
	if err := checkNativeJournalPath(path, false); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	f, err := openNativeJournalFile(path, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	raw, err := io.ReadAll(io.LimitReader(f, api.NativeExecutableRecordMaxBytes+1))
	if err != nil || len(raw) > api.NativeExecutableRecordMaxBytes {
		return nil, errors.Join(err, errors.New("native executable: oversized source record"))
	}
	var value nativeExecutableRecord
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return &value, value.validate(owner)
}

// This precedes child creation and authorization in startJailer. A retry may
// reuse identical bytes, but cannot replace the selected source for a generation.
func (j *nativeLaunchJournal) captureExecutable(ctx context.Context, lease Lease, path string) (err error) {
	lock, err := j.lock(ctx, lease.Instance)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	owner, err := j.read(lease.Instance)
	if err != nil {
		return err
	}
	if owner.Authorized || owner.Revoked || !sameNativePhysicalLease(owner.Lease, lease) || !filepath.IsAbs(path) {
		return errors.New("native executable: original prepared owner and absolute source are required")
	}
	if err := checkNativeEffectDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := openNativeJournalFile(path, os.O_RDONLY)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Geteuid() || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Mode().Perm()&0o111 == 0 {
		return errors.New("native executable: protected owned executable source is required")
	}
	size, digest, _, err := nativeExecutableDigest(ctx, f)
	if err != nil {
		return err
	}
	value := nativeExecutableRecord{Version: 1, Owner: nativeExecutableOwner(owner), Bytes: size, SHA256: digest}
	previous, err := j.readExecutable(owner)
	if err != nil {
		return err
	}
	if previous != nil {
		if !reflect.DeepEqual(*previous, value) {
			return errors.New("native executable: selected source changed within original generation")
		}
		return ctx.Err()
	}
	root := filepath.Dir(j.executablePath(owner))
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	if err := checkNativeJournalPath(root, true); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeNativeJournalValue(j.executablePath(owner), value)
}

func nativeExecutableDigest(ctx context.Context, f *os.File) (int64, string, snapshotInputFileStamp, error) {
	before, err := snapshotFileStamp(f)
	if err != nil {
		return 0, "", before, err
	}
	if before.Mode&0o111 == 0 {
		return 0, "", before, errors.New("native executable: non-executable input")
	}
	var magic [4]byte
	if _, err := f.ReadAt(magic[:], 0); err != nil || !bytes.Equal(magic[:], []byte{0x7f, 'E', 'L', 'F'}) {
		return 0, "", before, errors.New("native executable: ELF input required")
	}
	h := sha256.New()
	size, err := io.Copy(h, snapshotContextReader{ctx: ctx, Reader: f})
	if err != nil {
		return 0, "", before, err
	}
	after, err := snapshotFileStamp(f)
	if err != nil || before != after || before.Size != size {
		return 0, "", before, errors.Join(err, errors.New("native executable: input changed while hashing"))
	}
	return size, hex.EncodeToString(h.Sum(nil)), before, ctx.Err()
}

func (s *nativeMachineAPI) checkExecutable(ctx context.Context) error {
	current, err := s.v.nativeRecovery.journal.readExecutable(s.owner)
	if err != nil || !reflect.DeepEqual(current, s.executableSource) {
		return errors.Join(err, errors.New("native executable: original source record changed"))
	}
	if current == nil {
		if s.inventory != nil {
			return ctx.Err()
		}
		return errors.New("native executable: original selected source is unavailable")
	}
	path := filepath.Join(s.v.nativeRecovery.retirer.probe.root, strconv.Itoa(s.owner.PID), "exe")
	if s.executableFile == nil {
		// Follow only the trusted procfs exe link. The pidfd and incarnation are
		// retained and checked around this operation by the enclosing session.
		//nolint:forbidigo // Kernel exe link for the retained original PID, never a customer path.
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		s.executableFile = f
		size, digest, stamp, err := nativeExecutableDigest(ctx, f)
		if err != nil || size != current.Bytes || digest != current.SHA256 {
			return errors.Join(err, errors.New("native executable: running bytes differ from original source"))
		}
		s.executableStamp = stamp
		s.executable = &NativeRuntimeExecutable{Device: stamp.Device, Inode: stamp.Inode, Bytes: size, SHA256: digest}
	}
	stamp, err := snapshotFileStamp(s.executableFile)
	if err != nil || stamp != s.executableStamp {
		return errors.Join(err, errors.New("native executable: pinned input changed"))
	}
	opened, err := s.executableFile.Stat()
	if err != nil {
		return err
	}
	now, err := os.Stat(path)
	if err != nil || !os.SameFile(opened, now) {
		return errors.Join(err, errors.New("native executable: original task replaced its executable"))
	}
	return errors.Join(s.handle.CheckLive(), ctx.Err())
}
