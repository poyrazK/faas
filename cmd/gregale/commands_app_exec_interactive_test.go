package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestExpandShortExecFlags(t *testing.T) {
	got := expandShortExecFlags([]string{"-it", "--timeout-seconds", "60", "-t", "--", "-it", "-i"})
	want := []string{"--interactive", "--tty", "--timeout-seconds", "60", "--tty", "--", "-it", "-i"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expandShortExecFlags = %q, want %q", got, want)
	}
	if got := expandShortExecFlags([]string{"-i", "-ti"}); !reflect.DeepEqual(got, []string{"--interactive", "--interactive", "--tty"}) {
		t.Fatalf("expandShortExecFlags(-i -ti) = %q", got)
	}
}

// TestPumpAppTaskAttachRelaysSession drives the CLI pump against a fake
// gateway: stdin reaches the server, output reaches stdout/stderr, stdin EOF
// sends stdin-close, and the terminal message ends the session.
func TestPumpAppTaskAttachRelaysSession(t *testing.T) {
	received := make(chan []byte, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{Subprotocols: []string{api.AppTaskAttachSubprotocol}}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			received <- msg
			if msg[0] == api.AppTaskAttachStdinClose {
				_ = conn.WriteMessage(websocket.BinaryMessage, append([]byte{api.AppTaskAttachStdout}, []byte("out")...))
				_ = conn.WriteMessage(websocket.BinaryMessage, append([]byte{api.AppTaskAttachStderr}, []byte("err")...))
				exit := 4
				terminal, _ := api.EncodeAppTaskAttachTerminal(api.AppTaskAttachTerminalMessage{Status: api.AppTaskStatusFailed, ExitCode: &exit})
				_ = conn.WriteMessage(websocket.BinaryMessage, terminal)
				return
			}
		}
	}))
	defer server.Close()

	dialer := websocket.Dialer{Subprotocols: []string{api.AppTaskAttachSubprotocol}}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	oldIn, oldOut, oldErr := osStdin, osStdout, osStderr
	defer func() { osStdin, osStdout, osStderr = oldIn, oldOut, oldErr }()
	var stdout, stderr bytes.Buffer
	osStdin, osStdout, osStderr = strings.NewReader("ls\n"), &stdout, &stderr

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	terminal, err := pumpAppTaskAttach(ctx, conn, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.ExitCode == nil || *terminal.ExitCode != 4 || stdout.String() != "out" || stderr.String() != "err" {
		t.Fatalf("terminal=%+v stdout=%q stderr=%q", terminal, stdout.String(), stderr.String())
	}
	first, second := <-received, <-received
	if first[0] != api.AppTaskAttachStdin || string(first[1:]) != "ls\n" || second[0] != api.AppTaskAttachStdinClose {
		t.Fatalf("client messages = %q, %q", first, second)
	}
}

func TestDialAppTaskAttachRetriesWhileStarting(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.Header.Get(api.AppTaskAttachTokenHeader) != "tok" || r.Header.Get("Authorization") != "Bearer key" ||
			r.URL.Path != "/v1/apps/api/tasks/t1/attach" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if attempts < 3 {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"detail":"starting"}`))
			return
		}
		upgrader := websocket.Upgrader{Subprotocols: []string{api.AppTaskAttachSubprotocol}}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()
	client := api.NewClient(server.URL, "key")
	task := api.AppTaskResponse{ID: "t1", Attach: &api.AppTaskAttachInfo{Token: "tok", Path: api.AppTaskAttachPath("api", "t1")}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := dialAppTaskAttach(ctx, client, "api", task, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestDialAppTaskAttachSurfacesProblemDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"the attach token does not match this session"}`))
	}))
	defer server.Close()
	task := api.AppTaskResponse{ID: "t1", Attach: &api.AppTaskAttachInfo{Token: "tok", Path: api.AppTaskAttachPath("api", "t1")}}
	_, err := dialAppTaskAttach(context.Background(), api.NewClient(server.URL, "key"), "api", task, false, nil)
	if err == nil || !strings.Contains(err.Error(), "does not match") || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v", err)
	}
}
