// adr: 1027
package dbvm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

func validSpec() Spec {
	return Spec{
		KernelPath: "/srv/fc/kernel/1.7.0",
		BasePath:   "/srv/fc/base/postgres-17.ext4",
		VolumePath: "/srv/db/volumes/db-1.img",
		Major:      17,
		VcpuCount:  1,
		MemSizeMiB: 384,
		GuestCID:   0x2000,
		VsockUDS:   "/run/db/db-1.vsock",
	}
}

// TestBuildConfigIsNetworklessAndDurable pins the database VM shape of
// ADR-1027: no network interface, a read-only root base, and a volume whose
// guest flushes reach the host.
func TestBuildConfigIsNetworklessAndDurable(t *testing.T) {
	cfg, err := BuildConfig(validSpec())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.NetworkInterfaces) != 0 {
		t.Fatalf("database VM has network interfaces: %+v", cfg.NetworkInterfaces)
	}
	if len(cfg.Drives) != 2 {
		t.Fatalf("drives = %+v", cfg.Drives)
	}
	base, volume := cfg.Drives[0], cfg.Drives[1]
	if base.DriveID != DriveBase || !base.IsRootDevice || !base.IsReadOnly || base.CacheType != "" {
		t.Fatalf("base drive = %+v", base)
	}
	if volume.DriveID != DriveVolume || volume.IsRootDevice || volume.IsReadOnly || volume.CacheType != fcvm.DriveCacheWriteback {
		t.Fatalf("volume drive = %+v", volume)
	}
	if cfg.Entropy == nil || cfg.VsockDevice == nil || cfg.VsockDevice.GuestCID != 0x2000 {
		t.Fatalf("entropy/vsock missing: %+v %+v", cfg.Entropy, cfg.VsockDevice)
	}
	args := cfg.BootSource.BootArgs
	if !strings.Contains(args, dbguest.ModeArg) || !strings.Contains(args, dbguest.MajorArgKey+"=17") {
		t.Fatalf("boot args lack database mode: %q", args)
	}
	if strings.Contains(args, "ip=") {
		t.Fatalf("database VM boot args configure networking: %q", args)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "network-interfaces") {
		t.Fatalf("Firecracker config carries network-interfaces: %s", raw)
	}
	if !strings.Contains(string(raw), `"cache_type":"Writeback"`) {
		t.Fatalf("Firecracker config lacks write-back volume caching: %s", raw)
	}
}

func TestBuildConfigRejectsInvalidSpecs(t *testing.T) {
	cases := map[string]func(*Spec){
		"no volume":         func(s *Spec) { s.VolumePath = "" },
		"no vsock":          func(s *Spec) { s.VsockUDS = "" },
		"zero vcpus":        func(s *Spec) { s.VcpuCount = 0 },
		"too little memory": func(s *Spec) { s.MemSizeMiB = 64 },
		"reserved cid":      func(s *Spec) { s.GuestCID = 2 },
		"unsupported major": func(s *Spec) { s.Major = 12 },
		"restore without archive": func(s *Spec) {
			s.RestoreTarget = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := validSpec()
			mutate(&s)
			if _, err := BuildConfig(s); err == nil {
				t.Fatal("BuildConfig accepted an invalid spec")
			}
		})
	}
}

// TestBuildConfigRestoreBoot pins the restore boot: the guest reads the
// target from the kernel command line and needs archiving to fetch WAL.
func TestBuildConfigRestoreBoot(t *testing.T) {
	s := validSpec()
	s.Archive = true
	s.RestoreTarget = time.Date(2026, 10, 13, 9, 30, 0, 500, time.FixedZone("x", 3600))
	cfg, err := BuildConfig(s)
	if err != nil {
		t.Fatal(err)
	}
	target, ok, err := dbguest.RestoreTarget(cfg.BootSource.BootArgs)
	if err != nil || !ok || !target.Equal(s.RestoreTarget) || !dbguest.ArchiveEnabled(cfg.BootSource.BootArgs) {
		t.Fatalf("boot args %q: target %v %v %v", cfg.BootSource.BootArgs, target, ok, err)
	}
	if dbguest.RestoreUntilEnd(cfg.BootSource.BootArgs) {
		t.Fatal("a plain restore may end early")
	}
	s.RestoreUntilEnd = true
	if cfg, err := BuildConfig(s); err != nil || !dbguest.RestoreUntilEnd(cfg.BootSource.BootArgs) {
		t.Fatalf("until-end restore boot args = %q, %v", cfg.BootSource.BootArgs, err)
	}
	s.RestoreTarget = time.Time{}
	if _, err := BuildConfig(s); err == nil {
		t.Fatal("until-end without a restore accepted")
	}
	s.RestoreUntilEnd = false
	if cfg, _ := BuildConfig(s); strings.Contains(cfg.BootSource.BootArgs, dbguest.RestoreTargetArgKey) {
		t.Fatalf("plain boot carries a restore target: %q", cfg.BootSource.BootArgs)
	}
}

func TestAppDrivesKeepDefaultCacheType(t *testing.T) {
	raw, err := json.Marshal(fcvm.Drive{DriveID: "layer", PathOnHost: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "cache_type") {
		t.Fatalf("app drive JSON changed: %s", raw)
	}
}

func TestCreateVolumeRejectsOutOfRangeSizes(t *testing.T) {
	dir := t.TempDir()
	for _, size := range []int64{0, MinVolumeBytes - 1, MaxVolumeBytes + 1} {
		if err := CreateVolume(context.Background(), filepath.Join(dir, "v.img"), size); err == nil {
			t.Fatalf("CreateVolume accepted size %d", size)
		}
	}
}

// fakeFirecracker serves Firecracker's host vsock protocol on a unix socket
// and hands each accepted guest stream to handle.
func fakeFirecracker(t *testing.T, handle func(port string, conn net.Conn)) string {
	t.Helper()
	dir, err := shortTempDir(t)
	if err != nil {
		t.Fatal(err)
	}
	uds := filepath.Join(dir, "v.sock")
	ln, err := net.Listen("unix", uds)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				line, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil || !strings.HasPrefix(line, "CONNECT ") {
					return
				}
				port := strings.TrimSpace(strings.TrimPrefix(line, "CONNECT "))
				_, _ = conn.Write([]byte("OK 1073741824\n"))
				handle(port, conn)
			}()
		}
	}()
	return uds
}

func TestControlRoundTrip(t *testing.T) {
	uds := fakeFirecracker(t, func(port string, conn net.Conn) {
		if port != "5433" {
			return
		}
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			return
		}
		var req dbguest.Request
		if json.Unmarshal(line, &req) != nil || req.Op != dbguest.OpStatus {
			return
		}
		_ = json.NewEncoder(conn).Encode(dbguest.Response{State: dbguest.StateReady, Major: 17})
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := WaitState(ctx, uds, dbguest.StateReady)
	if err != nil || resp.Major != 17 {
		t.Fatalf("WaitState = %+v, %v", resp, err)
	}
}

func TestControlRejectsInvalidRequestLocally(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := Control(ctx, "/nonexistent", dbguest.Request{Op: dbguest.OpAdminSQL}); err == nil || !strings.Contains(err.Error(), "admin_sql") {
		t.Fatalf("admin_sql without SQL = %v; want a local validation error", err)
	}
}

func TestWaitStateSurfacesGuestFailure(t *testing.T) {
	uds := fakeFirecracker(t, func(_ string, conn net.Conn) {
		if _, err := bufio.NewReader(conn).ReadBytes('\n'); err == nil {
			_ = json.NewEncoder(conn).Encode(dbguest.Response{State: dbguest.StateFailed, Error: dbguest.ErrCodeMajorMismatch})
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := WaitState(ctx, uds, dbguest.StateReady); err == nil || !strings.Contains(err.Error(), dbguest.ErrCodeMajorMismatch) {
		t.Fatalf("WaitState error = %v", err)
	}
}

func TestDialRejectedConnect(t *testing.T) {
	dir, err := shortTempDir(t)
	if err != nil {
		t.Fatal(err)
	}
	uds := filepath.Join(dir, "v.sock")
	ln, err := net.Listen("unix", uds)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = bufio.NewReader(conn).ReadString('\n')
			_ = conn.Close() // Firecracker closes when no guest listener exists.
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := Dial(ctx, uds, dbguest.DataPort); err == nil {
		t.Fatal("Dial succeeded without a guest listener")
	}
}

func TestFilesystemBytes(t *testing.T) {
	out := []byte("Filesystem volume name:   gregale-db\nBlock count:              262144\nReserved block count:     0\nBlock size:               4096\n")
	if n, err := FilesystemBytes(out); err != nil || n != 1<<30 {
		t.Fatalf("filesystem bytes = %d, %v", n, err)
	}
	if _, err := FilesystemBytes([]byte("dumpe2fs: Bad magic number in super-block")); err == nil {
		t.Fatal("garbage parsed")
	}
}

// A hostile guest answering without end cannot make the host buffer
// without end: the reply is cut off at MaxResponseBytes.
func TestControlBoundsAGuestsReply(t *testing.T) {
	sent := make(chan int64, 1)
	uds := fakeFirecracker(t, func(port string, conn net.Conn) {
		if port != "5433" {
			return
		}
		_, _ = bufio.NewReader(conn).ReadBytes('\n')
		chunk := bytes.Repeat([]byte("x"), 32<<10)
		var n int64
		for n < 64<<20 {
			w, err := conn.Write(chunk)
			n += int64(w)
			if err != nil {
				break
			}
		}
		sent <- n
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := Control(ctx, uds, dbguest.Request{Op: dbguest.OpStatus}); err == nil {
		t.Fatal("an endless reply decoded")
	}
	select {
	case n := <-sent:
		if n >= 64<<20 {
			t.Fatalf("the host read all %d bytes of an endless reply", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the guest is still writing: the host never stopped reading")
	}
}
