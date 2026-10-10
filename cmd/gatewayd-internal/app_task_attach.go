package main

// AppTaskAttachHandler owns `GET /v1/apps/{slug}/tasks/{id}/attach`
// (ADR-958): it authenticates the caller with the shared auth chain, finds
// the node running the interactive task VM, presents the one-time attach
// token to that node's vmmd, and relays the session over a WebSocket.
// apid never sees the path (component ownership: apid must not reach vmmd).

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	mwauth "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	appTaskAttachPingInterval = 30 * time.Second
	appTaskAttachWriteTimeout = 10 * time.Second
	appTaskAttachDialTimeout  = 10 * time.Second
)

type appTaskAttachTargets interface {
	AppTaskAttachTarget(ctx context.Context, accountID, appID, taskID string) (state.AppTaskAttachTarget, error)
}

type appTaskAttachNodes interface {
	ClientFor(ctx context.Context, nodeID string) (vmmdpb.VmmdClient, io.Closer, bool)
}

type AppTaskAttachHandler struct {
	Auth    *mwauth.Middleware
	Targets appTaskAttachTargets
	Nodes   appTaskAttachNodes
	Log     *slog.Logger
	// PingInterval keeps idle sessions alive through proxies; tests shorten it.
	PingInterval time.Duration
}

func (h *AppTaskAttachHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	inner := mwauth.AccountHandler(func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		app, ok := h.Auth.LoadApp(w, r, acct, r.PathValue("slug"))
		if !ok {
			return
		}
		h.attach(w, r, acct, app)
	})
	chain := h.Auth.RequireScope(api.ScopesDeployWriteSurface...)(h.Auth.RequireMFA(inner))
	h.Auth.RequireLimited(chain)(w, r)
}

func (h *AppTaskAttachHandler) attach(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) {
	token := r.Header.Get(api.AppTaskAttachTokenHeader)
	if token == "" || len(r.Header.Values(api.AppTaskAttachTokenHeader)) != 1 {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Attach token required", "send the token from the task's create response in "+api.AppTaskAttachTokenHeader))
		return
	}
	target, err := h.Targets.AppTaskAttachTarget(r.Context(), acct.ID, app.ID, r.PathValue("id"))
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"Interactive task not found", "no interactive task with this id exists for the app"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load the interactive task"))
		return
	}
	switch {
	case target.TaskStatus.Terminal():
		api.WriteProblem(w, api.NewProblem(http.StatusGone, api.CodeConflict,
			"Session ended", "the interactive task has already finished"))
		return
	case target.TaskStatus != state.AppTaskRunning || target.NodeID == "":
		w.Header().Set("Retry-After", "1")
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Session not ready", "the task VM is still starting; retry shortly"))
		return
	}
	rows, cols := attachTerminalSize(r)

	sessionCtx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	dialCtx, cancelDial := context.WithTimeout(sessionCtx, appTaskAttachDialTimeout)
	client, closer, ok := h.Nodes.ClientFor(dialCtx, target.NodeID)
	cancelDial()
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeInternal,
			"Node unreachable", "the node running this session is not reachable"))
		return
	}
	defer func() { _ = closer.Close() }()
	stream, err := client.AttachAppTask(sessionCtx)
	if err == nil {
		err = stream.Send(&vmmdpb.AttachAppTaskRequest{Frame: &vmmdpb.AttachAppTaskRequest_Start{
			Start: &vmmdpb.AttachAppTaskStart{Instance: target.TaskID, AttachToken: token, Rows: rows, Cols: cols},
		}})
	}
	var first *vmmdpb.AttachAppTaskEvent
	if err == nil {
		first, err = stream.Recv()
	}
	if err != nil || first.GetAttached() == nil {
		api.WriteProblem(w, attachProblem(err))
		return
	}

	upgrader := websocket.Upgrader{Subprotocols: []string{api.AppTaskAttachSubprotocol}}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	if conn.Subprotocol() != api.AppTaskAttachSubprotocol {
		_ = conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseProtocolError, "subprotocol "+api.AppTaskAttachSubprotocol+" required"),
			time.Now().Add(appTaskAttachWriteTimeout))
		return
	}
	h.relay(sessionCtx, cancel, conn, stream)
}

// relay pumps client messages to vmmd and vmmd events to the client until
// the terminal result arrives or either side goes away.
func (h *AppTaskAttachHandler) relay(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, stream vmmdpb.Vmmd_AttachAppTaskClient) {
	var writeMu sync.Mutex
	write := func(messageType int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(appTaskAttachWriteTimeout))
		return conn.WriteMessage(messageType, data)
	}
	if err := write(websocket.BinaryMessage, []byte{api.AppTaskAttachAttached}); err != nil {
		return
	}
	conn.SetReadLimit(api.AppTaskAttachMaxMessageBytes)

	go func() {
		defer cancel()
		for {
			messageType, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if messageType != websocket.BinaryMessage || len(msg) == 0 {
				continue
			}
			frame := attachRequestFrame(msg)
			if frame == nil {
				continue
			}
			if err := stream.Send(frame); err != nil {
				return
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(h.pingInterval())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				writeMu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(appTaskAttachWriteTimeout))
				writeMu.Unlock()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	for {
		event, err := stream.Recv()
		if err != nil {
			reason := "session ended"
			if status.Code(err) != codes.Canceled {
				h.log().Warn("gatewayd: interactive app task stream ended without a result", "code", status.Code(err).String())
				reason = "session transport failed"
			}
			writeMu.Lock()
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, reason),
				time.Now().Add(appTaskAttachWriteTimeout))
			writeMu.Unlock()
			return
		}
		switch frame := event.GetFrame().(type) {
		case *vmmdpb.AttachAppTaskEvent_Output:
			kind := api.AppTaskAttachStdout
			if frame.Output.GetStream() == "stderr" {
				kind = api.AppTaskAttachStderr
			}
			if err := write(websocket.BinaryMessage, append([]byte{kind}, frame.Output.GetChunk()...)); err != nil {
				return
			}
		case *vmmdpb.AttachAppTaskEvent_Terminal:
			msg, encodeErr := api.EncodeAppTaskAttachTerminal(attachTerminalMessage(frame.Terminal))
			if encodeErr == nil {
				_ = write(websocket.BinaryMessage, msg)
			}
			writeMu.Lock()
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
				time.Now().Add(appTaskAttachWriteTimeout))
			writeMu.Unlock()
			return
		}
	}
}

func (h *AppTaskAttachHandler) pingInterval() time.Duration {
	if h.PingInterval > 0 {
		return h.PingInterval
	}
	return appTaskAttachPingInterval
}

func (h *AppTaskAttachHandler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func attachTerminalSize(r *http.Request) (uint32, uint32) {
	parse := func(name string) uint32 {
		value, err := strconv.ParseUint(r.URL.Query().Get(name), 10, 16)
		if err != nil {
			return 0
		}
		return uint32(value)
	}
	return parse("rows"), parse("cols")
}

func attachRequestFrame(msg []byte) *vmmdpb.AttachAppTaskRequest {
	payload := msg[1:]
	switch msg[0] {
	case api.AppTaskAttachStdin:
		if len(payload) == 0 {
			return nil
		}
		return &vmmdpb.AttachAppTaskRequest{Frame: &vmmdpb.AttachAppTaskRequest_Stdin{Stdin: append([]byte(nil), payload...)}}
	case api.AppTaskAttachResize:
		rows, cols, err := api.DecodeAppTaskAttachResize(payload)
		if err != nil {
			return nil
		}
		return &vmmdpb.AttachAppTaskRequest{Frame: &vmmdpb.AttachAppTaskRequest_Resize{
			Resize: &vmmdpb.AttachAppTaskResize{Rows: uint32(rows), Cols: uint32(cols)},
		}}
	case api.AppTaskAttachStdinClose:
		return &vmmdpb.AttachAppTaskRequest{Frame: &vmmdpb.AttachAppTaskRequest_StdinClose{StdinClose: &vmmdpb.AttachAppTaskStdinClose{}}}
	default:
		return nil
	}
}

func attachTerminalMessage(terminal *vmmdpb.ExecuteAppTaskResponse) api.AppTaskAttachTerminalMessage {
	msg := api.AppTaskAttachTerminalMessage{Status: api.AppTaskStatus(terminal.GetStatus())}
	if exit := terminal.GetExitCode(); exit != nil {
		value := int(exit.GetValue())
		msg.ExitCode = &value
	}
	if terminal.GetFailureCode() != "" {
		msg.Failure = &api.AppTaskFailure{Code: terminal.GetFailureCode(), Message: terminal.GetFailureMessage()}
	}
	return msg
}

func attachProblem(err error) *api.Problem {
	switch status.Code(err) {
	case codes.PermissionDenied:
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Attach rejected", "the attach token does not match this session")
	case codes.NotFound:
		return api.NewProblem(http.StatusGone, api.CodeConflict, "Session unavailable",
			"the session already has a client, or its attach window closed")
	default:
		return api.NewProblem(http.StatusBadGateway, api.CodeInternal, "Attach failed", "the node could not start the session")
	}
}

// appTaskAttachCarveOut routes the attach path to its handler ahead of the
// apid loopback proxy.
type appTaskAttachCarveOut struct {
	attach http.Handler
	next   http.Handler
}

func (c appTaskAttachCarveOut) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if c.attach != nil && r.Method == http.MethodGet && api.IsAppTaskAttachPath(r.URL.Path) {
		c.attach.ServeHTTP(w, r)
		return
	}
	c.next.ServeHTTP(w, r)
}
