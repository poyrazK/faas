//go:build metal

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// adr: 1330
// TestInteractiveImageRunMetal attaches to interactive image runs through the
// public edge on real VMs: a pipes session relays stdin and output and ends
// with the command's exit code, which also reaches the task row; a TTY
// session gets a terminal; closing the session cancels the run.
func TestInteractiveImageRunMetal(t *testing.T) {
	h := newMetalHarnessWith(t, e2etest.Gatewayd|e2etest.GatewaydPublic, "FAAS_IMAGE_RUNS=1", "FAAS_INTERACTIVE_IMAGE_RUNS=1")
	defer h.Close()
	h.MustSeedFakeImage(t, "busybox:interactive")
	image := h.imageRef(t, "busybox:interactive")
	args := []string{"/job-fixture", "interactive"}

	t.Run("pipes", func(t *testing.T) {
		run := h.mustStartImageRun(t, api.CreateImageRunRequest{Image: image, Args: &args, Interactive: true}, 5*time.Minute)
		session := h.mustAttachImageRun(t, run, "")
		defer session.close()
		var out bytes.Buffer
		session.readUntil(&out, "tty:false")
		session.send(api.AppTaskAttachStdin, []byte("hello\n"))
		session.readUntil(&out, "got:hello")
		session.send(api.AppTaskAttachStdin, []byte("exit 7\n"))
		if terminal := session.drain(&out); terminal.ExitCode == nil || *terminal.ExitCode != 7 {
			t.Fatalf("terminal = %+v, want exit code 7", terminal)
		}
		h.mustFinishInteractiveRun(t, run, "any-terminal", 7)
	})

	t.Run("tty", func(t *testing.T) {
		run := h.mustStartImageRun(t, api.CreateImageRunRequest{Image: image, Args: &args, Interactive: true, TTY: true}, 5*time.Minute)
		session := h.mustAttachImageRun(t, run, "?rows=30&cols=100")
		defer session.close()
		var out bytes.Buffer
		session.readUntil(&out, "tty:true")
		session.send(api.AppTaskAttachStdin, []byte("exit 0\n"))
		if terminal := session.drain(&out); terminal.ExitCode == nil || *terminal.ExitCode != 0 {
			t.Fatalf("terminal = %+v, want exit code 0", terminal)
		}
		h.mustFinishInteractiveRun(t, run, "succeeded", 0)
	})

	t.Run("disconnect cancels the run", func(t *testing.T) {
		run := h.mustStartImageRun(t, api.CreateImageRunRequest{Image: image, Args: &args, Interactive: true}, 5*time.Minute)
		session := h.mustAttachImageRun(t, run, "")
		var out bytes.Buffer
		session.readUntil(&out, "tty:false")
		session.close()
		h.mu.Lock()
		h.runNames[run.RunID] = run.Job
		h.mu.Unlock()
		h.MustWaitRunTerminal(t, &api.JobRunResponse{ID: run.RunID}, "cancelled", 2*time.Minute)
	})
}

// imageRunSession is one attach WebSocket (ADR-1037 wire contract).
type imageRunSession struct {
	t    *testing.T
	conn *websocket.Conn
}

// mustAttachImageRun dials the run's attach path through the public edge,
// retrying while the task is queued or its VM boots (409).
func (h *MetalJobHarness) mustAttachImageRun(t *testing.T, run api.ImageRunResponse, query string) *imageRunSession {
	t.Helper()
	if run.Attach == nil || run.Attach.Token == "" {
		t.Fatalf("image run %s has no attach credential", run.RunID)
	}
	url := "ws" + strings.TrimPrefix(h.EdgeURL(), "http") + run.Attach.Path + query
	header := http.Header{"Authorization": {"Bearer " + h.defaultKey}, api.AppTaskAttachTokenHeader: {run.Attach.Token}}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Subprotocols: []string{api.AppTaskAttachSubprotocol}}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		conn, resp, err := dialer.Dial(url, header)
		if err == nil {
			session := &imageRunSession{t: t, conn: conn}
			if msg := session.next(); len(msg) != 1 || msg[0] != api.AppTaskAttachAttached {
				t.Fatalf("first attach message = %v", msg)
			}
			return session
		}
		status := 0
		if resp != nil {
			status = resp.StatusCode
			_ = resp.Body.Close()
		}
		if status != http.StatusConflict || time.Now().After(deadline) {
			t.Fatalf("attach run %s: status %d: %v", run.RunID, status, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// mustFinishInteractiveRun waits for the run and checks the task's exit code
// and that the session's output was not stored.
func (h *MetalJobHarness) mustFinishInteractiveRun(t *testing.T, run api.ImageRunResponse, want string, exitCode int) {
	t.Helper()
	h.mu.Lock()
	h.runNames[run.RunID] = run.Job
	h.mu.Unlock()
	ref := &api.JobRunResponse{ID: run.RunID}
	h.MustWaitRunTerminal(t, ref, want, 2*time.Minute)
	h.MustAssertTaskExitCodes(t, ref, exitCode)
	path := "/v1/jobs/" + run.Job + "/runs/" + run.RunID + "/tasks/0/logs"
	raw, status, _ := h.request(t, h.defaultKey, http.MethodGet, path, nil)
	var logs api.JobTaskLogResponse
	decodeOK(t, raw, status, &logs, http.MethodGet, path)
	if strings.Contains(logs.LogContent, "got:") {
		t.Fatalf("interactive session output was stored: %q", logs.LogContent)
	}
}

func (s *imageRunSession) send(kind byte, payload []byte) {
	s.t.Helper()
	if err := s.conn.WriteMessage(websocket.BinaryMessage, append([]byte{kind}, payload...)); err != nil {
		s.t.Fatalf("send: %v", err)
	}
}

func (s *imageRunSession) next() []byte {
	s.t.Helper()
	_ = s.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	_, msg, err := s.conn.ReadMessage()
	if err != nil {
		s.t.Fatalf("read: %v", err)
	}
	return msg
}

// readUntil collects output until it contains want; a terminal message first
// fails the test.
func (s *imageRunSession) readUntil(out *bytes.Buffer, want string) {
	s.t.Helper()
	for !strings.Contains(out.String(), want) {
		msg := s.next()
		switch msg[0] {
		case api.AppTaskAttachStdout, api.AppTaskAttachStderr:
			out.Write(msg[1:])
		case api.AppTaskAttachTerminal:
			s.t.Fatalf("session ended before %q; output %q, terminal %s", want, out.String(), msg[1:])
		}
	}
}

// drain collects output until the terminal message.
func (s *imageRunSession) drain(out *bytes.Buffer) api.AppTaskAttachTerminalMessage {
	s.t.Helper()
	for {
		msg := s.next()
		switch msg[0] {
		case api.AppTaskAttachStdout, api.AppTaskAttachStderr:
			out.Write(msg[1:])
		case api.AppTaskAttachTerminal:
			var terminal api.AppTaskAttachTerminalMessage
			if err := json.Unmarshal(msg[1:], &terminal); err != nil {
				s.t.Fatal(err)
			}
			return terminal
		}
	}
}

func (s *imageRunSession) close() { _ = s.conn.Close() }
