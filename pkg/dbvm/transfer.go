package dbvm

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Moving a database to another node (ADR-1027) copies its parked volume:
// the source exports the volume's data extents and the destination
// imports them into a new sparse file. Both sides hash what passes: each
// extent's offset, length and bytes, then the volume size. The destination
// installs the volume only when its hash matches the source's.

// TransferChunk is the most volume bytes one transfer extent carries.
const TransferChunk = 1 << 20

// ErrTransferDigest: the imported volume does not hash to the exported one.
var ErrTransferDigest = errors.New("dbvm: imported volume does not match the export")

// transferHash hashes a volume transfer.
type transferHash struct{ h hash.Hash }

func newTransferHash() transferHash { return transferHash{sha256.New()} }

func (t transferHash) extent(offset int64, data []byte) {
	var hdr [16]byte
	binary.BigEndian.PutUint64(hdr[:8], uint64(offset)) //nolint:gosec // offsets are non-negative
	binary.BigEndian.PutUint64(hdr[8:], uint64(len(data)))
	_, _ = t.h.Write(hdr[:])
	_, _ = t.h.Write(data)
}

func (t transferHash) sum(size int64) []byte {
	var tail [8]byte
	binary.BigEndian.PutUint64(tail[:], uint64(size)) //nolint:gosec // sizes are non-negative
	_, _ = t.h.Write(tail[:])
	return t.h.Sum(nil)
}

// VolumeExport reads a stopped database's volume for a transfer.
type VolumeExport struct {
	release func()
	f       *os.File
	size    int64
	next    int64
	buf     []byte
	hash    transferHash
}

// ExportVolume opens a stopped database's volume for export. The database
// cannot start until the export is closed.
func (r *Runtime) ExportVolume(id string) (*VolumeExport, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	release, err := r.hold(id)
	if err != nil {
		return nil, err
	}
	// A vetted id under the volume root; never follow a planted symlink.
	f, err := os.OpenFile(r.VolumePath(id), os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		release()
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrVolumeNotFound
		}
		return nil, fmt.Errorf("dbvm: open volume for export: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		release()
		return nil, fmt.Errorf("dbvm: stat volume: %w", err)
	}
	return &VolumeExport{release: release, f: f, size: info.Size(), buf: make([]byte, TransferChunk), hash: newTransferHash()}, nil
}

// Size is the volume's size in bytes.
func (e *VolumeExport) Size() int64 { return e.size }

// Next returns the next extent holding data, up to TransferChunk bytes;
// holes and all-zero chunks are skipped, since a sparse file reads them as
// zeros anyway. It returns io.EOF after the last extent. The slice is
// reused by the next call.
func (e *VolumeExport) Next() (int64, []byte, error) {
	for e.next < e.size {
		start, err := seekData(e.f, e.next)
		if errors.Is(err, io.EOF) || start >= e.size {
			e.next = e.size
			break
		}
		if err != nil {
			return 0, nil, fmt.Errorf("dbvm: find volume data: %w", err)
		}
		n, err := e.f.ReadAt(e.buf[:min(int64(TransferChunk), e.size-start)], start)
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, nil, fmt.Errorf("dbvm: read volume: %w", err)
		}
		e.next = start + int64(n)
		if n == 0 {
			e.next = e.size
			break
		}
		data := e.buf[:n]
		if isZero(data) {
			continue
		}
		e.hash.extent(start, data)
		return start, data, nil
	}
	return 0, nil, io.EOF
}

// Sum is the transfer digest; call it after Next returned io.EOF.
func (e *VolumeExport) Sum() []byte { return e.hash.sum(e.size) }

// Close releases the volume; the database may start again.
func (e *VolumeExport) Close() error {
	err := e.f.Close()
	e.release()
	return err
}

// VolumeImport writes a transferred volume into a new file beside the
// volume path; Commit installs it.
type VolumeImport struct {
	release func()
	path    string
	tmp     *os.File
	size    int64
	hash    transferHash
	end     int64
}

// ImportVolume starts importing a database's volume of size bytes. A
// database that already has a volume here is refused; a leftover partial
// import is replaced.
func (r *Runtime) ImportVolume(id string, size int64) (*VolumeImport, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	if size < MinVolumeBytes || size > MaxVolumeBytes {
		return nil, fmt.Errorf("dbvm: volume size %d out of range", size)
	}
	if err := os.MkdirAll(r.cfg.VolumeRoot, 0o700); err != nil {
		return nil, fmt.Errorf("dbvm: volume root: %w", err)
	}
	release, err := r.hold(id)
	if err != nil {
		return nil, err
	}
	path := r.VolumePath(id)
	if _, err := os.Stat(path); err == nil {
		release()
		return nil, ErrVolumeExists
	}
	tmpPath := path + ".import"
	_ = os.Remove(tmpPath)
	tmp, err := os.OpenFile(tmpPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // under the volume root
	if err == nil {
		err = tmp.Truncate(size)
	}
	if err != nil {
		if tmp != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
		release()
		return nil, fmt.Errorf("dbvm: create import: %w", err)
	}
	return &VolumeImport{release: release, path: path, tmp: tmp, size: size, hash: newTransferHash()}, nil
}

// Write stores one extent. Extents arrive in increasing offset order.
func (w *VolumeImport) Write(offset int64, data []byte) error {
	if offset < w.end || offset+int64(len(data)) > w.size || len(data) == 0 || len(data) > TransferChunk {
		return fmt.Errorf("dbvm: import extent at %d (%d bytes) out of order or bounds", offset, len(data))
	}
	if _, err := w.tmp.WriteAt(data, offset); err != nil {
		return fmt.Errorf("dbvm: write import: %w", err)
	}
	w.hash.extent(offset, data)
	w.end = offset + int64(len(data))
	return nil
}

// Commit checks the transfer digest, makes the volume durable and puts it
// in place. A mismatch discards the import.
func (w *VolumeImport) Commit(digest []byte) error {
	defer w.release()
	tmpPath := w.tmp.Name()
	if subtle.ConstantTimeCompare(digest, w.hash.sum(w.size)) != 1 {
		_ = w.tmp.Close()
		_ = os.Remove(tmpPath)
		return ErrTransferDigest
	}
	err := w.tmp.Sync()
	if closeErr := w.tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Link(tmpPath, w.path) // fails if a volume appeared meanwhile
	}
	_ = os.Remove(tmpPath)
	if err != nil {
		return fmt.Errorf("dbvm: install imported volume: %w", err)
	}
	return syncDir(filepath.Dir(w.path))
}

// Abort discards an unfinished import.
func (w *VolumeImport) Abort() {
	_ = w.tmp.Close()
	_ = os.Remove(w.tmp.Name())
	w.release()
}

// hold marks a stopped database busy with a transfer: it cannot start, and
// a second transfer of it is refused.
func (r *Runtime) hold(id string) (func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.vms[id]; ok {
		return nil, ErrRunning
	}
	if r.transfers == nil {
		r.transfers = map[string]bool{}
	}
	if r.transfers[id] {
		return nil, ErrTransferring
	}
	r.transfers[id] = true
	return func() {
		r.mu.Lock()
		delete(r.transfers, id)
		r.mu.Unlock()
	}, nil
}

// ErrTransferring: the database's volume is being exported or imported.
var ErrTransferring = errors.New("dbvm: database volume is being transferred")

// seekData returns the first offset at or after off holding data, or
// io.EOF when none does. Filesystems without SEEK_DATA report the whole
// file as data.
func seekData(f *os.File, off int64) (int64, error) {
	start, err := unix.Seek(int(f.Fd()), off, unix.SEEK_DATA)
	switch {
	case errors.Is(err, unix.ENXIO):
		return 0, io.EOF
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOTSUP), errors.Is(err, unix.EOPNOTSUPP):
		return off, nil
	}
	return start, err
}

func isZero(b []byte) bool {
	for len(b) >= len(zeroBlock) {
		if !bytes.Equal(b[:len(zeroBlock)], zeroBlock[:]) {
			return false
		}
		b = b[len(zeroBlock):]
	}
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

var zeroBlock [4096]byte

func syncDir(dir string) error {
	d, err := os.OpenFile(dir, os.O_RDONLY, 0) //nolint:gosec // the volume root
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
