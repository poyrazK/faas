//go:build unix

// Socket activation (docs/adr/20261018074719-zero-downtime-daemon-restarts.md):
// a systemd-owned socket must carry connections across a daemon restart.

package wire_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// masterUnixSocket stands in for the socket systemd keeps open: a bound,
// listening unix socket whose descriptor outlives every server using it.
func masterUnixSocket(t *testing.T, mode os.FileMode) (string, *os.File) {
	t.Helper()
	dir, err := os.MkdirTemp("", "wa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "d.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	master, err := l.File()
	_ = l.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close() })
	return path, master
}

// dupFD hands one process generation its own copy of the master socket,
// which the activation set closes once the listener is consumed.
func dupFD(t *testing.T, f *os.File) int {
	t.Helper()
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	return fd
}

func TestListenOrRecreateKeepsInheritedSocketAcrossRestart(t *testing.T) {
	path, master := masterUnixSocket(t, 0o660)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	restore := wire.UseInheritedListenersForTest([]int{dupFD(t, master)}, []string{"daemon"})
	first, err := wire.ListenOrRecreate(path, 0, 0, 0o660)
	restore()
	if err != nil {
		t.Fatalf("first generation: %v", err)
	}
	_ = first.Close() // the old process has drained and exits
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("closing an inherited listener unlinked the socket path: %v", err)
	}

	client, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("connection during the restart gap was refused: %v", err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintln(client, "queued"); err != nil {
		t.Fatal(err)
	}

	restore = wire.UseInheritedListenersForTest([]int{dupFD(t, master)}, []string{"daemon"})
	defer restore()
	second, err := wire.ListenOrRecreate(path, 0, 0, 0o660)
	if err != nil {
		t.Fatalf("replacement generation: %v", err)
	}
	defer second.Close()
	go func() {
		conn, err := second.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if line, _ := bufio.NewReader(conn).ReadString('\n'); line == "queued\n" {
			_, _ = fmt.Fprintln(conn, "served")
		}
	}()
	if line, err := bufio.NewReader(client).ReadString('\n'); err != nil || line != "served\n" {
		t.Fatalf("queued connection response = %q, %v", line, err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("socket path was replaced across the restart (err=%v)", err)
	}
}

func TestListenAsClaimsInheritedTCPSocket(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	master, err := base.(*net.TCPListener).File()
	addr := base.Addr().String()
	_ = base.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()

	restore := wire.UseInheritedListenersForTest([]int{dupFD(t, master)}, []string{"grpc"})
	defer restore()
	l, err := wire.ListenAs(context.Background(), "tcp://"+addr, &tls.Config{}, "")
	if err != nil {
		t.Fatalf("ListenAs: %v", err)
	}
	defer l.Close()
	if l.Addr().String() != addr {
		t.Fatalf("listener = %s, want inherited %s", l.Addr(), addr)
	}
	if err := wire.RequireInheritedListenersClaimed("test"); err != nil {
		t.Fatalf("claimed socket reported unclaimed: %v", err)
	}
}

func TestInheritedListenerMatching(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	master, err := base.(*net.TCPListener).File()
	port := base.Addr().(*net.TCPAddr).Port
	_ = base.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()

	tests := []struct {
		name    string
		network string
		address string
		want    bool
	}{
		{"exact", "tcp", fmt.Sprintf("127.0.0.1:%d", port), true},
		{"wildcard config accepts a narrower unit", "tcp", fmt.Sprintf("0.0.0.0:%d", port), true},
		{"other port", "tcp", fmt.Sprintf("127.0.0.1:%d", port+1), false},
		{"other concrete address", "tcp", fmt.Sprintf("127.0.0.2:%d", port), false},
		{"unix request never takes tcp", "unix", "/run/faas/x.sock", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore := wire.UseInheritedListenersForTest([]int{dupFD(t, master)}, nil)
			defer restore()
			l, ok, err := wire.InheritedListener(tt.network, tt.address)
			if err != nil {
				t.Fatal(err)
			}
			if ok != tt.want {
				t.Fatalf("claimed = %v, want %v", ok, tt.want)
			}
			if l != nil {
				_ = l.Close()
			}
		})
	}
}

func TestInheritedListenerRefusesWorldAccessibleUnixSocket(t *testing.T) {
	path, master := masterUnixSocket(t, 0o666)
	restore := wire.UseInheritedListenersForTest([]int{dupFD(t, master)}, nil)
	defer restore()
	_, err := wire.ListenOrRecreate(path, 0, 0, 0o660)
	if err == nil || !strings.Contains(err.Error(), "SocketMode=0660") {
		t.Fatalf("err = %v, want SocketMode rejection", err)
	}
}

func TestInheritedListenerRejectsSecondClaim(t *testing.T) {
	path, master := masterUnixSocket(t, 0o660)
	restore := wire.UseInheritedListenersForTest([]int{dupFD(t, master)}, nil)
	defer restore()
	first, err := wire.ListenOrRecreate(path, 0, 0, 0o660)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := wire.ListenOrRecreate(path, 0, 0, 0o660); err == nil || !strings.Contains(err.Error(), "already claimed") {
		t.Fatalf("second claim err = %v, want already claimed", err)
	}
}

func TestRequireInheritedListenersClaimedNamesStraySocket(t *testing.T) {
	path, master := masterUnixSocket(t, 0o660)
	restore := wire.UseInheritedListenersForTest([]int{dupFD(t, master)}, []string{"stray"})
	defer restore()
	err := wire.RequireInheritedListenersClaimed("schedd")
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), `"stray"`) {
		t.Fatalf("err = %v, want the unclaimed socket named", err)
	}
	if got := wire.UnclaimedInheritedListeners(); len(got) != 1 {
		t.Fatalf("unclaimed = %v, want one", got)
	}
}

func TestLoadActivationEnvironment(t *testing.T) {
	inspect := func(fd int) (string, string, error) {
		return "unix", fmt.Sprintf("/run/faas/%d.sock", fd), nil
	}
	tests := []struct {
		name    string
		env     map[string]string
		want    []string
		wantErr string
	}{
		{name: "no activation", env: map[string]string{}},
		{name: "ancestor's descriptors", env: map[string]string{"LISTEN_PID": "1", "LISTEN_FDS": "2"}},
		{name: "incomplete", env: map[string]string{"LISTEN_PID": "42"}, wantErr: "incomplete"},
		{name: "bad count", env: map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "x"}, wantErr: "LISTEN_FDS"},
		{
			name: "named descriptors from fd 3",
			env:  map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "2", "LISTEN_FDNAMES": "internal:data"},
			want: []string{`unix /run/faas/3.sock (fd 3, name "internal")`, `unix /run/faas/4.sock (fd 4, name "data")`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := wire.LoadActivationForTest(tt.env, 42, inspect)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Fatalf("sockets = %v, want %v", got, tt.want)
			}
		})
	}
}

const (
	activationHelperEnv     = "WIRE_ACTIVATION_HELPER"
	activationSocketEnv     = "WIRE_ACTIVATION_SOCKET"
	activationGenerationEnv = "WIRE_ACTIVATION_GENERATION"
)

// TestActivationHelperProcess is one daemon generation started the way
// systemd starts a socket-activated service: the socket arrives as fd 3 with
// LISTEN_FDS. It serves until SIGTERM, then drains and exits.
func TestActivationHelperProcess(t *testing.T) {
	if os.Getenv(activationHelperEnv) != "1" {
		t.Skip("helper process")
	}
	// systemd sets LISTEN_PID between fork and exec; the parent cannot.
	_ = os.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	l, err := wire.ListenOrRecreate(os.Getenv(activationSocketEnv), 0, 0, 0o660)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(2)
	}
	if err := wire.RequireInheritedListenersClaimed("helper"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	generation := os.Getenv(activationGenerationEnv)
	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			time.Sleep(5 * time.Millisecond) // keep requests in flight across SIGTERM
			_, _ = io.WriteString(w, generation)
		}),
	}
	if err := wire.ServeUntilShutdown(ctx, srv, l, 10*time.Second); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

// TestUnixDaemonRestartUnderLoadDropsNothing restarts a socket-activated
// daemon process while clients keep sending requests. The old generation
// drains what it accepted; everything that arrives while no process is
// running waits in the inherited socket's backlog for the replacement.
func TestUnixDaemonRestartUnderLoadDropsNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns helper processes")
	}
	path, master := masterUnixSocket(t, 0o660)
	start := func(generation int) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestActivationHelperProcess$")
		cmd.Env = append(os.Environ(),
			activationHelperEnv+"=1",
			activationSocketEnv+"="+path,
			activationGenerationEnv+"="+strconv.Itoa(generation),
			"LISTEN_FDS=1",
			"LISTEN_FDNAMES=helper",
		)
		cmd.ExtraFiles = []*os.File{master} // fd 3
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", path)
			},
		},
	}
	var (
		served   [3]atomic.Int64
		failures atomic.Int64
		firstErr atomic.Value
		stop     = make(chan struct{})
		wg       sync.WaitGroup
	)
	old := start(1)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				resp, err := client.Post("http://daemon/work", "text/plain", strings.NewReader("payload"))
				if err != nil {
					failures.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
					continue
				}
				body, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				generation, convErr := strconv.Atoi(string(body))
				if err != nil || resp.StatusCode != http.StatusOK || convErr != nil || generation < 1 || generation > 2 {
					failures.Add(1)
					firstErr.CompareAndSwap(nil, fmt.Sprintf("status=%d body=%q err=%v", resp.StatusCode, body, err))
					continue
				}
				served[generation].Add(1)
			}
		}()
	}
	waitFor(t, func() bool { return served[1].Load() > 50 })

	if err := old.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := old.Wait(); err != nil {
		t.Fatalf("old generation did not drain cleanly: %v", err)
	}
	// The replacement's startup time: no process holds the socket now.
	time.Sleep(300 * time.Millisecond)
	replacement := start(2)
	defer func() {
		_ = replacement.Process.Signal(syscall.SIGTERM)
		_ = replacement.Wait()
	}()
	waitFor(t, func() bool { return served[2].Load() > 50 })
	close(stop)
	wg.Wait()

	if n := failures.Load(); n != 0 {
		t.Fatalf("%d requests failed across the restart; first: %v", n, firstErr.Load())
	}
	t.Logf("generation 1 served %d, generation 2 served %d", served[1].Load(), served[2].Load())
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(errors.New("condition not reached"))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
