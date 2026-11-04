package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// debugPprofTestEnv pretends to be Linux and routes the abstract-socket dial
// to srv, recording which socket the command asked for.
func debugPprofTestEnv(t *testing.T, srv *httptest.Server) *[]string {
	t.Helper()
	oldGOOS, oldDial, oldProc := debugPprofGOOS, debugPprofDial, debugPprofProcNetUnix
	t.Cleanup(func() { debugPprofGOOS, debugPprofDial, debugPprofProcNetUnix = oldGOOS, oldDial, oldProc })
	debugPprofGOOS = "linux"
	var sockets []string
	debugPprofDial = func(ctx context.Context, addr string) (net.Conn, error) {
		sockets = append(sockets, addr)
		var d net.Dialer
		return d.DialContext(ctx, "tcp", strings.TrimPrefix(srv.URL, "http://"))
	}
	return &sockets
}

func TestCmdDebugPprofCapturesAProfile(t *testing.T) {
	out, stderr := obsBundleTestStreams(t)
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write([]byte{0x1f, 0x8b, 'p', 'r', 'o', 'f'})
	}))
	defer srv.Close()
	sockets := debugPprofTestEnv(t, srv)
	dir := t.TempDir()

	file := filepath.Join(dir, "heap.pb.gz")
	if code := cmdDebugDispatch([]string{subDebugPprof, "--daemon", "schedd", "--gc", "-o", file}); code != 0 {
		t.Fatalf("heap: code %d: %s", code, stderr)
	}
	if got.URL.Path != "/debug/pprof/heap" || got.URL.Query().Get("gc") != "1" || got.URL.Query().Get("debug") != "0" || (*sockets)[0] != "faas-debug/schedd" {
		t.Fatalf("request %s on %v", got.URL, *sockets)
	}
	if data, err := os.ReadFile(file); err != nil || string(data[2:]) != "prof" {
		t.Fatalf("profile file: %q %v", data, err)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Errorf("profile mode = %v, want 0600", info.Mode().Perm())
	}
	if !strings.Contains(out.String(), "go tool pprof -http=:0 "+file) {
		t.Errorf("output: %s", out)
	}

	// CPU profile on the second generation's socket.
	if code := cmdDebugPprof([]string{"--daemon", "apid", "--pid", "4242", "--profile", "cpu", "--seconds", "5", "-o", filepath.Join(dir, "cpu.pb.gz")}); code != 0 {
		t.Fatalf("cpu: code %d: %s", code, stderr)
	}
	if got.URL.Path != "/debug/pprof/profile" || got.URL.Query().Get("seconds") != "5" || (*sockets)[1] != "faas-debug/apid.4242" {
		t.Fatalf("cpu request %s on %v", got.URL, *sockets)
	}

	// An existing file is never overwritten.
	if code := cmdDebugPprof([]string{"--daemon", "schedd", "-o", file}); code != 1 || !strings.Contains(stderr.String(), "create") {
		t.Fatalf("overwrite: code %d: %s", code, stderr)
	}
}

func TestCmdDebugPprofReportsDaemonErrors(t *testing.T) {
	_, stderr := obsBundleTestStreams(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "could not start CPU profile: cpu profiling already in use", http.StatusConflict)
	}))
	defer srv.Close()
	debugPprofTestEnv(t, srv)
	code := cmdDebugPprof([]string{"--daemon", "vmmd", "--profile", "cpu", "-o", filepath.Join(t.TempDir(), "p")})
	if code != 1 || !strings.Contains(stderr.String(), "answered 409: could not start CPU profile") {
		t.Fatalf("code %d: %s", code, stderr)
	}
}

func TestCmdDebugPprofRejectsBadInput(t *testing.T) {
	_, stderr := obsBundleTestStreams(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	debugPprofTestEnv(t, srv)
	for _, args := range [][]string{
		{},
		{"--daemon", "a/b"},
		{"--daemon", "schedd", "--profile", "cmdline"},
		{"--daemon", "schedd", "--profile", "cpu", "--seconds", "61"},
		{"--daemon", "schedd", "--debug", "3"},
		{"--daemon", "schedd", "extra"},
	} {
		if code := cmdDebugPprof(args); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
	debugPprofGOOS = "darwin"
	if code := cmdDebugPprof([]string{"--daemon", "schedd"}); code != 2 || !strings.Contains(stderr.String(), "Linux host") {
		t.Errorf("non-Linux: code %d: %s", code, stderr)
	}
	if calls != 0 {
		t.Fatalf("%d requests for invalid input", calls)
	}
}

func TestCmdDebugPprofList(t *testing.T) {
	out, _ := obsBundleTestStreams(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	debugPprofTestEnv(t, srv)
	debugPprofProcNetUnix = filepath.Join(t.TempDir(), "unix")
	table := `Num       RefCount Protocol Flags    Type St Inode Path
0000000000000000: 00000002 00000000 00010000 0001 01 20951 @faas-debug/schedd
0000000000000000: 00000002 00000000 00010000 0001 01 20952 @faas-debug/apid
0000000000000000: 00000002 00000000 00010000 0001 01 20953 @faas-debug/apid.4242
0000000000000000: 00000003 00000000 00000000 0001 03 20954 @faas-debug/schedd
0000000000000000: 00000002 00000000 00010000 0001 01 20955 /run/faas/schedd.sock
`
	if err := os.WriteFile(debugPprofProcNetUnix, []byte(table), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdDebugPprof([]string{"--list"}); code != 0 {
		t.Fatalf("list code %d", code)
	}
	if got, want := out.String(), "apid\napid (--pid 4242)\nschedd\n"; got != want {
		t.Fatalf("list =\n%s\nwant\n%s", got, want)
	}
}

func TestDebugPprofDefaultOutput(t *testing.T) {
	now := time.Date(2026, 10, 23, 20, 15, 0, 0, time.UTC)
	for _, tc := range []struct {
		o    debugPprofOptions
		want string
	}{
		{debugPprofOptions{daemon: "schedd", profile: "heap"}, "schedd-heap-20261023T201500Z.pb.gz"},
		{debugPprofOptions{daemon: "schedd", profile: "goroutine", debug: 2}, "schedd-goroutine-20261023T201500Z.txt"},
		{debugPprofOptions{daemon: "apid", profile: "cpu"}, "apid-cpu-20261023T201500Z.pb.gz"},
		{debugPprofOptions{daemon: "vmmd", profile: "trace"}, "vmmd-trace-20261023T201500Z.trace"},
	} {
		if got := tc.o.defaultOutput(now); got != tc.want {
			t.Errorf("%+v: %s, want %s", tc.o, got, tc.want)
		}
	}
}
