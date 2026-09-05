//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A restored virtio-rng can have fewer bytes available than the requested mix.
// Keep a FIFO writer open to model a device that cannot supply more data yet.
// Run the reader in a subprocess: a regression must time out without leaving a
// goroutine stuck inside a blocking device read in the test process.
func TestMixHWRNGAvailableBytes(t *testing.T) {
	if source := os.Getenv("FAAS_TEST_HWRNG_SOURCE"); source != "" {
		if err := mixHWRNG(source, os.Getenv("FAAS_TEST_HWRNG_DESTINATION")); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, tc := range []struct {
		name string
		size int
	}{
		{"empty", 0},
		{"partial", 33},
		{"full", reseedBytes},
		{"capped", reseedBytes + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "rng")
			destination := filepath.Join(t.TempDir(), "pool")
			if err := unix.Mkfifo(source, 0o600); err != nil {
				t.Fatal(err)
			}
			fd, err := unix.Open(source, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = unix.Close(fd) }()
			input := bytes.Repeat([]byte{0xa5}, tc.size)
			if n, err := unix.Write(fd, input); err != nil || n != len(input) {
				t.Fatalf("fill hardware source: n=%d err=%v", n, err)
			}
			if err := os.WriteFile(destination, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestMixHWRNGAvailableBytes$")
			cmd.Env = append(os.Environ(), "FAAS_TEST_HWRNG_SOURCE="+source, "FAAS_TEST_HWRNG_DESTINATION="+destination)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("optional hardware read must finish without waiting for more bytes: %v (deadline=%v): %s", err, ctx.Err(), out)
			}
			got, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}
			if want := input[:min(len(input), reseedBytes)]; !bytes.Equal(got, want) {
				t.Fatalf("mixed %d bytes, want %d available bytes", len(got), len(want))
			}
		})
	}
}

func TestMixHWRNGDeviceErrors(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "rng")
	destination := filepath.Join(dir, "pool")
	if err := mixHWRNG(source, destination); err != nil {
		t.Fatalf("missing optional hardware: %v", err)
	}
	if err := os.WriteFile(source, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mixHWRNG(source, destination); err != nil {
		t.Fatalf("empty optional hardware: %v", err)
	}
	if err := mixHWRNG(dir, destination); !errors.Is(err, unix.EISDIR) {
		t.Fatalf("hardware read error = %v, want EISDIR", err)
	}
	if err := os.WriteFile(source, []byte{1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mixHWRNG(source, destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pool open error = %v, want ENOENT", err)
	}
	if err := mixHWRNG(source, "/dev/full"); !errors.Is(err, unix.ENOSPC) {
		t.Fatalf("pool write error = %v, want ENOSPC", err)
	}
}
