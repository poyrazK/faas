// adr: 20261022184951 — amendment 1: vmmd removes native runtime snapshots on imaged's behalf.
package imaged

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// nativeRuntimeSnapshotRetirer is vmmd's removal of one native runtime
// snapshot object. imaged decides what to collect, but only vmmd can write the
// tree those objects live in.
type nativeRuntimeSnapshotRetirer interface {
	RetireNativeRuntimeSnapshotObject(ctx context.Context, key string) error
}

// RetireNativeRuntimeSnapshotObject asks vmmd to remove one object of a native
// runtime capture. A missing object is success on the vmmd side.
func (c *VMMClient) RetireNativeRuntimeSnapshotObject(ctx context.Context, key string) error {
	if c == nil {
		return fmt.Errorf("imaged: vmmclient: nil receiver")
	}
	cli, err := c.dial(ctx)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, defaultVMMDialTimeout)
	defer cancel()
	if _, err := cli.RetireNativeRuntimeSnapshotObject(callCtx, &vmmdpb.RetireNativeRuntimeSnapshotObjectRequest{StorageKey: key}); err != nil {
		return fmt.Errorf("imaged: vmmclient: retire native runtime snapshot object %q: %w", key, err)
	}
	return nil
}

// WithNativeRuntimeSnapshotRoot points collection at vmmd's private snapshot
// tree. imaged only reads it (group read) to learn which objects vmmd must
// remove; an empty root keeps every delete on the storage backend.
func (h *Handler) WithNativeRuntimeSnapshotRoot(root string) *Handler {
	h.nativeSnapshotRoot = ""
	if root = strings.TrimSpace(root); root != "" {
		h.nativeSnapshotRoot = filepath.Clean(root)
	}
	return h
}

// nativeRuntimeSnapshotMemKey maps one object of a v2 capture (memory, device
// state, drive or backing sidecar) to the capture's memory key. Only these
// objects can live in vmmd's private tree.
func nativeRuntimeSnapshotMemKey(key string) (string, bool) {
	slash := strings.LastIndex(key, "/")
	if slash < 0 {
		return "", false
	}
	switch key[slash+1:] {
	case "mem", "vmstate", "drive", "backing":
	default:
		return "", false
	}
	memory := key[:slash+1] + "mem"
	if !strings.HasSuffix(memory, "/v2/mem") || !state.IsSnapshotCaptureKey(memory) {
		return "", false
	}
	return memory, true
}

// nativeRuntimeSnapshotResident reports whether vmmd's private tree holds key.
// An unreadable tree is an error, not an absence, so the caller keeps the row
// instead of reporting a delete that never happened.
func (h *Handler) nativeRuntimeSnapshotResident(key string) (bool, error) {
	if h.nativeSnapshotRoot == "" {
		return false, nil
	}
	if _, ok := nativeRuntimeSnapshotMemKey(key); !ok {
		return false, nil
	}
	_, err := os.Lstat(filepath.Join(h.nativeSnapshotRoot, filepath.FromSlash(key)))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

// deleteSnapshotObject removes one snapshot object wherever it lives: through
// vmmd when the object is in vmmd's private tree, through the backend
// otherwise. A key names an object in at most one tree.
func (h *Handler) deleteSnapshotObject(ctx context.Context, be storage.StorageBackend, key string) error {
	resident, err := h.nativeRuntimeSnapshotResident(key)
	if err != nil {
		return fmt.Errorf("imaged: inspect native runtime snapshot %s: %w", key, err)
	}
	if !resident {
		return be.Delete(ctx, key)
	}
	return h.retireNativeRuntimeSnapshotObject(ctx, key)
}

// retireNativeRuntimeSnapshotObject has vmmd remove one object from its tree.
func (h *Handler) retireNativeRuntimeSnapshotObject(ctx context.Context, key string) error {
	retirer, ok := h.vmmClient.(nativeRuntimeSnapshotRetirer)
	if !ok || retirer == nil {
		return fmt.Errorf("imaged: native runtime snapshot %s can only be removed by vmmd, and no vmmd client is wired", key)
	}
	return retirer.RetireNativeRuntimeSnapshotObject(ctx, key)
}

// nativeRuntimeSnapshotKeys lists the v2 capture objects vmmd's private tree
// holds for one deployment.
func (h *Handler) nativeRuntimeSnapshotKeys(depID string) ([]string, error) {
	if h.nativeSnapshotRoot == "" {
		return nil, nil
	}
	if uuid.Validate(depID) != nil {
		return nil, nil //nolint:nilerr // vmmd keys its tree by deployment uuid; any other ID owns nothing there.
	}
	return nativeRuntimeSnapshotFiles(h.nativeSnapshotRoot, depID, time.Time{})
}

// nativeRuntimeSnapshotFiles walks <root>/snap/<depID> without following links
// and returns the v2 capture object keys under it, keeping only files last
// modified before cutoff when cutoff is set.
func nativeRuntimeSnapshotFiles(root, depID string, cutoff time.Time) ([]string, error) {
	top := filepath.Join(root, "snap", depID)
	var keys []string
	err := filepath.WalkDir(top, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		if _, ok := nativeRuntimeSnapshotMemKey(key); !ok {
			return nil
		}
		if !cutoff.IsZero() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.ModTime().Before(cutoff) {
				return nil
			}
		}
		keys = append(keys, key)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return keys, err
}

// retireNativeRuntimeSnapshotOrphans is removeLocalSnapshotOrphans for vmmd's
// private tree: objects of deployments with no snapshot row left are retired
// through vmmd. Both the directory and each file must be older than the grace,
// so a capture published just before its row commits is never taken.
func (l *Loop) retireNativeRuntimeSnapshotOrphans(ctx context.Context, known map[string]struct{}, cutoff time.Time) {
	if l.handler == nil || l.handler.nativeSnapshotRoot == "" {
		return
	}
	root := l.handler.nativeSnapshotRoot
	snapRoot := filepath.Join(root, "snap")
	entries, err := os.ReadDir(snapRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		l.log.Warn("imaged: native runtime snapshot orphan scan", "root", snapRoot, "err", err)
		return
	}
	retired := 0
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		deploymentID := entry.Name()
		if !entry.IsDir() {
			continue
		}
		if _, err := uuid.Parse(deploymentID); err != nil {
			continue
		}
		if _, ok := known[deploymentID]; ok {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		keys, err := nativeRuntimeSnapshotFiles(root, deploymentID, cutoff)
		if err != nil {
			l.log.Warn("imaged: native runtime snapshot orphan list", "deployment", deploymentID, "err", err)
			continue
		}
		for _, key := range keys {
			if err := l.handler.retireNativeRuntimeSnapshotObject(ctx, key); err != nil {
				l.log.Warn("imaged: native runtime snapshot orphan retire", "deployment", deploymentID, "key", key, "err", err)
				continue
			}
			retired++
		}
	}
	if retired > 0 {
		l.log.Info("imaged: native runtime snapshot orphans retired", "count", retired, "root", snapRoot)
	}
}
