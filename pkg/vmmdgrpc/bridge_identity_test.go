package vmmdgrpc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/bridgeproc"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
)

// credentialVMM is a VmmdAPI stub that reports bridge credentials.
type credentialVMM struct {
	vmmStubBase
	uid, gid int
	live     bool
}

func (c credentialVMM) BridgeCredential(string) (int, int, bool) { return c.uid, c.gid, c.live }

func TestBridgeIdentityFor(t *testing.T) {
	tests := []struct {
		name     string
		enabled  bool
		vmm      VmmdAPI
		want     bridgeIdentity
		wantCode codes.Code
	}{
		{name: "disabled keeps root bridges", vmm: vmmStubBase{}},
		{name: "enabled", enabled: true, vmm: credentialVMM{uid: fcvm.BridgeUIDBase + 4, gid: fcvm.BridgeUIDBase + 4, live: true}, want: bridgeIdentity{uid: fcvm.BridgeUIDBase + 4, gid: fcvm.BridgeUIDBase + 4}},
		{name: "vmm without credentials fails closed", enabled: true, vmm: vmmStubBase{}, wantCode: codes.FailedPrecondition},
		{name: "instance not live", enabled: true, vmm: credentialVMM{}, wantCode: codes.NotFound},
		{name: "jail uid instead of bridge uid", enabled: true, vmm: credentialVMM{uid: fcvm.JailUIDBase, gid: fcvm.JailUIDBase, live: true}, wantCode: codes.Internal},
		{name: "root", enabled: true, vmm: credentialVMM{live: true}, wantCode: codes.Internal},
		{name: "gid differs", enabled: true, vmm: credentialVMM{uid: fcvm.BridgeUIDBase, gid: 0, live: true}, wantCode: codes.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.enabled {
				t.Setenv(bridgeUnprivilegedEnv, bridgeUnprivilegedValue)
			} else {
				t.Setenv(bridgeUnprivilegedEnv, "")
			}
			got, err := (&Server{vmm: tt.vmm}).bridgeIdentityFor("inst")
			if tt.wantCode != codes.OK {
				if status.Code(err) != tt.wantCode {
					t.Fatalf("bridgeIdentityFor error = %v, want %v", err, tt.wantCode)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("bridgeIdentityFor = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestBridgeIdentityForJailUID(t *testing.T) {
	t.Setenv(bridgeUnprivilegedEnv, "")
	if id, err := bridgeIdentityForJailUID(0); err != nil || id.unprivileged() {
		t.Fatalf("disabled: %+v, %v", id, err)
	}
	t.Setenv(bridgeUnprivilegedEnv, bridgeUnprivilegedValue)
	id, err := bridgeIdentityForJailUID(fcvm.JailUIDBase + 9)
	if err != nil || id != (bridgeIdentity{uid: fcvm.BridgeUIDBase + 9, gid: fcvm.BridgeUIDBase + 9}) {
		t.Fatalf("enabled: %+v, %v", id, err)
	}
	// A network-ready event without a lease uid must not start a root
	// bridge while unprivileged bridges are on.
	if _, err := bridgeIdentityForJailUID(0); err == nil {
		t.Fatal("jail uid 0 produced a bridge identity")
	}
}

func TestBridgeSpawnArgv(t *testing.T) {
	unpriv := bridgeIdentity{uid: fcvm.BridgeUIDBase + 3, gid: fcvm.BridgeUIDBase + 3}
	uid := strconv.Itoa(unpriv.uid)
	if got, want := rawBridgeCommand(context.Background(), unpriv, 4242, "/b/raw", 8080).Args,
		[]string{"nsenter", "--target", "4242", "--net", "--setuid", uid, "--setgid", uid, "--", "/b/raw", netns.GuestIP, "8080"}; !slices.Equal(got, want) {
		t.Errorf("raw argv = %q, want %q", got, want)
	}
	if got, want := streamBridgeArgs("/b/stream", "fc-a", unpriv, "/s", netns.GuestIP, 8080, "D"),
		[]string{"nsenter", "--net=/run/netns/fc-a", "--setuid", uid, "--setgid", uid, "--", "/b/stream", "/s", netns.GuestIP, "8080", "D"}; !slices.Equal(got, want) {
		t.Errorf("unprivileged stream argv = %q, want %q", got, want)
	}
	if got, want := streamBridgeArgs("/b/stream", "fc-a", bridgeIdentity{}, "/s", netns.GuestIP, 8080, "D"),
		[]string{"ip", "netns", "exec", "fc-a", "/b/stream", "/s", netns.GuestIP, "8080", "D"}; !slices.Equal(got, want) {
		t.Errorf("root stream argv = %q, want %q", got, want)
	}
}

// vmmd's environment carries database and storage credentials. No bridge,
// root or not, may inherit it.
func TestBridgeEnvironmentIsMinimal(t *testing.T) {
	t.Setenv("FAAS_COMPUTE_DATABASE_URL", "postgres://secret")
	unpriv := bridgeIdentity{uid: fcvm.BridgeUIDBase, gid: fcvm.BridgeUIDBase}
	for name, env := range map[string][]string{
		"raw root":     rawBridgeCommand(context.Background(), bridgeIdentity{}, 1, "/b", 1).Env,
		"raw unpriv":   rawBridgeCommand(context.Background(), unpriv, 1, "/b", 1).Env,
		"stream extra": unpriv.env("FAAS_BRIDGE_PROTOCOL=h1"),
	} {
		for _, kv := range env {
			if strings.Contains(kv, "secret") {
				t.Errorf("%s: bridge inherited vmmd environment %q", name, kv)
			}
		}
		if !slices.Contains(env, bridgeEnvPATH) {
			t.Errorf("%s: env %q lacks PATH", name, env)
		}
	}
	for _, want := range []string{bridgeproc.ExpectUIDEnv + "=" + strconv.Itoa(unpriv.uid), bridgeproc.ParentPIDEnv + "=" + strconv.Itoa(os.Getpid())} {
		if !slices.Contains(unpriv.env(), want) {
			t.Errorf("unprivileged bridge env lacks %q", want)
		}
	}
	if slices.ContainsFunc(bridgeIdentity{}.env(), func(kv string) bool {
		return strings.HasPrefix(kv, bridgeproc.ExpectUIDEnv) || strings.HasPrefix(kv, bridgeproc.ParentPIDEnv)
	}) {
		t.Error("root bridge got unprivileged-only settings")
	}
}

func TestBridgeSocketDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ibridge")
	saved, savedChown := bridgeRunRoot, chownBridgeDir
	t.Cleanup(func() { bridgeRunRoot, chownBridgeDir = saved, savedChown })
	bridgeRunRoot = root
	var chowned []string
	chownBridgeDir = func(path string, uid, gid int) error {
		chowned = append(chowned, path+":"+strconv.Itoa(uid)+":"+strconv.Itoa(gid))
		return nil
	}

	id := bridgeIdentity{uid: fcvm.BridgeUIDBase + 1, gid: fcvm.BridgeUIDBase + 1}
	socket := streamBridgeSockPath("inst-a", id)
	if want := filepath.Join(root, "inst-a", bridgeSocketName); socket != want {
		t.Fatalf("socket = %q, want %q", socket, want)
	}
	// A stale directory left by an earlier bridge holds a symlink to a file
	// outside it. Preparing must remove the link, not what it points to.
	outside := filepath.Join(t.TempDir(), "keep")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(filepath.Dir(socket), "link")); err != nil {
		t.Fatal(err)
	}
	if err := id.prepareSocketDir(socket); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("prepare followed a stale symlink: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(socket))
	if err != nil || len(entries) != 0 {
		t.Fatalf("socket directory not empty: %v, %v", entries, err)
	}
	for path, want := range map[string]os.FileMode{root: 0o711, filepath.Dir(socket): 0o700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s mode = %v, %v; want %v", path, info.Mode().Perm(), err, want)
		}
	}
	if want := []string{filepath.Dir(socket) + ":" + strconv.Itoa(id.uid) + ":" + strconv.Itoa(id.gid)}; !slices.Equal(chowned, want) {
		t.Fatalf("chown = %q, want %q", chowned, want)
	}
	id.removeSocket(socket)
	if _, err := os.Stat(filepath.Dir(socket)); !os.IsNotExist(err) {
		t.Fatalf("socket directory survived removal: %v", err)
	}

	request := streamBridgeSockPathForRequest("inst-a", id)
	if filepath.Dir(filepath.Dir(request)) != root || filepath.Base(request) != bridgeSocketName || filepath.Dir(request) == filepath.Dir(socket) {
		t.Fatalf("per-request socket %q must have its own directory under %q", request, root)
	}
}

func TestBridgePeerVerification(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("peer credentials are read only on linux")
	}
	dir, err := os.MkdirTemp("", "bridge-peer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
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
			_ = conn.Close()
		}
	}()
	dial := func() net.Conn {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	self := os.Getuid()
	if err := (bridgeIdentity{uid: self, gid: self}).verifyBridgePeer(dial()); self != 0 && err != nil {
		t.Fatalf("own uid rejected: %v", err)
	}
	if err := (bridgeIdentity{uid: self + 1, gid: self + 1}).verifyBridgePeer(dial()); err == nil {
		t.Fatal("a socket served by another uid was accepted")
	}
	if err := (bridgeIdentity{}).verifyBridgePeer(dial()); err != nil {
		t.Fatalf("root bridge peer check: %v", err)
	}
}
