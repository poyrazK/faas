package ingress

import (
	"context"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type nativeEpochReader interface {
	Read(context.Context, string) ([]byte, error)
	Link(string) (string, error)
	PID() int
}

func readNativeProcessEpoch(ctx context.Context, reader nativeEpochReader) (NativeProcessEpoch, error) {
	if ctx.Err() != nil {
		return NativeProcessEpoch{}, ErrNativeStartupUnverified
	}
	machine, err := reader.Read(ctx, "machine-id")
	if err != nil || len(machine) != 33 || machine[len(machine)-1] != '\n' {
		return NativeProcessEpoch{}, ErrNativeStartupUnverified
	}
	boot, err := reader.Read(ctx, "sys/kernel/random/boot_id")
	if err != nil || len(boot) != 37 || boot[len(boot)-1] != '\n' {
		return NativeProcessEpoch{}, ErrNativeStartupUnverified
	}
	stat, err := reader.Read(ctx, "self/stat")
	if err != nil {
		return NativeProcessEpoch{}, ErrNativeStartupUnverified
	}
	start, err := nativeProcessStart(stat, reader.PID())
	if err != nil {
		return NativeProcessEpoch{}, err
	}
	epoch := NativeProcessEpoch{MachineID: string(machine[:len(machine)-1]), BootID: string(boot[:len(boot)-1]), PID: reader.PID(), StartTicks: start}
	for _, item := range []struct {
		self   string
		target *string
	}{{"self/ns/pid", &epoch.PIDNamespace}, {"self/ns/net", &epoch.NetNamespace}} {
		value, err := reader.Link(item.self)
		if err != nil {
			return NativeProcessEpoch{}, ErrNativeStartupUnverified
		}
		*item.target = value
	}
	if ctx.Err() != nil || validateNativeProcessEpoch(epoch) != nil {
		return NativeProcessEpoch{}, ErrNativeStartupUnverified
	}
	return epoch, nil
}

func stableNativeProcessEpoch(ctx context.Context, reader nativeEpochReader) (NativeProcessEpoch, error) {
	before, err := readNativeProcessEpoch(ctx, reader)
	if err != nil {
		return NativeProcessEpoch{}, err
	}
	after, err := readNativeProcessEpoch(ctx, reader)
	if err != nil || after != before || ctx.Err() != nil {
		return NativeProcessEpoch{}, ErrNativeStartupUnverified
	}
	return before, nil
}

func nativeProcessStart(raw []byte, pid int) (uint64, error) {
	if len(raw) == 0 || len(raw) > api.RuntimeUpgradeNativeMetadataMaxBytes || raw[len(raw)-1] != '\n' {
		return 0, ErrNativeStartupUnverified
	}
	text := string(raw[:len(raw)-1])
	prefix := strconv.Itoa(pid) + " ("
	end := strings.LastIndex(text, ") ")
	if !strings.HasPrefix(text, prefix) || end < len(prefix) {
		return 0, ErrNativeStartupUnverified
	}
	fields := strings.Fields(text[end+2:]) // comm may contain spaces, ')' and newlines.
	if len(fields) < 20 || len(fields[0]) != 1 || !strings.Contains("RSDI", fields[0]) {
		return 0, ErrNativeStartupUnverified
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || start == 0 || strconv.FormatUint(start, 10) != fields[19] {
		return 0, ErrNativeStartupUnverified
	}
	return start, nil
}
