//go:build linux

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devpatch"
)

// startDevPatchLoop polls vmmd for developer live patches for the main
// workload (ADR-740). Sidecar workloads are never patched.
func startDevPatchLoop(ctx context.Context, sup *Supervisor, log *slog.Logger) {
	go runDevPatchLoop(ctx, log, devPatchIO{
		fetch: fetchDevPatch,
		apply: func(dir string, archive []byte, deleted []string) (devpatch.ApplyResult, error) {
			return devpatch.Apply(dir, archive, deleted, api.DevPatchMaxBytes)
		},
		restart: sup.RequestRestart,
		sleep:   sleepContext,
		ack: func(generation, applyMS int64, errorCode string) {
			if err := sendDevPatchAck(generation, applyMS, errorCode); err != nil {
				log.Debug("developer live patch acknowledgement not delivered", "generation", generation, "err", err)
			}
		},
	})
}

func sendDevPatchAck(generation, applyMS int64, errorCode string) error {
	conn, err := dialRuntimeConfigHost()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	body, err := json.Marshal(runtimeConfigRequest{Kind: "dev_patch_ack", PatchGeneration: generation, PatchApplyMS: applyMS, ErrorCode: errorCode})
	if err != nil {
		return err
	}
	if err := writeRuntimeConfigFrame(conn, body); err != nil {
		return err
	}
	_, err = readRuntimeConfigFrame(conn)
	return err
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func fetchDevPatch(afterGeneration int64) (devPatchPoll, error) {
	conn, err := dialRuntimeConfigHost()
	if err != nil {
		return devPatchPoll{}, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	body, err := json.Marshal(runtimeConfigRequest{Kind: "dev_patch", PatchGeneration: afterGeneration})
	if err != nil {
		return devPatchPoll{}, err
	}
	if err := writeRuntimeConfigFrame(conn, body); err != nil {
		return devPatchPoll{}, err
	}
	frame, err := readRuntimeConfigFrame(conn)
	if err != nil {
		return devPatchPoll{}, err
	}
	var response runtimeConfigResponse
	if err := json.Unmarshal(frame, &response); err != nil {
		return devPatchPoll{}, err
	}
	return devPatchPoll{Patch: response.DevPatch, Unchanged: response.Unchanged, Error: response.Error}, nil
}
