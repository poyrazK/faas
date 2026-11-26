package sandboxdata

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

// The code interpreter's routes (ADR-20261027090013 step 14). They need a
// session booted from an image with the driver, the code-interpreter
// preset; any other session answers 422 sandbox_unsupported.

// codeLanguages are the languages a code context can run.
var codeLanguages = map[string]bool{"python": true}

// codeRunBody is POST /v1/sandboxes/{id}/code.
type codeRunBody struct {
	Code          string `json:"code"`
	Context       string `json:"context,omitempty"`
	Language      string `json:"language,omitempty"`
	TimeoutMillis int64  `json:"timeout_ms,omitempty"`
}

// CodeLogs are a run's stdout and stderr, in the chunks they came in.
type CodeLogs struct {
	Stdout []string `json:"stdout"`
	Stderr []string `json:"stderr"`
}

// CodeExecution answers a run: its rich results, its logs, the error it
// raised, and its execution count.
type CodeExecution struct {
	Results         []sandboxproto.CodeOutput `json:"results"`
	Logs            CodeLogs                  `json:"logs"`
	Error           *sandboxproto.CodeError   `json:"error,omitempty"`
	ExecutionCount  *int                      `json:"execution_count,omitempty"`
	TimedOut        bool                      `json:"timed_out,omitempty"`
	OutputTruncated bool                      `json:"output_truncated,omitempty"`
}

func codeExecution(raw json.RawMessage, logs CodeLogs) CodeExecution {
	var res sandboxproto.CodeRunResult
	_ = json.Unmarshal(raw, &res)
	if res.Results == nil {
		res.Results = []sandboxproto.CodeOutput{}
	}
	if logs.Stdout == nil {
		logs.Stdout = []string{}
	}
	if logs.Stderr == nil {
		logs.Stderr = []string{}
	}
	return CodeExecution{Results: res.Results, Logs: logs, Error: res.Error, ExecutionCount: res.ExecutionCount,
		TimedOut: res.TimedOut, OutputTruncated: res.OutputTruncated}
}

// codeRun runs code in a context; with Accept: text/event-stream its stdout
// and stderr stream before the execution.
func (h *Handler) codeRun(w http.ResponseWriter, r *http.Request) {
	c, ok := h.claims(w, r)
	if !ok {
		return
	}
	var body codeRunBody
	if !httpkit.DecodeJSONLimit(w, r, &body, inputRequestMaxBytes) {
		return
	}
	if body.Language != "" && !codeLanguages[body.Language] {
		api.WriteProblem(w, api.ErrSandboxUnsupported("language", "code contexts run python"))
		return
	}
	req := sandboxproto.Request{Op: sandboxproto.OpCodeRun, Code: body.Code, Context: body.Context,
		TimeoutMillis: min(defaultExecTimeoutMillis, c.ExecTimeoutMaxMillis), MaxOutputBytes: c.ExecOutputMaxBytes}
	if body.TimeoutMillis > 0 {
		req.TimeoutMillis = body.TimeoutMillis
	}
	if req.TimeoutMillis > c.ExecTimeoutMaxMillis {
		api.WriteProblem(w, limitProblem("timeout_ms", c.ExecTimeoutMaxMillis, req.TimeoutMillis))
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		h.streamCall(w, r, c, req, func(raw json.RawMessage) any { return codeExecution(raw, CodeLogs{}) })
		return
	}
	var logs CodeLogs
	raw, prob := h.call(r.Context(), c, req, nil, func(frameType uint32, chunk []byte) error {
		if frameType == sandboxproto.FrameStderr {
			logs.Stderr = append(logs.Stderr, text(chunk))
		} else {
			logs.Stdout = append(logs.Stdout, text(chunk))
		}
		return nil
	})
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, codeExecution(raw, logs))
}

// codeContextBody is POST /v1/sandboxes/{id}/code/contexts.
type codeContextBody struct {
	Language string `json:"language,omitempty"`
	Cwd      string `json:"cwd,omitempty"`
}

func (h *Handler) codeContextCreate(w http.ResponseWriter, r *http.Request) {
	c, ok := h.claims(w, r)
	if !ok {
		return
	}
	var body codeContextBody
	if !httpkit.DecodeJSONLimit(w, r, &body, requestMaxBytes) {
		return
	}
	if body.Language != "" && !codeLanguages[body.Language] {
		api.WriteProblem(w, api.ErrSandboxUnsupported("language", "code contexts run python"))
		return
	}
	raw, prob := h.call(r.Context(), c, sandboxproto.Request{Op: sandboxproto.OpCodeContext, Action: sandboxproto.CodeActionCreate,
		Language: body.Language, Workdir: body.Cwd}, nil, nil)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	var res sandboxproto.CodeContextResult
	_ = json.Unmarshal(raw, &res)
	httpkit.WriteJSON(w, http.StatusCreated, res.Context)
}

func (h *Handler) codeContextList(w http.ResponseWriter, r *http.Request) {
	c, ok := h.claims(w, r)
	if !ok {
		return
	}
	raw, prob := h.call(r.Context(), c, sandboxproto.Request{Op: sandboxproto.OpCodeContext, Action: sandboxproto.CodeActionList}, nil, nil)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	var res sandboxproto.CodeContextResult
	_ = json.Unmarshal(raw, &res)
	if res.Contexts == nil {
		res.Contexts = []sandboxproto.CodeContext{}
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]any{"contexts": res.Contexts})
}

// codeContextAction restarts or removes a context.
func (h *Handler) codeContextAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := h.claims(w, r)
		if !ok {
			return
		}
		if _, prob := h.call(r.Context(), c, sandboxproto.Request{Op: sandboxproto.OpCodeContext, Action: action,
			Context: r.PathValue("context")}, nil, nil); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, struct{}{})
	}
}
