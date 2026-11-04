package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/realtime"
)

// defaultEndpointSnapshotPath is on /run (tmpfs): the snapshot holds endpoint
// credentials, so it outlives a process restart or hand-off but never
// reaches disk, and a reboot clears it (apid's reconcile then registers
// everything again).
const defaultEndpointSnapshotPath = "/run/faas/realtimed/endpoints.json"

// endpointSnapshotDebounce groups registrations that arrive together, such
// as one apid reconcile pass, into one write.
const endpointSnapshotDebounce = 250 * time.Millisecond

func endpointSnapshotPath() string {
	if path := os.Getenv("FAAS_REALTIME_ENDPOINT_SNAPSHOT"); path != "" {
		return path
	}
	return defaultEndpointSnapshotPath
}

// restoreEndpointSnapshot registers the endpoints the previous process saved,
// before this one serves. A missing snapshot is normal on first start.
func restoreEndpointSnapshot(manager *realtime.Manager, path string, log *slog.Logger) {
	data, err := os.ReadFile(path) //nolint:forbidigo // operator-owned runtime path, never a customer path
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		log.Warn("realtimed: read endpoint snapshot; endpoints arrive with apid's next reconcile", "path", path, "err", err)
		return
	}
	restored, err := manager.RestoreEndpoints(data)
	if err != nil {
		log.Warn("realtimed: some saved endpoints were not restored; apid's reconcile registers them", "restored", restored, "err", err)
	}
	log.Info("realtimed: restored endpoints from the previous process", "endpoints", restored)
}

// saveEndpointSnapshots writes the manager's endpoints to path after each
// change, until ctx ends.
func saveEndpointSnapshots(ctx context.Context, manager *realtime.Manager, path string, changed <-chan struct{}, log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
		timer := time.NewTimer(endpointSnapshotDebounce)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := writeEndpointSnapshot(manager, path); err != nil {
			log.Warn("realtimed: save endpoint snapshot", "path", path, "err", err)
		}
	}
}

func writeEndpointSnapshot(manager *realtime.Manager, path string) error {
	data, err := manager.EndpointSnapshot()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".endpoints-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
