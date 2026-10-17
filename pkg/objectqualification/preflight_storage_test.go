package objectqualification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

func eligibleInspectionStorage() StorageObservation {
	return StorageObservation{Parent: "/mnt/objects/tests", MountID: 42, MountPoint: "/mnt/objects", MountRoot: "/", Device: "259:2", RootDevice: "259:1", Filesystem: "ext4", FilesystemType: unix.EXT4_SUPER_MAGIC, LocalBlockDevice: true, DirectoryAccessible: true, AvailableBytes: 8 << 30, AvailableInodes: 100_000}
}

func TestPreflightStorageEligibility(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*StorageObservation)
		bad    bool
	}{
		{name: "dedicated ext4"},
		{name: "dedicated XFS", change: func(s *StorageObservation) { s.Filesystem = "xfs"; s.FilesystemType = unix.XFS_SUPER_MAGIC }},
		{name: "root mount", bad: true, change: func(s *StorageObservation) { s.MountPoint = "/" }},
		{name: "whole root bind", bad: true, change: func(s *StorageObservation) { s.Device = s.RootDevice }},
		{name: "subtree bind", bad: true, change: func(s *StorageObservation) { s.MountRoot = "/shared/subtree" }},
		{name: "overlay", bad: true, change: func(s *StorageObservation) { s.Filesystem = "overlay"; s.FilesystemType = unix.OVERLAYFS_SUPER_MAGIC }},
		{name: "NFS", bad: true, change: func(s *StorageObservation) { s.Filesystem = "nfs"; s.LocalBlockDevice = false }},
		{name: "filesystem mismatch", bad: true, change: func(s *StorageObservation) { s.FilesystemType = unix.XFS_SUPER_MAGIC }},
		{name: "no local block device", bad: true, change: func(s *StorageObservation) { s.LocalBlockDevice = false }},
		{name: "read only", bad: true, change: func(s *StorageObservation) { s.ReadOnly = true }},
		{name: "no access", bad: true, change: func(s *StorageObservation) { s.DirectoryAccessible = false }},
		{name: "low disk", bad: true, change: func(s *StorageObservation) { s.AvailableBytes = 1 << 30 }},
		{name: "no inodes", bad: true, change: func(s *StorageObservation) { s.AvailableInodes = 0 }},
		{name: "prefix collision", bad: true, change: func(s *StorageObservation) { s.Parent = "/mnt/objects-other/tests" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := eligibleInspectionStorage()
			if tc.change != nil {
				tc.change(&s)
			}
			if err := validateInspectionStorage(s); (err != nil) != tc.bad {
				t.Fatal(s, err)
			}
		})
	}
}

func TestPreflightMountIdentityAndEncoding(t *testing.T) {
	data := []byte("20 1 259:2 / /mnt/data\\040disk rw,relatime - ext4 /dev/nvme1n1 rw\n21 20 259:3 / /mnt/data\\040disk ro - xfs /dev/nvme2n1 rw\n")
	s, err := findInspectionMount(data, 20)
	if err != nil || s.MountPoint != "/mnt/data disk" || s.ReadOnly || s.Device != "259:2" {
		t.Fatal(s, err)
	}
	s, err = findInspectionMount(data, 21)
	if err != nil || !s.ReadOnly || s.Filesystem != "xfs" {
		t.Fatal("stacked mount selected by path instead of mount ID", s, err)
	}
	for _, bad := range [][]byte{data[:8], append(append([]byte(nil), data...), data...), []byte("20 1 8:1 / /mnt/\\999 rw - ext4 /dev/disk rw\n")} {
		if _, err = findInspectionMount(bad, 20); err == nil {
			t.Fatal("ambiguous mount metadata accepted", string(bad))
		}
	}
	if _, err = findInspectionMount(data, 22); err == nil {
		t.Fatal("missing mount accepted")
	}
	for _, value := range []string{"relative", "/mnt/../other", "/mnt/\\", "/mnt/\\777"} {
		if _, err = unescapeMountPath(value); err == nil {
			t.Fatal(value)
		}
	}
	large := strings.NewReader(strings.Repeat("x", api.ObjectQualificationReportBytes+1))
	if _, err = readInspectionMetadata(large); err == nil {
		t.Fatal("mount metadata budget ignored")
	}
}

func TestPreflightInspectionCreatesNoDataOrCredentials(t *testing.T) {
	root := t.TempDir()
	request := inspectionRequest{Directory: filepath.Join(root, "planned"), Daemon: "/bin/true", Control: "/bin/true", Role: "follower"}
	data, _ := json.Marshal(request)
	var output bytes.Buffer
	if err := ServeInspection(t.Context(), bytes.NewReader(data), &output); err != nil {
		t.Fatal(err)
	}
	var host HostInspection
	if err := json.Unmarshal(output.Bytes(), &host); err != nil || host.Error != "" || validateHostInspection(host, request) != nil || host.Storage.MountID == 0 {
		t.Fatal(host, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("inspection created fixture files", entries, err)
	}
	for _, suffix := range []string{"", "-local", "-replica"} {
		if _, err = os.Stat(request.Directory + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("inspection created a work path", suffix, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = inspectHost(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled inspection proceeded", err)
	}
}

func TestPreflightInspectionRefusesAliasesAndExistingData(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(root, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectStorage(filepath.Join(root, "alias", "new"), "primary"); err == nil {
		t.Fatal("symlink parent accepted")
	}
	path := filepath.Join(root, "run-replica")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectStorage(filepath.Join(root, "run"), "follower"); err == nil {
		t.Fatal("existing remote mode accepted")
	}
	if entries, err := os.ReadDir(path); err != nil || len(entries) != 0 {
		t.Fatal("existing directory changed", entries, err)
	}
	fifo := filepath.Join(root, "fifo")
	if err := unix.Mkfifo(fifo, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectHost(t.Context(), inspectionRequest{Directory: filepath.Join(root, "new"), Daemon: fifo, Control: "/bin/true", Role: "primary"}); err == nil {
		t.Fatal("nonregular executable accepted")
	}
}

func TestPreflightInspectionProtocolBounds(t *testing.T) {
	for _, input := range []string{"", `{"unexpected":true}`, `{} {}`, strings.Repeat(" ", api.ObjectQualificationAgentLineBytes+1)} {
		if err := decodeInspection(strings.NewReader(input), &inspectionRequest{}); err == nil {
			t.Fatal("invalid protocol accepted")
		}
	}
	var output inspectionOutput
	input := strings.Repeat("x", api.ObjectQualificationAgentLineBytes+1024)
	n, err := io.Copy(&output, struct{ io.Reader }{strings.NewReader(input)})
	if err != nil || n != int64(len(input)) || output.buffer.Len() != api.ObjectQualificationAgentLineBytes || !output.overflow {
		t.Fatal("response cap or draining failed", n, output.buffer.Len(), output.overflow, err)
	}
}

func TestPreflightInspectionCancelsBlockedInput(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ServeInspection(ctx, reader, io.Discard) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("inspection ignored canceled stdin")
	}
}
