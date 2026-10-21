//go:build unix

// Two-generation hand-off (docs/adr/1736-zero-downtime-daemon-restarts.md).

package wire_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

const (
	handoffHelperEnv      = "WIRE_HANDOFF_HELPER"
	handoffAddrEnv        = "WIRE_HANDOFF_ADDR"
	handoffFailNextEnv    = "WIRE_HANDOFF_FAIL_SUCCESSOR"
	handoffLongRequestEnv = "WIRE_HANDOFF_LONG"
	// Pre-traffic checks (docs/adr/20261020214632-automatic-unit-deploy-
	// safety-rails.md): the old generation checks GET /check; a successor
	// answers it after CHECK_DELAY, with 503 when BAD_CHECK, and records
	// when it answered in CHECK_DONE; OLD_SUCCESSOR makes the successor act
	// like a release before checks; ERR_FILE receives a failed hand-off.
	handoffCheckEnv        = "WIRE_HANDOFF_CHECK"
	handoffBadCheckEnv     = "WIRE_HANDOFF_BAD_CHECK"
	handoffCheckDelayEnv   = "WIRE_HANDOFF_CHECK_DELAY"
	handoffCheckDoneEnv    = "WIRE_HANDOFF_CHECK_DONE"
	handoffOldSuccessorEnv = "WIRE_HANDOFF_OLD_SUCCESSOR"
	handoffErrFileEnv      = "WIRE_HANDOFF_ERR_FILE"
)

// TestHandoffHelperProcess is one daemon generation. It serves /fast and
// /long, hands off on SIGUSR2 and drains as the old generation, or drains
// with the restart grace on SIGTERM.
func TestHandoffHelperProcess(t *testing.T) {
	if os.Getenv(handoffHelperEnv) != "1" {
		t.Skip("helper process")
	}
	if wire.IsHandoffSuccessor() && os.Getenv(handoffFailNextEnv) == "1" {
		os.Exit(7) // a successor that never becomes ready
	}
	if wire.IsHandoffSuccessor() && os.Getenv(handoffOldSuccessorEnv) == "1" {
		// A release before pre-traffic checks ignores what they pass.
		_ = os.Unsetenv("FAAS_HANDOFF_CHECK_FDS")
		_ = os.Unsetenv("FAAS_HANDOFF_GO_FD")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	l, err := wire.ListenInherited("tcp", os.Getenv(handoffAddrEnv))
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		os.Exit(2)
	}
	long, _ := time.ParseDuration(os.Getenv(handoffLongRequestEnv))
	checkDelay, _ := time.ParseDuration(os.Getenv(handoffCheckDelayEnv))
	pid := strconv.Itoa(os.Getpid())
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/check" {
			time.Sleep(checkDelay)
			if wire.IsHandoffSuccessor() && os.Getenv(handoffBadCheckEnv) == "1" {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			if done := os.Getenv(handoffCheckDoneEnv); done != "" && wire.IsHandoffSuccessor() {
				_ = os.WriteFile(done, []byte(strconv.FormatInt(time.Now().UnixNano(), 10)), 0o600)
			}
			_, _ = io.WriteString(w, pid)
			return
		}
		if r.URL.Path == "/long" {
			select {
			case <-time.After(long):
			case <-r.Context().Done():
				return
			}
		}
		_, _ = io.WriteString(w, pid)
	})}
	drain := wire.NewHTTPDrain(server)
	go func() { _ = server.Serve(drain.Listener(l)) }()
	fmt.Fprintf(os.Stderr, "helper %s: serving (successor=%v)\n", pid, wire.IsHandoffSuccessor())
	defer wire.NotifyReadyWhen(ctx, func() bool { return true })()

	requests := wire.HandoffRequests(ctx)
	for {
		select {
		case <-requests:
			// This fixture exercises socket transfer and draining. Model its
			// isolated daemon cgroup instead of measuring the shared test runner.
			opts := wire.HandoffOptionsWithMemoryCheckForTest(func() error { return nil })
			if os.Getenv(handoffCheckEnv) == "1" {
				opts.Checks = []wire.HandoffCheck{{Name: "check", Network: "tcp", Address: os.Getenv(handoffAddrEnv),
					Path: "/check", Want: []int{http.StatusOK}}}
			}
			if _, err := wire.Handoff(ctx, opts); err != nil {
				fmt.Fprintln(os.Stderr, "hand-off failed; still serving:", err)
				if file := os.Getenv(handoffErrFileEnv); file != "" {
					_ = os.WriteFile(file, []byte(err.Error()), 0o600)
				}
				continue
			}
			// Old generation: stop accepting, finish what is in flight.
			fmt.Fprintf(os.Stderr, "helper %s: handed off; draining\n", pid)
			drainCtx, cancel := context.WithTimeout(context.Background(), 2*long+10*time.Second)
			err := drain.Shutdown(drainCtx, server)
			cancel()
			fmt.Fprintf(os.Stderr, "helper %s: drained (%v)\n", pid, err)
			os.Exit(0)
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "helper %s: stopping\n", pid)
			drainCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			err := drain.Shutdown(drainCtx, server)
			cancel()
			fmt.Fprintf(os.Stderr, "helper %s: stopped (%v)\n", pid, err)
			os.Exit(0)
		}
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func startHandoffDaemon(t *testing.T, addr string, long time.Duration, extraEnv ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHandoffHelperProcess$")
	cmd.Env = append(os.Environ(), handoffHelperEnv+"=1", handoffAddrEnv+"="+addr, handoffLongRequestEnv+"="+long.String())
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	})
	return cmd
}

func get(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url) //nolint:noctx // test client
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	return string(body), nil
}

// TestHandoffKeepsLongRequestsAndDropsNothing hands a daemon off under load
// while requests longer than the 25 s restart grace are in flight. The old
// generation finishes them; new requests reach the new generation; nothing
// fails.
func TestHandoffKeepsLongRequestsAndDropsNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("runs requests longer than the restart grace")
	}
	addr := freeAddr(t)
	const long = 30 * time.Second
	old := startHandoffDaemon(t, addr, long)
	oldPID := strconv.Itoa(old.Process.Pid)
	client := &http.Client{Timeout: 2 * time.Minute, Transport: &http.Transport{DisableKeepAlives: true}}
	waitFor(t, func() bool { _, err := get(client, "http://"+addr+"/fast"); return err == nil })

	var (
		byPID    sync.Map
		failures atomic.Int64
		firstErr atomic.Value
		stop     = make(chan struct{})
		wg       sync.WaitGroup
	)
	count := func(pid string) {
		v, _ := byPID.LoadOrStore(pid, new(atomic.Int64))
		v.(*atomic.Int64).Add(1)
	}
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				case <-time.After(20 * time.Millisecond): // stay within macOS's ephemeral ports
				}
				pid, err := get(client, "http://"+addr+"/fast")
				if err != nil {
					failures.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
					continue
				}
				count(pid)
			}
		})
	}
	longResults := make(chan string, 3)
	for range 3 {
		go func() {
			pid, err := get(client, "http://"+addr+"/long")
			if err != nil {
				pid = "error: " + err.Error()
			}
			longResults <- pid
		}()
	}
	time.Sleep(time.Second) // the long requests are in flight on the old generation

	if err := old.Process.Signal(syscall.SIGUSR2); err != nil {
		t.Fatal(err)
	}
	var newPID string
	waitFor(t, func() bool {
		found := false
		byPID.Range(func(k, _ any) bool {
			if k.(string) != oldPID {
				newPID, found = k.(string), true
			}
			return !found
		})
		return found
	})
	t.Cleanup(func() {
		if pid, err := strconv.Atoi(newPID); err == nil {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	})
	// systemd waits for and reaps only its own children: the successor
	// must not be the old generation's (handoff_spawn.go).
	if out, err := exec.Command("ps", "-o", "ppid=", "-p", newPID).Output(); err == nil && strings.TrimSpace(string(out)) == oldPID {
		t.Fatalf("successor %s is the old generation's child", newPID)
	}

	for range 3 {
		select {
		case got := <-longResults:
			if got != oldPID {
				t.Errorf("long request = %q, want it finished by the old generation %s", got, oldPID)
			}
		case <-time.After(long + 30*time.Second):
			t.Fatal("long request did not finish")
		}
	}
	exited := make(chan error, 1)
	go func() { exited <- old.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("old generation exited with %v, want a clean drain", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("old generation still running after its requests finished")
	}
	// After the old generation is gone, everything reaches the new one.
	before := int64(0)
	if v, ok := byPID.Load(newPID); ok {
		before = v.(*atomic.Int64).Load()
	}
	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
	if v, ok := byPID.Load(newPID); !ok || v.(*atomic.Int64).Load() <= before {
		t.Fatalf("new generation %s served nothing after the hand-off", newPID)
	}
	if n := failures.Load(); n != 0 {
		t.Fatalf("%d requests failed across the hand-off; first: %v", n, firstErr.Load())
	}
}

// TestHandoffFailureKeepsTheOldGenerationServing: a successor that never
// becomes ready is stopped and the running daemon keeps every request.
func TestHandoffFailureKeepsTheOldGenerationServing(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns helper processes")
	}
	addr := freeAddr(t)
	old := startHandoffDaemon(t, addr, time.Second, handoffFailNextEnv+"=1")
	oldPID := strconv.Itoa(old.Process.Pid)
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	waitFor(t, func() bool { _, err := get(client, "http://"+addr+"/fast"); return err == nil })

	if err := old.Process.Signal(syscall.SIGUSR2); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pid, err := get(client, "http://"+addr+"/fast")
		if err != nil {
			t.Fatalf("request failed while the hand-off failed: %v", err)
		}
		if pid != oldPID {
			t.Fatalf("request served by %s, want the old generation %s", pid, oldPID)
		}
	}
	if err := old.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("old generation exited after a failed hand-off: %v", err)
	}
}

func TestHandoffEnvironmentEndsTheParentsActivation(t *testing.T) {
	sockets, err := wire.LoadActivationForTest(map[string]string{
		"LISTEN_PID": "1", "LISTEN_FDS": "1", "FAAS_HANDOFF_FDS": "2",
	}, 42, func(fd int) (string, string, error) {
		return "tcp", fmt.Sprintf("127.0.0.1:%d", 8000+fd), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sockets, "|"); !strings.Contains(got, "127.0.0.1:8003") || !strings.Contains(got, "127.0.0.1:8004") || len(sockets) != 2 {
		t.Fatalf("sockets = %v, want the two handed-off descriptors", sockets)
	}
}

// pollPIDs requests /fast until stop closes, recording each response's
// serving PID with its arrival time, and every failure.
func pollPIDs(client *http.Client, addr string, stop <-chan struct{}) (results func() ([]servedBy, []string)) {
	var (
		mu       sync.Mutex
		served   []servedBy
		failures []string
		done     = make(chan struct{})
	)
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
			pid, err := get(client, "http://"+addr+"/fast")
			mu.Lock()
			if err != nil {
				failures = append(failures, err.Error())
			} else {
				served = append(served, servedBy{pid: pid, at: time.Now()})
			}
			mu.Unlock()
		}
	}()
	return func() ([]servedBy, []string) {
		<-done
		mu.Lock()
		defer mu.Unlock()
		return served, failures
	}
}

type servedBy struct {
	pid string
	at  time.Time
}

// TestHandoffChecksGateTheSuccessor: with pre-traffic checks, the successor
// takes no traffic until it answered the old generation's check, then takes
// over; no request fails across the hand-off.
func TestHandoffChecksGateTheSuccessor(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns helper processes")
	}
	addr := freeAddr(t)
	done := t.TempDir() + "/check-done"
	old := startHandoffDaemon(t, addr, time.Second, handoffCheckEnv+"=1", handoffCheckDelayEnv+"=1500ms",
		handoffCheckDoneEnv+"="+done)
	oldPID := strconv.Itoa(old.Process.Pid)
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	waitFor(t, func() bool { _, err := get(client, "http://"+addr+"/fast"); return err == nil })

	stop := make(chan struct{})
	results := pollPIDs(client, addr, stop)
	if err := old.Process.Signal(syscall.SIGUSR2); err != nil {
		t.Fatal(err)
	}
	var newPID string
	waitFor(t, func() bool {
		pid, err := get(client, "http://"+addr+"/fast")
		if err == nil && pid != oldPID {
			newPID = pid
			return true
		}
		return false
	})
	t.Cleanup(func() {
		if pid, err := strconv.Atoi(newPID); err == nil {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	})
	time.Sleep(200 * time.Millisecond)
	close(stop)
	served, failures := results()
	if len(failures) != 0 {
		t.Fatalf("%d requests failed across a checked hand-off; first: %s", len(failures), failures[0])
	}
	raw, err := os.ReadFile(done)
	if err != nil {
		t.Fatalf("the successor never answered its check: %v", err)
	}
	checked, _ := strconv.ParseInt(string(raw), 10, 64)
	for _, s := range served {
		if s.pid == newPID && s.at.UnixNano() < checked {
			t.Fatalf("the successor served traffic at %v, before it answered its check at %v", s.at, time.Unix(0, checked))
		}
	}
}

// TestHandoffCheckFailureRejectsTheSuccessor: a successor that fails its
// pre-traffic check never takes a request, the old generation keeps every
// one, and the failure reads as a rejection.
func TestHandoffCheckFailureRejectsTheSuccessor(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns helper processes")
	}
	addr := freeAddr(t)
	errFile := t.TempDir() + "/err"
	old := startHandoffDaemon(t, addr, time.Second, handoffCheckEnv+"=1", handoffBadCheckEnv+"=1",
		handoffErrFileEnv+"="+errFile)
	oldPID := strconv.Itoa(old.Process.Pid)
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	waitFor(t, func() bool { _, err := get(client, "http://"+addr+"/fast"); return err == nil })

	if err := old.Process.Signal(syscall.SIGUSR2); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pid, err := get(client, "http://"+addr+"/fast")
		if err != nil {
			t.Fatalf("request failed while the successor was rejected: %v", err)
		}
		if pid != oldPID {
			t.Fatalf("request served by %s, a successor that failed its check", pid)
		}
	}
	waitFor(t, func() bool { _, err := os.Stat(errFile); return err == nil })
	raw, _ := os.ReadFile(errFile)
	if !strings.Contains(string(raw), "rejected by pre-traffic check check: GET /check answered 503, want [200]") {
		t.Fatalf("hand-off error = %q, want the rejection", raw)
	}
	if !errors.Is(fmt.Errorf("x: %w", wire.ErrHandoffRejected), wire.ErrHandoffRejected) {
		t.Fatal("ErrHandoffRejected does not wrap")
	}
}

// TestHandoffToASuccessorWithoutChecks: a successor from a release before
// pre-traffic checks reports ready without them and is handed off as before.
func TestHandoffToASuccessorWithoutChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns helper processes")
	}
	addr := freeAddr(t)
	old := startHandoffDaemon(t, addr, time.Second, handoffCheckEnv+"=1", handoffOldSuccessorEnv+"=1")
	oldPID := strconv.Itoa(old.Process.Pid)
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	waitFor(t, func() bool { _, err := get(client, "http://"+addr+"/fast"); return err == nil })
	if err := old.Process.Signal(syscall.SIGUSR2); err != nil {
		t.Fatal(err)
	}
	var newPID string
	waitFor(t, func() bool {
		pid, err := get(client, "http://"+addr+"/fast")
		if err == nil && pid != oldPID {
			newPID = pid
			return true
		}
		return false
	})
	if pid, err := strconv.Atoi(newPID); err == nil {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
}
