// Package sandboxdata is the data plane of sandbox sessions
// (ADR-20261027090013 §3, §4): commands, background commands and files of a
// session's VM, served by gatewayd-internal and run by the session node's
// vmmd over SandboxCall.
//
// The session token is the only credential. It is verified here with apid's
// Ed25519 public key, which the gateway fetches from apid and which mints
// nothing (ADR-20261102191500), and a placed session's token
// names the node and instance it runs on, so no call reads the control
// plane's database. A call with a token that is missing, forged, expired or
// for another session answers like an absent route; a closed session's VM is
// gone, which its node reports as not found.
package sandboxdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
)

// Nodes leases a node's vmmd client; gateway.NodeClientCache implements it.
type Nodes interface {
	ClientFor(ctx context.Context, nodeID string) (vmmdpb.VmmdClient, io.Closer, bool)
}

// TokenVerifier checks session tokens; *sandboxtoken.Verifier implements it.
type TokenVerifier interface {
	Verify(token string, now time.Time) (sandboxtoken.Claims, error)
}

// Handler serves the data-plane routes. Now is time.Now unless set.
type Handler struct {
	Tokens TokenVerifier
	Nodes  Nodes
	Log    *slog.Logger
	Now    func() time.Time
}

const (
	// nodeDialTimeout bounds leasing the node's vmmd connection.
	nodeDialTimeout = 5 * time.Second
	// defaultExecTimeoutMillis is a command's timeout when the call names
	// none, within the plan's cap.
	defaultExecTimeoutMillis = 60_000
	// requestMaxBytes bounds a JSON request body.
	requestMaxBytes = 512 << 10
)

// Routes registers the data-plane routes.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/sandboxes/{id}/exec", h.exec)
	mux.HandleFunc("GET /v1/sandboxes/{id}/exec", h.execList)
	mux.HandleFunc("GET /v1/sandboxes/{id}/exec/{handle}", h.execOutput)
	mux.HandleFunc("DELETE /v1/sandboxes/{id}/exec/{handle}", h.execKill)
	mux.HandleFunc("GET /v1/sandboxes/{id}/files", h.fileRead)
	mux.HandleFunc("PUT /v1/sandboxes/{id}/files", h.fileWrite)
	mux.HandleFunc("GET /v1/sandboxes/{id}/files:list", h.fileList)
	mux.HandleFunc("GET /v1/sandboxes/{id}/files:stat", h.fileStat)
	mux.HandleFunc("POST /v1/sandboxes/{id}/files:remove", h.fileChange(sandboxproto.OpFileRemove))
	mux.HandleFunc("POST /v1/sandboxes/{id}/files:mkdir", h.fileChange(sandboxproto.OpFileMkdir))
	mux.HandleFunc("POST /v1/sandboxes/{id}/files:rename", h.fileChange(sandboxproto.OpFileRename))
}

// claims verifies the call's session token for the session in its path.
// Every failure answers like an absent route, except a session that is not
// booted yet, which is worth a retry.
func (h *Handler) claims(w http.ResponseWriter, r *http.Request) (sandboxtoken.Claims, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if h.Tokens == nil || !ok {
		http.NotFound(w, r)
		return sandboxtoken.Claims{}, false
	}
	c, err := h.Tokens.Verify(token, h.now())
	if err != nil || c.SandboxID != r.PathValue("id") {
		http.NotFound(w, r)
		return sandboxtoken.Claims{}, false
	}
	if !c.Placed() {
		w.Header().Set("Retry-After", "1")
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeSandboxNotReady, "Sandbox not ready",
			"the session is still starting; read it again for a token that reaches its VM"))
		return sandboxtoken.Claims{}, false
	}
	return c, true
}

// call runs one operation on the session's VM. upload, when set, is sent as
// the operation's data; onFrame sees each stream and data frame. It returns
// the result's JSON, or the problem to answer.
func (h *Handler) call(ctx context.Context, c sandboxtoken.Claims, req sandboxproto.Request, upload io.Reader,
	onFrame func(frameType uint32, body []byte) error) (json.RawMessage, *api.Problem) {
	if err := req.Validate(); err != nil {
		return nil, api.ErrValidation(strings.TrimPrefix(err.Error(), sandboxproto.ErrInvalidRequest.Error()+": "))
	}
	dialCtx, cancel := context.WithTimeout(ctx, nodeDialTimeout)
	client, closer, ok := h.Nodes.ClientFor(dialCtx, c.NodeID)
	cancel()
	if !ok {
		return nil, api.NewProblem(http.StatusServiceUnavailable, api.CodeInternal, "Node unreachable", "the node running this session is not reachable")
	}
	defer func() { _ = closer.Close() }()
	callCtx, stop := context.WithCancel(ctx)
	defer stop()
	stream, err := client.SandboxCall(callCtx)
	if err != nil {
		return nil, callProblem(err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, api.ErrInternal("encode the sandbox request")
	}
	start := &vmmdpb.SandboxCallStart{Instance: c.InstanceID, SandboxId: c.SandboxID, Request: body}
	if err := stream.Send(&vmmdpb.SandboxCallRequest{Message: &vmmdpb.SandboxCallRequest_Start{Start: start}}); err != nil {
		return nil, callProblem(err)
	}
	// The upload is sent before the answer is read, so the request body is
	// never read after the handler returns; an early guest refusal ends the
	// stream, which stops the upload.
	sendInput(stream, upload)
	for {
		ev, err := stream.Recv()
		if err != nil {
			return nil, callProblem(err)
		}
		f := ev.GetFrame()
		switch f.GetType() {
		case sandboxproto.FrameResult:
			return f.GetBody(), nil
		case sandboxproto.FrameError:
			return nil, guestProblem(f.GetBody())
		default:
			if onFrame != nil {
				if err := onFrame(f.GetType(), f.GetBody()); err != nil {
					return nil, api.ErrInternal("the response could not be written")
				}
			}
		}
	}
}

// sendInput streams upload as data frames and a data-end frame, then ends
// the caller's side of the stream.
func sendInput(stream vmmdpb.Vmmd_SandboxCallClient, upload io.Reader) {
	defer func() { _ = stream.CloseSend() }()
	if upload == nil {
		return
	}
	buf := make([]byte, sandboxproto.ChunkBytes)
	for {
		n, err := upload.Read(buf)
		if n > 0 {
			frame := &vmmdpb.SandboxFrame{Type: sandboxproto.FrameData, Body: append([]byte(nil), buf[:n]...)}
			if stream.Send(&vmmdpb.SandboxCallRequest{Message: &vmmdpb.SandboxCallRequest_Frame{Frame: frame}}) != nil {
				return
			}
		}
		if errors.Is(err, io.EOF) {
			end := &vmmdpb.SandboxFrame{Type: sandboxproto.FrameDataEnd}
			_ = stream.Send(&vmmdpb.SandboxCallRequest{Message: &vmmdpb.SandboxCallRequest_Frame{Frame: end}})
			return
		}
		if err != nil {
			return
		}
	}
}

// callProblem maps a SandboxCall failure.
func callProblem(err error) *api.Problem {
	switch status.Code(err) {
	case codes.NotFound:
		return api.NewProblem(http.StatusNotFound, api.CodeSandboxNotFound, "Sandbox not found", "the session has ended or does not run here")
	case codes.InvalidArgument:
		return api.ErrValidation(status.Convert(err).Message())
	case codes.Canceled, codes.DeadlineExceeded:
		return api.NewProblem(http.StatusGatewayTimeout, api.CodeInternal, "Sandbox call interrupted", "the call ended before the session answered")
	}
	return api.NewProblem(http.StatusServiceUnavailable, api.CodeInternal, "Sandbox unavailable", "the session could not take the call; retry")
}

// guestRefusals maps each guest refusal to its status and problem code.
var guestRefusals = map[string]struct {
	status int
	code   string
}{
	sandboxproto.CodeInvalidRequest:   {http.StatusBadRequest, api.CodeValidation},
	sandboxproto.CodeNotFound:         {http.StatusNotFound, api.CodeSandboxEntryNotFound},
	sandboxproto.CodeExists:           {http.StatusConflict, api.CodeSandboxPathExists},
	sandboxproto.CodeOutsideWorkspace: {http.StatusForbidden, api.CodeSandboxOutsideWorkspace},
	sandboxproto.CodeNotADirectory:    {http.StatusBadRequest, api.CodeSandboxNotADirectory},
	sandboxproto.CodeIsADirectory:     {http.StatusBadRequest, api.CodeSandboxIsADirectory},
	sandboxproto.CodeNotEmpty:         {http.StatusConflict, api.CodeSandboxDirectoryNotEmpty},
	sandboxproto.CodeTooLarge:         {http.StatusRequestEntityTooLarge, api.CodeSandboxTooLarge},
	sandboxproto.CodeLimit:            {http.StatusTooManyRequests, api.CodeSandboxBusy},
	sandboxproto.CodeUnsupported:      {http.StatusUnprocessableEntity, api.CodeSandboxUnsupported},
	sandboxproto.CodeStartFailed:      {http.StatusUnprocessableEntity, api.CodeSandboxCommandStartFailed},
}

// guestProblem maps the guest's error frame; a guest failure that is not a
// refusal is the platform's.
func guestProblem(body []byte) *api.Problem {
	var e sandboxproto.ErrorFrame
	if err := sandboxproto.DecodeStrict(body, &e); err != nil {
		return api.NewProblem(http.StatusBadGateway, api.CodeInternal, "Sandbox failed", "the session sent an unreadable error")
	}
	r, ok := guestRefusals[e.Code]
	if !ok {
		return api.NewProblem(http.StatusBadGateway, api.CodeInternal, "Sandbox failed", "the session could not complete the call")
	}
	return api.NewProblem(r.status, r.code, "Sandbox refused the call", e.Message)
}

// limitProblem refuses a value over the plan cap the token carries.
func limitProblem(field string, max, observed int64) *api.Problem {
	return api.NewProblem(http.StatusForbidden, api.CodeAgentLimit, "Sandbox limit reached",
		fmt.Sprintf("%s is %d; the plan allows at most %d", field, observed, max)).WithLimit(max, observed)
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}
