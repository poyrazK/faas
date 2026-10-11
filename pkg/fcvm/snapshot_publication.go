package fcvm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/guestmemproto"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Complete local memory writeback before returning a usable capture. moveOut's
// cross-filesystem copy closes without syncing; otherwise the first restore's
// bind-source permission sync also flushes the entire new memory image.
func publishLocalSnapshotMemory(ctx context.Context, source, destination string, syncFile func(string) error) (size, content int64, err error) {
	size, content, err = moveOutSparse(ctx, source, destination)
	if err != nil {
		return 0, -1, err
	}
	if err := syncFile(destination); err != nil {
		return 0, -1, fmt.Errorf("sync published memory: %w", err)
	}
	return size, content, nil
}

// moveOutSparse is moveOut for Firecracker memory files. The jail is tmpfs,
// so the rename onto /srv/fc crosses filesystems and moveOut's copyFile
// fallback would write every zero page as an allocated block. The cache then
// reflinks that dense file, so the capturing node kept the full guest RAM on
// disk and recorded it as stored_bytes while the real content was a fraction
// of it. The fallback here leaves zero pages as holes. content is the non-zero
// byte count, or -1 when a same-filesystem rename meant nothing was scanned.
func moveOutSparse(ctx context.Context, src, dst string) (size, content int64, err error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return 0, -1, err
	}
	if err := os.Rename(src, dst); err == nil {
		fi, err := os.Stat(dst)
		if err != nil {
			return 0, -1, err
		}
		return fi.Size(), -1, nil
	}
	return copySparseFile(ctx, src, dst)
}

// copySparseFile is moveOutSparse's cross-filesystem branch: a zero-skipping
// copy to dst, then removal of src.
func copySparseFile(ctx context.Context, src, dst string) (size, content int64, err error) {
	// nolint:forbidigo // src is the vmmd-owned jail memory file.
	in, err := os.Open(src)
	if err != nil {
		return 0, -1, err
	}
	defer func() { _ = in.Close() }()
	// nolint:forbidigo // dst is a vmmd-allocated snapshot destination.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
	if err != nil {
		return 0, -1, err
	}
	size, content, err = storage.CopySparse(ctx, out, in)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(dst)
		return 0, -1, err
	}
	_ = os.Remove(src)
	return size, content, nil
}

// logSnapshotMemory records one line per capture describing what the memory
// file holds: logical size, non-zero content (-1 when unscanned), recorded
// stored_bytes and the guest's own meminfo breakdown when it answered.
func logSnapshotMemory(instance string, memBytes, memContent, storedBytes int64, guest guestmemproto.Stats, guestErr error) {
	attrs := []any{
		"instance", instance,
		"mem_bytes", memBytes,
		"mem_content_bytes", memContent,
		"stored_bytes", storedBytes,
	}
	if guestErr != nil {
		attrs = append(attrs, "guest_meminfo_err", guestErr.Error())
	} else {
		attrs = append(attrs,
			"guest_mem_free", guest.MemFree,
			"guest_cached", guest.Cached,
			"guest_buffers", guest.Buffers,
			"guest_shmem", guest.Shmem,
			"guest_anon", guest.AnonPages,
			"guest_slab", guest.Slab,
			"guest_slab_reclaimable", guest.SReclaimable,
			"guest_kernel_stack", guest.KernelStack,
			"guest_page_tables", guest.PageTables,
		)
	}
	slog.Default().Info("vmm: snapshot memory", attrs...)
}

func syncLocalSnapshotMemory(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("published snapshot memory must be a regular file")
	}
	if err == nil {
		err = f.Sync()
	}
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	return syncResourceParent(path)
}

func (v *JailerVMM) cleanupFailedSnapshotCapture(ctx context.Context, spec SnapshotSpec) {
	if v.storage == nil || !state.IsSnapshotCaptureKey(spec.StorageKey) {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	snap := state.Snapshot{StorageKey: spec.StorageKey}
	for _, key := range []string{spec.StorageKey, state.SnapshotVMStateKey(snap), state.SnapshotDriveKey(snap)} {
		if key == "" {
			continue
		}
		if err := v.storage.Delete(cleanup, key); err != nil {
			slog.Default().Warn("vmm: remove failed snapshot capture", "key", key, "err", err)
		}
	}
}
