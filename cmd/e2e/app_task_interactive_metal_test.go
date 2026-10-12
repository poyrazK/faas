//go:build metal

package e2e_test

// adr: 1037 — interactive app tasks on real Firecracker VMs: a TTY session
// through the public gateway, copy-out, and port-forward.

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskmux"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

type interactiveSessionClient struct {
	t    *testing.T
	conn *websocket.Conn
}

func createInteractiveTask(t *testing.T, h *e2etest.Harness, key, slug string, request api.CreateAppTaskRequest) api.AppTaskResponse {
	t.Helper()
	request.Interactive = true
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/tasks", request)
	if status != http.StatusAccepted {
		t.Fatalf("create interactive task: status=%d body=%s", status, body)
	}
	var task api.AppTaskResponse
	if err := json.Unmarshal(body, &task); err != nil || task.Attach == nil || task.Attach.Token == "" {
		t.Fatalf("create interactive task response = %s (%v)", body, err)
	}
	return task
}

// attachInteractiveTask dials the attach WebSocket through the public edge,
// retrying while the task VM starts (409).
func attachInteractiveTask(t *testing.T, h *e2etest.Harness, key string, task api.AppTaskResponse, token, query string) (*interactiveSessionClient, int) {
	t.Helper()
	url := "ws" + strings.TrimPrefix(h.EdgeURL(), "http") + task.Attach.Path + query
	header := http.Header{"Authorization": {"Bearer " + key}, api.AppTaskAttachTokenHeader: {token}}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Subprotocols: []string{api.AppTaskAttachSubprotocol}}
	deadline := time.Now().Add(90 * time.Second)
	for {
		conn, resp, err := dialer.Dial(url, header)
		if err == nil {
			client := &interactiveSessionClient{t: t, conn: conn}
			if msg := client.next(); len(msg) != 1 || msg[0] != api.AppTaskAttachAttached {
				t.Fatalf("first attach message = %v", msg)
			}
			return client, http.StatusSwitchingProtocols
		}
		status := 0
		if resp != nil {
			status = resp.StatusCode
			_ = resp.Body.Close()
		}
		if status != http.StatusConflict || time.Now().After(deadline) {
			return nil, status
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (c *interactiveSessionClient) send(kind byte, payload []byte) {
	c.t.Helper()
	if err := c.conn.WriteMessage(websocket.BinaryMessage, append([]byte{kind}, payload...)); err != nil {
		c.t.Fatalf("send: %v", err)
	}
}

func (c *interactiveSessionClient) next() []byte {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	_, msg, err := c.conn.ReadMessage()
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	return msg
}

// readUntil collects stdout until it contains want; it fails on a terminal
// message that arrives first.
func (c *interactiveSessionClient) readUntil(out *bytes.Buffer, want string) {
	c.t.Helper()
	for !strings.Contains(out.String(), want) {
		msg := c.next()
		switch msg[0] {
		case api.AppTaskAttachStdout, api.AppTaskAttachStderr:
			out.Write(msg[1:])
		case api.AppTaskAttachTerminal:
			c.t.Fatalf("session ended before %q; output %q, terminal %s", want, out.String(), msg[1:])
		}
	}
}

// drain collects stdout until the terminal message and returns both.
func (c *interactiveSessionClient) drain(out *bytes.Buffer) api.AppTaskAttachTerminalMessage {
	c.t.Helper()
	for {
		msg := c.next()
		switch msg[0] {
		case api.AppTaskAttachStdout:
			out.Write(msg[1:])
		case api.AppTaskAttachTerminal:
			terminal, err := api.DecodeAppTaskAttachTerminal(msg[1:])
			if err != nil {
				c.t.Fatal(err)
			}
			return terminal
		}
	}
}

func TestInteractiveAppTaskMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping interactive app task acceptance")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping interactive app task acceptance")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "interactive")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake,
		"FAAS_APP_TASK_API_ENABLED=1", "FAAS_INTERACTIVE_APP_TASKS=1", "FAAS_APP_TASK_DISPATCH=1")
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "shell-app", Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	image, _ := e2etest.HelloImage("library/shell-app", "hello from the shell app")
	ref := registry.AddImage("library/shell-app", image)
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/shell-app/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, body)
	}
	deployCtx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	if _, err := e2etest.WaitForDeploymentLive(deployCtx, t, pool, parseImageDeployment(t, body), 240*time.Second); err != nil {
		t.Fatalf("deployment did not reach live: %v", err)
	}

	t.Run("tty_session", func(t *testing.T) {
		task := createInteractiveTask(t, h, key, "shell-app", api.CreateAppTaskRequest{
			TTY: true, Command: []string{"/hello-server", "-echo-session"}, TimeoutSeconds: 300,
		})
		if _, status := attachInteractiveTask(t, h, key, task, "not-the-token", ""); status != http.StatusForbidden {
			t.Fatalf("wrong token attach status = %d, want 403", status)
		}
		session, status := attachInteractiveTask(t, h, key, task, task.Attach.Token, "?rows=33&cols=101")
		if session == nil {
			t.Fatalf("attach status = %d", status)
		}
		defer session.conn.Close()
		var out bytes.Buffer
		session.readUntil(&out, "tty:true size:33x101")
		session.send(api.AppTaskAttachStdin, []byte("hi\n"))
		session.readUntil(&out, "got:hi")
		session.send(api.AppTaskAttachResize, api.EncodeAppTaskAttachResize(40, 120)[1:])
		session.send(api.AppTaskAttachStdin, []byte("size\n"))
		session.readUntil(&out, "size:40x120")
		session.send(api.AppTaskAttachStdin, []byte("exit\n"))
		terminal := session.drain(&out)
		if terminal.ExitCode == nil || *terminal.ExitCode != 7 {
			t.Fatalf("terminal = %s", describeTerminal(terminal))
		}
		if _, status := attachInteractiveTask(t, h, key, task, task.Attach.Token, ""); status != http.StatusGone {
			t.Fatalf("reattach status = %d, want 410", status)
		}
		row, readBody := waitInteractiveTaskTerminal(t, h, key, "shell-app", task.ID)
		if row.Status != api.AppTaskStatusFailed || row.ExitCode == nil || *row.ExitCode != 7 || row.StdoutTail != "" {
			t.Fatalf("stored task = %s", readBody)
		}
	})

	t.Run("copy_out", func(t *testing.T) {
		task := createInteractiveTask(t, h, key, "shell-app", api.CreateAppTaskRequest{
			Command: []string{api.AppTaskCopyOutCommand, "/app/hello.txt"},
		})
		session, status := attachInteractiveTask(t, h, key, task, task.Attach.Token, "")
		if session == nil {
			t.Fatalf("attach status = %d", status)
		}
		defer session.conn.Close()
		session.send(api.AppTaskAttachStdinClose, nil)
		var archive bytes.Buffer
		terminal := session.drain(&archive)
		if terminal.ExitCode == nil || *terminal.ExitCode != 0 {
			t.Fatalf("copy terminal = %s", describeTerminal(terminal))
		}
		tr := tar.NewReader(&archive)
		header, err := tr.Next()
		if err != nil {
			t.Fatalf("archive: %v", err)
		}
		content, _ := io.ReadAll(tr)
		if header.Name != "hello.txt" || strings.TrimSpace(string(content)) != "hello from the shell app" {
			t.Fatalf("archive entry %q = %q", header.Name, content)
		}
	})

	t.Run("port_forward", func(t *testing.T) {
		// guest-init's workload identity proxy listens on loopback in every
		// task VM; any HTTP answer proves the relay end to end.
		task := createInteractiveTask(t, h, key, "shell-app", api.CreateAppTaskRequest{
			Command: []string{api.AppTaskPortForwardCommand, "127.0.0.1:2773"},
		})
		session, status := attachInteractiveTask(t, h, key, task, task.Attach.Token, "")
		if session == nil {
			t.Fatalf("attach status = %d", status)
		}
		defer session.conn.Close()
		frames := apptaskmux.NewWriter(writerTo(func(p []byte) error {
			session.send(api.AppTaskAttachStdin, p)
			return nil
		}))
		_ = frames.Open(1)
		_ = frames.Data(1, []byte("GET /healthz HTTP/1.0\r\nHost: localhost\r\n\r\n"))
		_ = frames.EOF(1)

		stdoutR, stdoutW := io.Pipe()
		go func() {
			// No t.Fatalf off the test goroutine: report through the pipe.
			for {
				_ = session.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
				_, msg, err := session.conn.ReadMessage()
				if err != nil {
					_ = stdoutW.CloseWithError(err)
					return
				}
				if len(msg) > 0 && msg[0] == api.AppTaskAttachStdout {
					_, _ = stdoutW.Write(msg[1:])
				}
				if len(msg) > 0 && msg[0] == api.AppTaskAttachTerminal {
					_ = stdoutW.CloseWithError(errors.New("session ended: " + string(msg[1:])))
					return
				}
			}
		}()
		reader := apptaskmux.NewReader(stdoutR)
		var reply bytes.Buffer
		for {
			frame, err := reader.Next()
			if err != nil {
				t.Fatalf("relay frames: %v (reply so far %q)", err, reply.String())
			}
			if frame.Stream != 1 {
				continue
			}
			if frame.Type == apptaskmux.FrameData {
				reply.Write(frame.Payload)
			}
			if frame.Type == apptaskmux.FrameEOF || frame.Type == apptaskmux.FrameClose {
				break
			}
		}
		if !strings.HasPrefix(reply.String(), "HTTP/1.") {
			t.Fatalf("relayed reply = %q", reply.String())
		}
		session.send(api.AppTaskAttachStdinClose, nil)
	})

	// Task VMs are torn down by vmmd; the app's own serving instance must be
	// parked so the harness does not leave it running for leakcheck.
	appID := mustGetAppID(t, h, key, "shell-app")
	if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/shell-app/park", nil); status != http.StatusNoContent {
		t.Fatalf("cleanup park: status=%d", status)
	}
	parkCtx, parkCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer parkCancel()
	if _, err := e2etest.WaitForInstanceState(parkCtx, t, pool, appID, state.StateParked, 45*time.Second); err != nil {
		t.Fatalf("cleanup park: %v", err)
	}
}

// waitInteractiveTaskTerminal polls the stored task until schedd records its
// result. The attached client sees the terminal frame first: schedd persists
// the result only after vmmd has torn the task VM down.
func waitInteractiveTaskTerminal(t *testing.T, h *e2etest.Harness, key, slug, id string) (api.AppTaskResponse, []byte) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		body, status := doReq(t, h, key, http.MethodGet, "/v1/apps/"+slug+"/tasks/"+id, nil)
		var row api.AppTaskResponse
		if status == http.StatusOK && json.Unmarshal(body, &row) == nil && row.Status.Terminal() {
			return row, body
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s did not reach a terminal state: %d %s", id, status, body)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func describeTerminal(terminal api.AppTaskAttachTerminalMessage) string {
	exit := "none"
	if terminal.ExitCode != nil {
		exit = fmt.Sprint(*terminal.ExitCode)
	}
	failure := "none"
	if terminal.Failure != nil {
		failure = terminal.Failure.Code + ": " + terminal.Failure.Message
	}
	return fmt.Sprintf("status=%s exit=%s failure=%s", terminal.Status, exit, failure)
}

type writerTo func([]byte) error

func (w writerTo) Write(p []byte) (int, error) {
	if err := w(p); err != nil {
		return 0, err
	}
	return len(p), nil
}
