// adr: 20261022184951 — amendment 1: the drift sweep reads vmmd's private snapshot tree.
package main

import (
	"log/slog"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/storage"
)

// nativeRuntimeSnapshotLister opens vmmd's private snapshot tree read-only for
// the drift sweep. The constructor creates nothing, and listing a tree that
// does not exist returns no keys, so nodes without native runtime capture are
// unaffected.
func nativeRuntimeSnapshotLister(log *slog.Logger) storage.LocalArtifactLister {
	root := strings.TrimSpace(os.Getenv("FAAS_NATIVE_RUNTIME_SNAPSHOT_ROOT"))
	if root == "" {
		root = "/srv/fc/native-snap"
	}
	backend, err := storage.NewLocalStorageBackend(root)
	if err != nil {
		log.Warn("schedd: native runtime snapshot root unusable; drift sweep reads the shared backend only", "root", root, "err", err)
		return nil
	}
	return backend
}
