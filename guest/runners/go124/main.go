// go124 runner — hosts the customer's Go static-binary handler behind the §4.9
// envelope contract. Mirrors guest/runners/python312/main.go; the only
// material difference is the spawn: the customer's handler is a static Go
// binary (Railpack's go plan emits CGO_ENABLED=0 by default), so the runner
// execs the file directly with no interpreter argument. The runner still
// reads --handler <path> for symmetry, but in production the path is locked
// to /app/handler by imaged.handleDeployment (the entrypoint baked into
// the AppManifest). --runtime is informational.
//
// Why two near-identical files: the runner is a tiny static Go binary
// (~80 LOC). Splitting them keeps each one buildable + lintable on its
// own without a runtime-detection shim, and matches the per-runtime
// image split in images/. The shared envelope shape lives in pkg/api
// for any caller that wants to validate it.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/guest/runners/internal"
)

type envelope struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Query   string            `json:"query"`
	BodyB64 string            `json:"body_b64"`
	// WaitUntilSec + TailPipePath are the waitUntil(post-response
	// tail) primitive fields (issue #667 / ADR-078). Default 0/empty
	// = no tail — backwards-compatible with pre-#667 handlers.
	//
	// PR 3 (issue #667 follow-up): the runner-side tail host is
	// wired in tail_host_integration.go::drainTailHost. The drain
	// runs after invokeHandler returns and before the response
	// envelope is written (so the customer's tail_seconds window
	// ends before the response is read).
	//
	// Per-request values (issue #667): WaitUntilSec is the per-task
	// ceiling (Free 5 / Hobby 15 / Pro 30 / Scale 60 seconds),
	// stamped by imaged at build time via the FAAS_TAIL_WAIT_SEC
	// env var; the runner reads the env var on boot and propagates
	// the value to the handler envelope on every request. TailPipePath
	// is the per-request JSONL pipe path (imaged formats it as
	// /tmp/faas-tail-<random>.jsonl per wake, stamped via
	// FAAS_TAIL_PIPE_PATH) — the runner's tail host drains the pipe
	// after invokeHandler returns. Empty pipe / 0 ceiling = feature
	// disabled on this request (backwards-compatible — pre-#667
	// handlers ignore the new fields).
	WaitUntilSec int    `json:"wait_until_sec"`
	TailPipePath string `json:"tail_pipe_path,omitempty"`
}

type response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	BodyB64 string            `json:"body_b64"`
	// TailErrors is the per-task failure list (issue #667).
	// Debug-only — surfaced via runner stderr + schedd audit rows,
	// never via the HTTP response body.
	TailErrors []string `json:"tail_errors,omitempty"`
}

func main() {
	runtime := flag.String("runtime", "go124", "runtime id (informational)")
	handlerPath := flag.String("handler", "/app/handler", "path to customer handler binary")
	flag.Parse()
	if *runtime != "go124" {
		log.Printf("warning: --runtime=%s ignored; only go124 is supported by this binary", *runtime)
	}
	if _, err := os.Stat(*handlerPath); err != nil {
		log.Fatalf("go124 runner: handler not found at %s: %v", *handlerPath, err)
	}

	// Issue #667 / ADR-078 (PR 3): read the per-request tail
	// primitive knobs from env vars stamped by imaged at build time.
	// FAAS_TAIL_WAIT_SEC is the per-task wall-clock ceiling (Free 5 /
	// Hobby 15 / Pro 30 / Scale 60). FAAS_TAIL_PIPE_PATH is the
	// per-request JSONL pipe path the customer's handler appends
	// waitUntil(promise) registrations to. Empty values = feature
	// disabled on this request (backwards-compatible with pre-#667
	// handlers). See tail_host_integration.go.
	tailWaitSec := envIntDefault("FAAS_TAIL_WAIT_SEC", 0)
	tailPipePath := os.Getenv("FAAS_TAIL_PIPE_PATH")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// Issue #470 / PR #470-FU-B: framework_ready signal fires once
	// per wake when the runner's first non-5xx response lands.
	signal := internal.NewRunnerSignal("go124", time.Now())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, *handlerPath, signal, tailWaitSec, tailPipePath)
	})

	// Issue #460 / ADR-053 (PR-C): PORT env var carries the
	// per-deployment override port guest-init stamped onto the
	// exec'd env (see guest/init/main_linux.go::runAppWithEnv).
	// Falls back to 8080 for unit tests + non-PR-C paths.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
	log.Printf("go124 runner: serving on %s (handler=%s, h2c-capable for app_protocol∈{http2,grpc})", addr, *handlerPath)
	// ADR-126 / G19: the runner's :8080 listener opts into H2C
	// prior-knowledge via the shared internal helper. H1 callers
	// (app_protocol=http1, today's default) continue to work
	// unchanged — stdlib selects HTTP/1.1 when the client doesn't
	// open an H2 preface. See guest/runners/internal/h2c_listener.go.
	internal.ListenAndServeH2C(internal.H2CListenerConfig{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	})
}

// handle runs the §4.9 envelope round-trip through the customer's
// handler. The runner is the request translator — it knows nothing
// about Go beyond "exec the file with the handler path" and "pipe
// the envelope JSON over stdin".
//
// Issue #667 / ADR-078 (PR 3): tailWaitSec and tailPipePath are the
// per-request tail primitives stamped by imaged at build time. The
// runner passes them through to the handler envelope (so the handler
// knows the per-task ceiling) and feeds them to drainTailHost after
// the handler returns (so the runner's tail host can drain the
// JSONL pipe and emit 0x04 DGRAMs).
func handle(w http.ResponseWriter, r *http.Request, handlerPath string, signal *internal.RunnerSignal, tailWaitSec int, tailPipePath string) {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	env := envelope{
		Method:       r.Method,
		Path:         r.URL.Path,
		Headers:      headerMap(r.Header),
		Query:        r.URL.RawQuery,
		BodyB64:      base64.StdEncoding.EncodeToString(body),
		WaitUntilSec: tailWaitSec,
		TailPipePath: tailPipePath,
	}

	started := time.Now()
	resp, err := invokeHandler(r.Context(), handlerPath, env)
	if err != nil {
		internal.ObserveGuestExecution(r.Context(), "go124", started, http.StatusInternalServerError, err).ApplyResponseHeaders(w.Header())
		log.Printf("go124 runner: handler error: %v", err)
		http.Error(w, "handler error", http.StatusInternalServerError)
		return
	}
	if resp.Status == 0 {
		resp.Status = http.StatusOK
	}
	evidence := internal.ObserveGuestExecution(r.Context(), "go124", started, resp.Status, nil)
	// Issue #667 / ADR-078 (PR 3): drain the tail pipe before
	// writing the response. The drain runs AFTER invokeHandler
	// returns (the handler has already written to the JSONL
	// pipe) and BEFORE the response envelope is written to the
	// wire + the framework_ready signal fires. The customer's
	// tail_seconds window therefore ends before the response
	// is read; the snapshotAndPark 5s watchdog on schedd is
	// the upper bound if the drain hangs.
	drainTailHost(r.Context(), env, &resp)
	internal.ApplyResponseHeaders(w.Header(), resp.Headers)
	evidence.ApplyResponseHeaders(w.Header())
	w.WriteHeader(resp.Status)
	if resp.BodyB64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(resp.BodyB64)
		if err != nil {
			log.Printf("go124 runner: bad body_b64: %v", err)
			return
		}
		_, _ = w.Write(decoded)
	}
	// Issue #470 / PR #470-FU-B: fire the framework-ready signal
	// after the response has been written. Status < 500 → ready.
	if resp.Status < 500 {
		signal.SignalReady(time.Since(signal.StartTime()).Milliseconds())
	}
}

// invokeHandler spawns the customer's static Go binary at handlerPath
// and pipes the request envelope over stdin; reads the response
// envelope from stdout.
func invokeHandler(ctx context.Context, handlerPath string, env envelope) (response, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, handlerPath)
	cmd.Env = append(os.Environ(), "FAAS_RUNTIME=go124")

	var stdin bytes.Buffer
	if err := json.NewEncoder(&stdin).Encode(env); err != nil {
		return response{}, fmt.Errorf("encode envelope: %w", err)
	}
	cmd.Stdin = &stdin

	// Issue #254: tee customer stderr to os.Stderr so it reaches
	// guest-init's supervisor ring and, from there, `faas logs`.
	// stdout stays a bare buffer — it is protocol-bearing (the §4.9
	// envelope is decoded from it below). See the full rationale at
	// guest/runners/node22/main.go::invokeHandler.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.MultiWriter(&stderr, os.Stderr)

	if err := cmd.Run(); err != nil {
		if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			return response{}, fmt.Errorf("handler timeout: %w", context.DeadlineExceeded)
		}
		return response{}, fmt.Errorf("handler exec: %w (stderr=%s)", err, stderr.String())
	}
	var resp response
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
		return response{}, fmt.Errorf("decode response: %w (stdout=%s)", err, stdout.String())
	}
	return resp, nil
}

// headerMap folds http.Header into the lowercase-string-keyed map the
// §4.9 envelope expects. Multi-value headers are joined with ", ".
//
// Note: the implementation preserves Go's canonical header spellings
// (not lowercased keys) and keeps only v[0] of multi-valued headers —
// the Node22 runner's doc comment is wrong on both points. The new
// runners match this implementation, not the comment.
func headerMap(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

// envIntDefault reads an env var as an int; returns fallback if
// unset or malformed. Used for the per-plan tail primitive knobs
// (FAAS_TAIL_WAIT_SEC etc.) — a malformed value silently falls
// back to 0 (feature disabled), which is the safe-default per
// issue #667 / ADR-078 (timeout 0 = no tail).
func envIntDefault(name string, fallback int) int {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}
