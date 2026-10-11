// adr: 680
package main

import (
	"bufio"
	"encoding/hex"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStampRestoreReseedEnv(t *testing.T) {
	const dir, sock = "/run/guest-init/rng", "/run/guest-init/reseed.sock"
	require := "--require=/run/guest-init/rng/node-reseed.cjs"
	cases := []struct {
		name       string
		env        []string
		wantNode   string
		wantPython string
		optedOut   bool
	}{
		{name: "empty env", wantNode: require, wantPython: dir + "/python"},
		{
			name:       "customer values are preserved",
			env:        []string{"NODE_OPTIONS=--max-old-space-size=256", "PYTHONPATH=/app/lib"},
			wantNode:   "--max-old-space-size=256 " + require,
			wantPython: dir + "/python" + string(os.PathListSeparator) + "/app/lib",
		},
		{
			name:       "idempotent",
			env:        []string{"NODE_OPTIONS=" + require, "PYTHONPATH=" + dir + "/python"},
			wantNode:   require,
			wantPython: dir + "/python",
		},
		{name: "opt out", env: []string{"GREGALE_RESTORE_RESEED=off", "NODE_OPTIONS=--x"}, optedOut: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stampRestoreReseedEnv(append([]string(nil), tc.env...), dir, sock)
			if tc.optedOut {
				if strings.Join(got, "\n") != strings.Join(tc.env, "\n") {
					t.Fatalf("opted-out env changed: %q", got)
				}
				return
			}
			if v := envValue(got, "NODE_OPTIONS"); v != tc.wantNode {
				t.Errorf("NODE_OPTIONS = %q, want %q", v, tc.wantNode)
			}
			if v := envValue(got, "PYTHONPATH"); v != tc.wantPython {
				t.Errorf("PYTHONPATH = %q, want %q", v, tc.wantPython)
			}
			if v := envValue(got, restoreReseedSocketEnv); v != sock {
				t.Errorf("%s = %q, want %q", restoreReseedSocketEnv, v, sock)
			}
			for _, key := range []string{"NODE_OPTIONS", "PYTHONPATH"} {
				n := 0
				for _, kv := range got {
					if strings.HasPrefix(kv, key+"=") {
						n++
					}
				}
				if n != 1 {
					t.Errorf("%s appears %d times, want 1", key, n)
				}
			}
		})
	}
}

// StampRestoreReseedEnv must not point Node at a preload that was never
// written: a --require of a missing file stops every Node process starting.
func TestStampRestoreReseedEnvNeedsTheServer(t *testing.T) {
	restoreReseedEnabled.Store(false)
	if got := StampRestoreReseedEnv([]string{"A=1"}); len(got) != 1 {
		t.Fatalf("env stamped without a running reseed server: %q", got)
	}
}

// shortSocketDir keeps unix socket paths under macOS's 104-byte limit.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "rr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func startTestBarrier(t *testing.T) (*restoreReseedBarrier, string) {
	t.Helper()
	sock := filepath.Join(shortSocketDir(t), "r.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	b := newRestoreReseedBarrier(nil)
	go b.serve(ln)
	return b, sock
}

func waitRegistered(t *testing.T, b *restoreReseedBarrier, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for b.registered() < want {
		if time.Now().After(deadline) {
			t.Fatalf("registered = %d, want %d", b.registered(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type scriptedClient struct {
	mu     sync.Mutex
	nonces []string
}

// dial registers a fake workload that answers each reseed with reply(n),
// where n counts reseed requests. reply returning "" means stay silent;
// "close" hangs up.
func (s *scriptedClient) dial(t *testing.T, sock, hello string, reply func(n int) string) {
	t.Helper()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := io.WriteString(conn, hello+"\n"); err != nil {
		t.Fatal(err)
	}
	go func() {
		r := bufio.NewReader(conn)
		for n := 1; ; n++ {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			s.mu.Lock()
			s.nonces = append(s.nonces, strings.TrimPrefix(strings.TrimSpace(line), "reseed "))
			s.mu.Unlock()
			switch out := reply(n); out {
			case "":
			case "close":
				_ = conn.Close()
				return
			default:
				_, _ = io.WriteString(conn, out+"\n")
			}
		}
	}()
}

func TestRestoreReseedBarrier(t *testing.T) {
	ok := func(int) string { return "ok" }
	cases := []struct {
		name      string
		replies   []func(int) string
		wantErr   string
		wantAfter int // clients still registered afterwards
	}{
		{name: "no clients"},
		{name: "all confirm", replies: []func(int) string{ok, ok}, wantAfter: 2},
		{name: "one refuses", replies: []func(int) string{ok, func(int) string { return "err openssl_reseed_unavailable" }}, wantErr: "openssl_reseed_unavailable", wantAfter: 2},
		{name: "one stays silent", replies: []func(int) string{ok, func(int) string { return "" }}, wantErr: "1 of 2", wantAfter: 2},
		{name: "exited process is dropped", replies: []func(int) string{ok, func(int) string { return "close" }}, wantAfter: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, sock := startTestBarrier(t)
			for i, reply := range tc.replies {
				(&scriptedClient{}).dial(t, sock, "hello node "+string(rune('1'+i)), reply)
			}
			waitRegistered(t, b, len(tc.replies))
			start := time.Now()
			err := b.Reseed(150 * time.Millisecond)
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("Reseed took %v; the deadline must bound it", elapsed)
			}
			if tc.wantErr == "" && err != nil {
				t.Fatalf("Reseed: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("Reseed err = %v, want it to mention %q", err, tc.wantErr)
			}
			if got := b.registered(); got != tc.wantAfter {
				t.Fatalf("registered after = %d, want %d", got, tc.wantAfter)
			}
		})
	}
}

// production-us hunt #6 (H5-57): a restored Node process faulting its heap
// back answered after the original 250 ms budget on about 1 in 20 concurrent
// restores, and every miss cold-booted the instance. A slow but live process
// must pass the production budget.
func TestRestoreReseedBudgetAdmitsASlowRestoredProcess(t *testing.T) {
	b, sock := startTestBarrier(t)
	slow := func(int) string { time.Sleep(400 * time.Millisecond); return "ok" }
	(&scriptedClient{}).dial(t, sock, "hello node 1", func(int) string { return "ok" })
	(&scriptedClient{}).dial(t, sock, "hello node 2", slow)
	waitRegistered(t, b, 2)
	if err := b.Reseed(RestoreReseedTimeout); err != nil {
		t.Fatalf("Reseed with the production budget: %v", err)
	}
}

func TestRestoreReseedNoncesAreFreshPerProcessAndRound(t *testing.T) {
	b, sock := startTestBarrier(t)
	a, c := &scriptedClient{}, &scriptedClient{}
	ok := func(int) string { return "ok" }
	a.dial(t, sock, "hello node 1", ok)
	c.dial(t, sock, "hello python 2", ok)
	waitRegistered(t, b, 2)
	for round := 0; round < 2; round++ {
		if err := b.Reseed(time.Second); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	seen := map[string]bool{}
	for _, s := range []*scriptedClient{a, c} {
		s.mu.Lock()
		for _, n := range s.nonces {
			raw, err := hex.DecodeString(n)
			if err != nil || len(raw) != restoreReseedNonceSize {
				t.Fatalf("nonce %q is not %d hex bytes", n, restoreReseedNonceSize)
			}
			if seen[n] {
				t.Fatalf("nonce %q was sent twice", n)
			}
			seen[n] = true
		}
		s.mu.Unlock()
	}
	if len(seen) != 4 {
		t.Fatalf("saw %d nonces, want 4", len(seen))
	}
}

func TestRestoreReseedRejectsMalformedHello(t *testing.T) {
	b, sock := startTestBarrier(t)
	for _, hello := range []string{"hi there", "hello node", "hello node -3", "hello node x"} {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(conn, hello+"\n")
		buf := make([]byte, 1)
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Read(buf); err == nil {
			t.Errorf("%q: server wrote to a malformed client", hello)
		}
		_ = conn.Close()
	}
	if got := b.registered(); got != 0 {
		t.Fatalf("registered = %d after malformed hellos, want 0", got)
	}
}
