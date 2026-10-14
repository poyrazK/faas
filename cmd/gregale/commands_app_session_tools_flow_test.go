package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskmux"
)

// fakeSessionGateway admits one interactive task and serves its attach
// WebSocket by running serve over the session's stdin/stdout.
func fakeSessionGateway(t *testing.T, serve func(stdin io.Reader, stdout io.Writer)) (*httptest.Server, *api.CreateAppTaskRequest) {
	t.Helper()
	var admitted api.CreateAppTaskRequest
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/apps/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&admitted)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: "t1", Status: api.AppTaskStatusQueued, Interactive: true,
			Attach: &api.AppTaskAttachInfo{Token: "tok", Path: api.AppTaskAttachPath("api", "t1"), Subprotocol: api.AppTaskAttachSubprotocol}})
	})
	mux.HandleFunc("GET /v1/apps/api/tasks/t1/attach", func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{Subprotocols: []string{api.AppTaskAttachSubprotocol}}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		stdinR, stdinW := io.Pipe()
		go func() {
			defer stdinW.Close()
			for {
				_, msg, err := conn.ReadMessage()
				if err != nil || len(msg) == 0 || msg[0] == api.AppTaskAttachStdinClose {
					return
				}
				if msg[0] == api.AppTaskAttachStdin {
					_, _ = stdinW.Write(msg[1:])
				}
			}
		}()
		out := &wsStdout{conn: conn}
		serve(stdinR, out)
		exit := 0
		terminal, _ := api.EncodeAppTaskAttachTerminal(api.AppTaskAttachTerminalMessage{Status: api.AppTaskStatusSucceeded, ExitCode: &exit})
		_ = out.write(terminal)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &admitted
}

type wsStdout struct {
	conn *websocket.Conn
}

func (w *wsStdout) write(msg []byte) error { return w.conn.WriteMessage(websocket.BinaryMessage, msg) }

func (w *wsStdout) Write(p []byte) (int, error) {
	if err := w.write(append([]byte{api.AppTaskAttachStdout}, p...)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func TestPortForwardSessionRelaysThroughGateway(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				data, _ := io.ReadAll(conn)
				_, _ = conn.Write(bytes.ToUpper(data))
			}()
		}
	}()
	server, admitted := fakeSessionGateway(t, func(stdin io.Reader, stdout io.Writer) {
		_ = apptaskmux.Serve(context.Background(), stdin, stdout, func(ctx context.Context) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", echo.Addr().String())
		})
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := api.NewClient(server.URL, "key")
	session, closeSession, err := openAppTaskStreamSession(ctx, client, "api", []string{api.AppTaskPortForwardCommand, "db.svc.gregale:5432"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession()
	if !admitted.Interactive || admitted.TTY || admitted.Command[0] != api.AppTaskPortForwardCommand {
		t.Fatalf("admitted request = %+v", admitted)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	forwarded := make(chan error, 1)
	go func() { forwarded <- apptaskmux.Forward(ctx, ln, session, session.stdout) }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte("select 1;"))
	_ = conn.(*net.TCPConn).CloseWrite()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, _ := io.ReadAll(conn)
	_ = conn.Close()
	if string(got) != "SELECT 1;" {
		t.Fatalf("relayed reply = %q", got)
	}
	_ = session.CloseStdin()
	terminal, err := session.Wait(ctx)
	if err != nil || terminal.ExitCode == nil || *terminal.ExitCode != 0 {
		t.Fatalf("terminal = %+v, %v", terminal, err)
	}
	if err := <-forwarded; err != nil && !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Forward = %v", err)
	}
}

func TestCopySessionExtractsArchive(t *testing.T) {
	archive := distArchive(t).Bytes()
	server, admitted := fakeSessionGateway(t, func(stdin io.Reader, stdout io.Writer) {
		_, _ = stdout.Write(archive)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, closeSession, err := openAppTaskStreamSession(ctx, api.NewClient(server.URL, "key"), "api", []string{api.AppTaskCopyOutCommand, "/app/dist"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession()
	dest := t.TempDir()
	if err := extractAppTaskTar(session.stdout, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if admitted.Command[1] != "/app/dist" {
		t.Fatalf("admitted = %+v", admitted)
	}
}
