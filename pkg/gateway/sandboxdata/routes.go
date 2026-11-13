package sandboxdata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
	"github.com/onebox-faas/faas/pkg/sandboxtoken"
)

// execBody is POST /v1/sandboxes/{id}/exec. cmd is a shell command line or
// an argument vector.
type execBody struct {
	Cmd            json.RawMessage   `json:"cmd"`
	Workdir        string            `json:"workdir,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TTY            bool              `json:"tty,omitempty"`
	Background     bool              `json:"background,omitempty"`
	TimeoutMillis  int64             `json:"timeout_ms,omitempty"`
	MaxOutputBytes int64             `json:"max_output_bytes,omitempty"`
	RunAs          string            `json:"run_as,omitempty"`
}

// ExecResponse answers a foreground command.
type ExecResponse struct {
	Stdout          string  `json:"stdout"`
	Stderr          string  `json:"stderr"`
	ExitCode        int     `json:"exit_code"`
	Signal          int     `json:"signal,omitempty"`
	TimedOut        bool    `json:"timed_out,omitempty"`
	Killed          bool    `json:"killed,omitempty"`
	OutputTruncated bool    `json:"output_truncated,omitempty"`
	WallTimeSeconds float64 `json:"wall_time_s"`
}

func (h *Handler) exec(w http.ResponseWriter, r *http.Request) {
	c, ok := h.claims(w, r)
	if !ok {
		return
	}
	var body execBody
	if !httpkit.DecodeJSONLimit(w, r, &body, requestMaxBytes) {
		return
	}
	req, prob := execRequest(c, body)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	if req.Background {
		raw, prob := h.call(r.Context(), c, req, nil, nil)
		writeResult(w, http.StatusAccepted, raw, prob)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		h.execStream(w, r, c, req)
		return
	}
	var stdout, stderr bytes.Buffer
	raw, prob := h.call(r.Context(), c, req, nil, func(frameType uint32, chunk []byte) error {
		if frameType == sandboxproto.FrameStderr {
			stderr.Write(chunk)
		} else {
			stdout.Write(chunk)
		}
		return nil
	})
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, execResponse(raw, stdout.Bytes(), stderr.Bytes()))
}

// execStream answers a foreground command as server-sent events: stdout and
// stderr as they arrive, then one result event.
func (h *Handler) execStream(w http.ResponseWriter, r *http.Request, c sandboxtoken.Claims, req sandboxproto.Request) {
	flusher, _ := w.(http.Flusher)
	started := false
	event := func(name string, v any) error {
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			started = true
		}
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}
	raw, prob := h.call(r.Context(), c, req, nil, func(frameType uint32, chunk []byte) error {
		name := "stdout"
		if frameType == sandboxproto.FrameStderr {
			name = "stderr"
		}
		return event(name, text(chunk))
	})
	switch {
	case prob != nil && !started:
		api.WriteProblem(w, prob)
	case prob != nil:
		_ = event("error", prob)
	default:
		_ = event("result", execResponse(raw, nil, nil))
	}
}

// execRequest is the guest request for a call, within the token's caps.
func execRequest(c sandboxtoken.Claims, body execBody) (sandboxproto.Request, *api.Problem) {
	req := sandboxproto.Request{Op: sandboxproto.OpExec, Workdir: body.Workdir, Env: body.Env, Background: body.Background, RunAs: body.RunAs}
	var line string
	if err := json.Unmarshal(body.Cmd, &line); err == nil {
		req.Argv = []string{"/bin/sh", "-c", line}
	} else if err := json.Unmarshal(body.Cmd, &req.Argv); err != nil || len(req.Argv) == 0 {
		return req, api.ErrValidation("cmd must be a command line or a non-empty array of arguments")
	}
	if body.TTY {
		return req, api.ErrSandboxUnsupported("tty", "terminal commands need the session's interactive attach, which is not available yet")
	}
	req.TimeoutMillis, req.MaxOutputBytes = min(defaultExecTimeoutMillis, c.ExecTimeoutMaxMillis), c.ExecOutputMaxBytes
	if body.TimeoutMillis > 0 {
		req.TimeoutMillis = body.TimeoutMillis
	}
	if body.MaxOutputBytes > 0 {
		req.MaxOutputBytes = body.MaxOutputBytes
	}
	switch {
	case req.TimeoutMillis > c.ExecTimeoutMaxMillis:
		return req, limitProblem("timeout_ms", c.ExecTimeoutMaxMillis, req.TimeoutMillis)
	case req.MaxOutputBytes > c.ExecOutputMaxBytes:
		return req, limitProblem("max_output_bytes", c.ExecOutputMaxBytes, req.MaxOutputBytes)
	}
	return req, nil
}

func execResponse(raw json.RawMessage, stdout, stderr []byte) ExecResponse {
	var res sandboxproto.ExecResult
	_ = json.Unmarshal(raw, &res)
	return ExecResponse{Stdout: text(stdout), Stderr: text(stderr), ExitCode: res.ExitCode, Signal: res.Signal, TimedOut: res.TimedOut,
		Killed: res.Killed, OutputTruncated: res.OutputTruncated, WallTimeSeconds: float64(res.WallTimeMillis) / 1000}
}

// text is output as JSON text; bytes that are not UTF-8 become U+FFFD.
func text(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "\uFFFD")
}

func (h *Handler) execList(w http.ResponseWriter, r *http.Request) {
	if c, ok := h.claims(w, r); ok {
		h.run(w, r, c, sandboxproto.Request{Op: sandboxproto.OpExecList})
	}
}

// execOutput answers a background command's output since the query's
// stdout_offset and stderr_offset.
func (h *Handler) execOutput(w http.ResponseWriter, r *http.Request) {
	c, ok := h.claims(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	req := sandboxproto.Request{Op: sandboxproto.OpExecOutput, Handle: r.PathValue("handle")}
	for _, f := range []struct {
		name string
		dst  *int64
	}{{"stdout_offset", &req.StdoutOffset}, {"stderr_offset", &req.StderrOffset}, {"max_bytes", &req.MaxBytes}} {
		if raw := q.Get(f.name); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				api.WriteProblem(w, api.ErrValidation(f.name+" must be a number"))
				return
			}
			*f.dst = n
		}
	}
	h.run(w, r, c, req)
}

func (h *Handler) execKill(w http.ResponseWriter, r *http.Request) {
	if c, ok := h.claims(w, r); ok {
		h.run(w, r, c, sandboxproto.Request{Op: sandboxproto.OpExecKill, Handle: r.PathValue("handle"), Signal: r.URL.Query().Get("signal")})
	}
}

// fileRead streams a file's contents. A failure after the first byte aborts
// the response, so a partial body is never taken for the file.
func (h *Handler) fileRead(w http.ResponseWriter, r *http.Request) {
	c, ok := h.claims(w, r)
	if !ok {
		return
	}
	req := sandboxproto.Request{Op: sandboxproto.OpFileRead, Path: r.URL.Query().Get("path")}
	started := false
	_, prob := h.call(r.Context(), c, req, nil, func(_ uint32, chunk []byte) error {
		if !started {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(http.StatusOK)
			started = true
		}
		_, err := w.Write(chunk)
		return err
	})
	switch {
	case prob != nil && !started:
		api.WriteProblem(w, prob)
	case prob != nil:
		panic(http.ErrAbortHandler)
	case !started:
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
	}
}

// fileWrite replaces a file with the request body.
func (h *Handler) fileWrite(w http.ResponseWriter, r *http.Request) {
	c, ok := h.claims(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	req := sandboxproto.Request{Op: sandboxproto.OpFileWrite, Path: q.Get("path"), Parents: q.Get("parents") == "true"}
	if raw := q.Get("mode"); raw != "" {
		mode, err := strconv.ParseUint(raw, 8, 32)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("mode must be octal permission bits"))
			return
		}
		req.Mode = uint32(mode)
	}
	body := http.MaxBytesReader(w, r.Body, sandboxproto.MaxWriteBytes)
	raw, prob := h.call(r.Context(), c, req, body, nil)
	writeResult(w, http.StatusOK, raw, prob)
}

func (h *Handler) fileList(w http.ResponseWriter, r *http.Request) {
	c, ok := h.claims(w, r)
	if !ok {
		return
	}
	req := sandboxproto.Request{Op: sandboxproto.OpFileList, Path: r.URL.Query().Get("path")}
	if raw := r.URL.Query().Get("depth"); raw != "" {
		depth, err := strconv.Atoi(raw)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("depth must be a number"))
			return
		}
		req.Depth = depth
	}
	h.run(w, r, c, req)
}

func (h *Handler) fileStat(w http.ResponseWriter, r *http.Request) {
	if c, ok := h.claims(w, r); ok {
		h.run(w, r, c, sandboxproto.Request{Op: sandboxproto.OpFileStat, Path: r.URL.Query().Get("path")})
	}
}

// changeBody is files:remove, files:mkdir and files:rename.
type changeBody struct {
	Path      string `json:"path"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
	Parents   bool   `json:"parents,omitempty"`
	Mode      uint32 `json:"mode,omitempty"`
}

func (h *Handler) fileChange(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := h.claims(w, r)
		if !ok {
			return
		}
		var body changeBody
		if !httpkit.DecodeJSONLimit(w, r, &body, requestMaxBytes) {
			return
		}
		req := sandboxproto.Request{Op: op, Path: body.Path, Recursive: body.Recursive, Parents: body.Parents, Mode: body.Mode}
		if op == sandboxproto.OpFileRename {
			req.Path, req.To = body.From, body.To
		}
		h.run(w, r, c, req)
	}
}

// run runs an operation whose answer is its result.
func (h *Handler) run(w http.ResponseWriter, r *http.Request, c sandboxtoken.Claims, req sandboxproto.Request) {
	raw, prob := h.call(r.Context(), c, req, nil, nil)
	writeResult(w, http.StatusOK, raw, prob)
}

func writeResult(w http.ResponseWriter, code int, raw json.RawMessage, prob *api.Problem) {
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(raw)
}
