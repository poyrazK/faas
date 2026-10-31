package daemonunit

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// shortTempDir keeps socket paths under macOS's 104-byte limit.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "fds-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestListenFDNamesParsesTheActivationEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     map[string]string
		want    []string
		wantErr bool
	}{
		{name: "none", env: map[string]string{}},
		{name: "another process's", env: map[string]string{"LISTEN_PID": "1", "LISTEN_FDS": "2"}},
		{name: "named", env: map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "2", "LISTEN_FDNAMES": "gvsock-a-1026:gvsock-b-1026"},
			want: []string{"gvsock-a-1026", "gvsock-b-1026"}},
		{name: "unnamed", env: map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "1"}, want: []string{"unknown"}},
		{name: "name count differs", env: map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "2", "LISTEN_FDNAMES": "a"}, wantErr: true},
		{name: "bad count", env: map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "x"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := listenFDNames(func(k string) string { return tc.env[k] }, 42)
			if (err != nil) != tc.wantErr || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("listenFDNames = %v, %v; want %v, err %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

// Store sends the listener's descriptor with its name; Remove names it
// without one; invalid names are refused before anything is sent.
func TestFDStoreSendsDescriptorsToSystemd(t *testing.T) {
	path := fmt.Sprintf("/tmp/daemonunit-fdstore-%d.sock", time.Now().UnixNano())
	t.Cleanup(func() { _ = os.Remove(path) })
	notify, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = notify.Close() })
	lnPath := filepath.Join(shortTempDir(t), "v.sock_1026")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: lnPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	s := &FDStore{socket: path, passed: map[string]*net.UnixListener{}}
	if err := s.Store("gvsock-job-1-1026", ln); err != nil {
		t.Fatal(err)
	}
	buf, oob := make([]byte, 128), make([]byte, syscall.CmsgSpace(4))
	_ = notify.SetReadDeadline(time.Now().Add(time.Second))
	n, oobn, _, _, err := notify.ReadMsgUnix(buf, oob)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "FDSTORE=1\nFDNAME=gvsock-job-1-1026" {
		t.Fatalf("state = %q", got)
	}
	msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(msgs) != 1 {
		t.Fatalf("control messages = %v, %v", msgs, err)
	}
	fds, err := syscall.ParseUnixRights(&msgs[0])
	if err != nil || len(fds) != 1 {
		t.Fatalf("rights = %v, %v", fds, err)
	}
	// The received descriptor is the same listening socket.
	f := os.NewFile(uintptr(fds[0]), "stored")
	got, err := net.FileListener(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got.Addr().String() != lnPath {
		t.Fatalf("stored listener is at %s, want %s", got.Addr(), lnPath)
	}
	_ = got.Close()

	if err := s.Remove("gvsock-job-1-1026"); err != nil {
		t.Fatal(err)
	}
	n, oobn, _, _, err = notify.ReadMsgUnix(buf, oob)
	if err != nil || string(buf[:n]) != "FDSTOREREMOVE=1\nFDNAME=gvsock-job-1-1026" || oobn != 0 {
		t.Fatalf("remove = %q (%d control bytes), %v", buf[:n], oobn, err)
	}
	for _, bad := range []string{"", "a:b", "a b", strings.Repeat("x", 256)} {
		if err := s.Store(bad, ln); err == nil {
			t.Fatalf("name %q accepted", bad)
		}
	}
}

func TestFDStoreWithoutSystemdIsANoop(t *testing.T) {
	s := &FDStore{passed: map[string]*net.UnixListener{}}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(shortTempDir(t), "s"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	if err := s.Store("gvsock-x-1026", ln); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("gvsock-x-1026"); err != nil {
		t.Fatal(err)
	}
	if got := s.Take("gvsock-x-1026"); got != nil {
		t.Fatal("a listener came back that nobody stored")
	}
	var none *FDStore
	if none.Take("x") != nil || none.Untaken() != nil {
		t.Fatal("nil store is not empty")
	}
}
