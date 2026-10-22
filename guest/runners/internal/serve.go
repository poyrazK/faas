package internal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
)

// HandlerTimeout is the least time one invocation gets, a streamed response
// included. The request budget the gateway forwards may extend it.
const HandlerTimeout = 30 * time.Second

// invocationTimeout bounds one invocation: the request budget the gateway
// forwarded in X-Faas-Deadline-Ms (ADR-1150), but never less than
// HandlerTimeout, so existing functions keep their 30 s, and never more than
// the longest plan budget.
func invocationTimeout(h http.Header) time.Duration {
	return api.GuestDeadline(h.Get(api.DeadlineHeader), HandlerTimeout)
}

// Envelope is the §4.9 request a runner sends its handler, extended with the
// waitUntil tail primitive (ADR-078). Default 0/empty = no tail.
type Envelope struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Query   string            `json:"query"`
	BodyB64 string            `json:"body_b64"`
	// WaitUntilSec is the per-task ceiling for waitUntil(promise) work the
	// handler registers on TailPipePath; 0 disables the tail.
	WaitUntilSec int    `json:"wait_until_sec"`
	TailPipePath string `json:"tail_pipe_path,omitempty"`
	// LifecycleHook is derived from the socket peer and guest-init's marker,
	// never from the request body or a forwarded client address.
	LifecycleHook     string            `json:"lifecycle_hook,omitempty"`
	Background        *BackgroundPolicy `json:"background,omitempty"`
	ForbidBackground  bool              `json:"forbid_background,omitempty"`
	ResourceLifecycle bool              `json:"resource_lifecycle,omitempty"`
}

// RequiresCompletion participates in the worker-pool capability negotiation.
func (e Envelope) RequiresCompletion() bool { return e.Background != nil }

// Response frames (ADR-999). A line without a frame is a complete §4.9
// response. A "head" line starts a streamed response, "chunk" lines carry its
// body, and "end" or "error" closes it.
const (
	FrameHead       = "head"
	FrameChunk      = "chunk"
	FrameEnd        = "end"
	FrameError      = "error"
	FrameBackground = "background"
	FrameComplete   = "complete"
)

// Response is one line a handler writes on stdout.
type Response struct {
	Frame   string       `json:"frame,omitempty"`
	Status  int          `json:"status"`
	Headers HeaderValues `json:"headers"`
	BodyB64 string       `json:"body_b64"`
	// Message explains an error frame.
	Message string `json:"message,omitempty"`
	// TailErrors is debug-only waitUntil failure detail (ADR-078); it never
	// reaches the HTTP response.
	TailErrors []string         `json:"tail_errors,omitempty"`
	Background *BackgroundFrame `json:"background,omitempty"`
}

// ServeConfig describes one runner's handler.
type ServeConfig struct {
	// Runtime labels guest telemetry, the tail host and log lines.
	Runtime string
	// Spec is the handler command. Handlers speaking the persistent protocol
	// run in a reused worker; others run once per request.
	Spec         workerpool.Spec
	WaitUntilSec int
	TailPipePath string
	Signal       *RunnerSignal
	// Background is shared trusted wiring. Production runners leave it nil
	// until VM registration and checkpoint protection are qualified.
	Background     *BackgroundRuntime
	Execution      *ExecutionRuntime
	executionLease *functionlifetime.Lease
}

// Serve runs one HTTP request through the handler. A complete response is
// written as before; a streamed one sends its status and headers when the
// head frame arrives and flushes every chunk as it comes (ADR-999). After the
// status is sent, a failure can only abort the response, so the client sees a
// truncated body rather than a clean end.
func Serve(w http.ResponseWriter, r *http.Request, cfg ServeConfig) {
	if cfg.Execution != nil {
		cfg.Execution.serve(w, r, cfg)
		return
	}
	env := requestEnvelope(r, cfg)
	ctx, cancel := context.WithTimeout(r.Context(), invocationTimeout(r.Header))
	defer cancel()
	started := time.Now()
	inv, err := invokeWithLease(ctx, cfg, env)
	if err != nil {
		ObserveGuestExecution(r.Context(), cfg.Runtime, started, http.StatusInternalServerError, err).WithProcessUsage(inv.usage).ApplyResponseHeaders(w.Header())
		log.Printf("%s runner: handler error: %v", cfg.Runtime, err)
		WriteHandlerFailure(w, r, err)
		return
	}
	healthy, detached := false, false
	defer func() {
		if !detached {
			inv.close(healthy)
		}
	}()
	if inv.first.Status == 0 {
		inv.first.Status = http.StatusOK
	}
	healthy = writeInvocationResponse(w, r, cfg, env, inv, started)
	if !healthy {
		return
	}
	if cfg.Background != nil {
		inv.detachBackground(r.Context(), cfg.Background)
		detached = true
	}
	if inv.first.Status < 500 && cfg.Signal != nil {
		cfg.Signal.SignalReady(time.Since(cfg.Signal.StartTime()).Milliseconds())
	}
}

func requestEnvelope(r *http.Request, cfg ServeConfig) Envelope {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	env := Envelope{
		Method: r.Method, Path: r.URL.Path, Headers: RequestHeaders(r.Header),
		Query: r.URL.RawQuery, BodyB64: base64.StdEncoding.EncodeToString(body),
		WaitUntilSec: cfg.WaitUntilSec, TailPipePath: cfg.TailPipePath,
		LifecycleHook: lifecycleHook(r),
	}
	if cfg.Background != nil {
		env.Background = &BackgroundPolicy{TimeoutMS: cfg.Background.timeout.Milliseconds(), MaxTasks: cfg.Background.maxTasks}
	}
	return env
}

func writeInvocationResponse(w http.ResponseWriter, r *http.Request, cfg ServeConfig, env Envelope, inv *invocation, started time.Time) bool {
	resp := inv.first
	evidence := ObserveGuestExecution(r.Context(), cfg.Runtime, started, resp.Status, nil).WithProcessUsage(inv.usage)
	if resp.Frame == FrameHead {
		ApplyResponseHeaders(w.Header(), resp.Headers)
		evidence.ApplyResponseHeaders(w.Header())
		w.WriteHeader(resp.Status)
		_ = http.NewResponseController(w).Flush()
		if err := streamBody(w, inv.next); err != nil {
			log.Printf("%s runner: streamed response aborted: %v", cfg.Runtime, err)
			panic(http.ErrAbortHandler)
		}
		if cfg.Background == nil {
			DrainForResponse(r.Context(), cfg.Runtime, env.WaitUntilSec, env.TailPipePath, &resp.TailErrors)
		}
	} else {
		// The legacy pipe contract still drains before a buffered response.
		if cfg.Background == nil {
			DrainForResponse(r.Context(), cfg.Runtime, env.WaitUntilSec, env.TailPipePath, &resp.TailErrors)
		}
		ApplyResponseHeaders(w.Header(), resp.Headers)
		evidence.ApplyResponseHeaders(w.Header())
		w.WriteHeader(resp.Status)
		if resp.BodyB64 != "" {
			decoded, err := base64.StdEncoding.DecodeString(resp.BodyB64)
			if err != nil {
				log.Printf("%s runner: bad body_b64: %v", cfg.Runtime, err)
				return false
			}
			if _, err := w.Write(decoded); err != nil {
				return false
			}
		}
	}
	// All response frames and writes completed. A caller cancelling at this
	// boundary must not cancel post-response work; failures during a stream
	// or write already took the unhealthy path above.
	return true
}

// invocation is a handler's response as it is read: the first line, then the
// frames of a streamed response.
type invocation struct {
	first       Response
	usage       GuestProcessUsage
	background  *invocationBackground
	next        func() ([]byte, error)
	nextContext func(context.Context) ([]byte, error)
	released    <-chan struct{}
	close       func(healthy bool)
}

// invoke sends env to a reused worker when the handler speaks the persistent
// protocol, and otherwise runs the handler once with env on stdin.
func invoke(ctx context.Context, spec workerpool.Spec, env Envelope, background *BackgroundRuntime) (*invocation, error) {
	lines, handled, err := workerpool.OpenIfSupported(ctx, spec, env)
	if handled {
		if err != nil {
			return &invocation{}, err
		}
		inv := &invocation{next: lines.Next, nextContext: lines.NextContext, close: lines.Close, released: lines.Released()}
		if background != nil {
			inv.trackBackground(ctx, lines, background, env.Headers[api.InvocationIDHeader])
		}
		line, err := inv.next()
		if err != nil {
			inv.close(false)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return inv, ctxErr
			}
			return inv, fmt.Errorf("workerpool: read response: %w", err)
		}
		if err := json.Unmarshal(line, &inv.first); err != nil {
			inv.close(false)
			return inv, fmt.Errorf("workerpool: decode response: %w", err)
		}
		if inv.first.Frame != "" && inv.first.Frame != FrameHead {
			inv.close(false)
			return inv, fmt.Errorf("workerpool: decode response: unexpected first frame %q", inv.first.Frame)
		}
		return inv, nil
	}
	if background != nil {
		return &invocation{}, errors.New("runner: background work requires a completion-capable persistent adapter")
	}
	return invokeOnce(ctx, spec, env)
}

func decodeResponse(line []byte) (Response, error) {
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return resp, fmt.Errorf("workerpool: decode response: %w", err)
	}
	return resp, nil
}

// invokeOnce runs a handler without the persistent protocol: one process per
// request, stdout buffered whole. stdout is protocol-bearing, so only stderr
// is teed to the runner's stderr, which reaches `gregale logs` (issue #254).
func invokeOnce(ctx context.Context, spec workerpool.Spec, env Envelope) (*invocation, error) {
	cmd := exec.CommandContext(ctx, spec.Executable, spec.Args...)
	cmd.Env = spec.Env
	var stdin bytes.Buffer
	if err := json.NewEncoder(&stdin).Encode(env); err != nil {
		return &invocation{}, fmt.Errorf("encode envelope: %w", err)
	}
	cmd.Stdin = &stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.MultiWriter(&stderr, os.Stderr)

	inv := &invocation{next: func() ([]byte, error) { return nil, io.EOF }, close: func(bool) {}}
	if err := cmd.Run(); err != nil {
		inv.usage = ProcessResourceUsage(cmd.ProcessState)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return inv, fmt.Errorf("handler timeout: %w", context.DeadlineExceeded)
		}
		return inv, fmt.Errorf("handler exec: %w (stderr=%s)", err, stderr.String())
	}
	inv.usage = ProcessResourceUsage(cmd.ProcessState)
	// A complete response may span lines (pretty-printed JSON), so decode
	// the whole output first; only a streamed response is line-framed.
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &inv.first); err == nil && inv.first.Frame == "" {
		return inv, nil
	}
	reader := bufio.NewReader(bytes.NewReader(stdout.Bytes()))
	first, _ := reader.ReadBytes('\n')
	if err := json.Unmarshal(first, &inv.first); err != nil || inv.first.Frame != FrameHead {
		return inv, fmt.Errorf("decode response: not a response or a streamed head (stdout=%s)", stdout.String())
	}
	inv.next = func() ([]byte, error) { return reader.ReadBytes('\n') }
	return inv, nil
}

// streamBody copies the frames after a head frame to w, flushing each chunk,
// until the end frame.
func streamBody(w http.ResponseWriter, next func() ([]byte, error)) error {
	flusher := http.NewResponseController(w)
	for {
		line, err := next()
		if line = bytes.TrimSpace(line); len(line) == 0 {
			if err != nil {
				return fmt.Errorf("read streamed response: %w", err)
			}
			continue
		}
		var frame Response
		if err := json.Unmarshal(line, &frame); err != nil {
			return fmt.Errorf("decode streamed frame: %w", err)
		}
		switch frame.Frame {
		case FrameChunk:
			chunk, err := base64.StdEncoding.DecodeString(frame.BodyB64)
			if err != nil {
				return fmt.Errorf("decode chunk: %w", err)
			}
			if len(chunk) > 0 {
				if _, err := w.Write(chunk); err != nil {
					return fmt.Errorf("write chunk: %w", err)
				}
				_ = flusher.Flush()
			}
		case FrameEnd:
			return nil
		case FrameError:
			return fmt.Errorf("handler failed mid-stream: %s", frame.Message)
		default:
			return fmt.Errorf("unexpected frame %q in a streamed response", frame.Frame)
		}
	}
}
