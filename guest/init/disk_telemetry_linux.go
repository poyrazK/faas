//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

const (
	diskTelemetryType        byte = 0x06
	diskTelemetryInterval         = 5 * time.Second
	diskTelemetryMaxBody          = 256
	diskTelemetrySendTimeout      = time.Second
)

type diskTelemetryWire struct {
	UsedBytes     int64 `json:"used_bytes"`
	CapacityBytes int64 `json:"capacity_bytes"`
}

// startDiskTelemetry samples the merged root filesystem. For the normal
// overlay boot, statfs reports the drive1 upper filesystem; for full-rootfs
// artifacts it reports the writable image directly. Samples are best-effort
// and bounded so telemetry can never delay workload startup or shutdown.
func startDiskTelemetry(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		send := func() {
			used, capacity, err := writableFilesystemUsage()
			if err != nil {
				return
			}
			if err := emitDiskTelemetry(ctx, used, capacity); err != nil {
				// A guest without vsock support simply has no disk signal;
				// the host keeps the sample absent and continues serving.
				return
			}
		}
		send()
		ticker := time.NewTicker(diskTelemetryInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				send()
			}
		}
	}()
}

func writableFilesystemUsage() (int64, int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs("/", &stat); err != nil {
		return 0, 0, fmt.Errorf("statfs root: %w", err)
	}
	if stat.Blocks == 0 || stat.Bsize <= 0 || stat.Bfree > stat.Blocks {
		return 0, 0, fmt.Errorf("invalid statfs root values")
	}
	blockSize := uint64(stat.Bsize)
	maxInt64 := uint64(^uint64(0) >> 1)
	if stat.Blocks > maxInt64/blockSize {
		return 0, 0, fmt.Errorf("statfs root values overflow int64")
	}
	capacity := stat.Blocks * blockSize
	used := (stat.Blocks - stat.Bfree) * blockSize
	if used > maxInt64 {
		return 0, 0, fmt.Errorf("statfs root values overflow int64")
	}
	return int64(used), int64(capacity), nil
}

func emitDiskTelemetry(ctx context.Context, usedBytes, capacityBytes int64) error {
	if usedBytes < 0 || capacityBytes <= 0 || usedBytes > capacityBytes {
		return fmt.Errorf("invalid disk sample used=%d capacity=%d", usedBytes, capacityBytes)
	}
	body, err := json.Marshal(diskTelemetryWire{UsedBytes: usedBytes, CapacityBytes: capacityBytes})
	if err != nil {
		return err
	}
	if len(body) > diskTelemetryMaxBody {
		return fmt.Errorf("disk telemetry body too large: %d", len(body))
	}
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	tv := unix.Timeval{Sec: 1}
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &tv); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	frame := make([]byte, 1+len(body))
	frame[0] = diskTelemetryType
	copy(frame[1:], body)
	_, err = unix.SendmsgN(fd, frame, nil, &unix.SockaddrVM{CID: unix.VMADDR_CID_HOST, Port: VsockFrameworkReadyPort}, 0)
	return err
}
