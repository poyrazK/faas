//go:build unix

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDevBridgeProcessHelper(t *testing.T) {
	mode := os.Getenv("GREGALE_BRIDGE_PROCESS_HELPER")
	if mode == "" {
		return
	}
	if mode == "exit" {
		os.Exit(7)
	}
	if mode == "descendant" {
		child := exec.Command(os.Args[0], "-test.run=^TestDevBridgeProcessHelper$") //nolint:noctx // Deliberately outlives its parent fixture.
		child.Env = append(os.Environ(), "GREGALE_BRIDGE_PROCESS_HELPER=wait")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(9)
		}
		os.Exit(7)
	}
	if mode == "ignore-term" {
		signal.Ignore(syscall.SIGTERM)
	}
	_, _ = os.Stdout.WriteString("helper-ready\n")
	for {
		time.Sleep(time.Second)
	}
}

// adr: 379 — a descendant holding stdout must not delay detecting its parent's
// exit. The supervisor kills it immediately and drains the owned output pipes.
func TestDevBridgeProcessDescendantCannotHoldOutputOpen(t *testing.T) {
	_, restore := captureStdout(t)
	defer restore()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	process, err := startBridgeProcess([]string{executable, "-test.run=^TestDevBridgeProcessHelper$"}, append(os.Environ(), "GREGALE_BRIDGE_PROCESS_HELPER=descendant"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.stop() })
	select {
	case <-process.done:
	case <-time.After(2 * time.Second):
		t.Fatal("descendant output delayed parent exit")
	}
	if process.exitCode() != 7 {
		t.Fatal("parent exit code lost")
	}
	for _, done := range process.outputDone {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("descendant retained output pipe")
		}
	}
	if err := process.stop(); err != nil {
		t.Fatal(err)
	}
}

// adr: 379 — child configuration contains local URLs, never platform authority.
func TestDevBridgeProcessEnvironment(t *testing.T) {
	dependencies := map[string]string{"inventory": "http://127.0.0.1:1234"}
	env, err := bridgeProcessEnvironment([]string{"FAAS_TOKEN=secret", "GREGALE_API_TOKEN=secret", "GREGALE_DEV_BRIDGE_TOKEN=secret", "APP_SECRET=application"}, 8080, "session", "http://127.0.0.1:1235", dependencies, map[string]string{"INVENTORY_URL": "inventory"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	for _, want := range []string{"PORT=8080", "HOST=127.0.0.1", "APP_SECRET=application", "INVENTORY_URL=http://127.0.0.1:1234"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(joined, "=secret") {
		t.Fatal("platform authority inherited by child")
	}
	for _, key := range []string{"PORT", "HOST", "FAAS_TOKEN", "GREGALE_API_TOKEN", "1BAD", "BAD-NAME"} {
		if _, err := bridgeProcessEnvironment(nil, 8080, "session", "url", dependencies, map[string]string{key: "inventory"}); err == nil {
			t.Errorf("reserved/invalid binding %s accepted", key)
		}
	}
	if _, err := bridgeProcessEnvironment(nil, 8080, "session", "url", dependencies, map[string]string{"UNKNOWN_URL": "unknown"}); err == nil {
		t.Fatal("undeclared dependency bound")
	}
	if _, err := bridgeProcessEnvironment(nil, 8080, "session", "url", map[string]string{"a-b": "url", "a_b": "url"}, nil); err == nil {
		t.Fatal("ambiguous normalized name accepted")
	}
}

// adr: 379 — readiness rejects redirects and a successful premature exit.
func TestDevBridgeReadinessBoundaries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ready", http.StatusFound) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := bridgeWaitLocalReady(ctx, strings.TrimPrefix(server.URL, "http://"), "/ready", nil); err == nil {
		t.Fatal("redirect treated as readiness")
	}
	process := &bridgeProcess{done: make(chan struct{})}
	close(process.done)
	if err := bridgeWaitLocalReady(t.Context(), "127.0.0.1:1", "", process); err == nil {
		t.Fatal("premature exit treated as readiness")
	}
}

// adr: 379 — graceful and forced shutdown finish within a bounded interval.
func TestDevBridgeProcessStopsAndPreservesExit(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"exit", "wait", "ignore-term"} {
		t.Run(mode, func(t *testing.T) {
			stdout, restore := captureStdout(t)
			defer restore()
			process, err := startBridgeProcess([]string{executable, "-test.run=^TestDevBridgeProcessHelper$"}, append(os.Environ(), "GREGALE_BRIDGE_PROCESS_HELPER="+mode))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = process.stop() })
			deadline := time.Now().Add(3 * time.Second)
			for mode != "exit" && !strings.Contains(stdout.String(), "helper-ready") {
				if time.Now().After(deadline) {
					t.Fatal("child did not become ready")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if mode == "exit" {
				select {
				case <-process.done:
				case <-time.After(3 * time.Second):
					t.Fatal("child exit not observed")
				}
				if process.exitCode() != 7 {
					t.Fatal("exit code changed")
				}
			}
			if err := process.stop(); err != nil {
				t.Fatal(err)
			}
			if localAppProcessGroupAlive(process.command) {
				t.Fatal("process group survived shutdown")
			}
		})
	}
}
