// python313 runner — Python 3.13 variant of the §4.9 function runner.
// Hosts the customer's Python 3.13 handler behind the envelope
// contract. Mirrors guest/runners/python312/main.go; only the
// runtime id differs — the handler filename stays /app/handler.py
// because Python handlers are already version-neutral on the wire
// (the base image in images/runner-python313.Dockerfile is the
// only version-bearing surface).
//
// Differs from guest/runners/python312/main.go only in:
//   - default --runtime flag value ("python313")
//   - the runtime-mismatch guard text + env var name
//
// §4.9 envelope shape is identical to the python312 runner; the
// payload contract is in pkg/api for any caller that wants to
// validate it.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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
	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
)

type envelope struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Query   string            `json:"query"`
	BodyB64 string            `json:"body_b64"`
	// WaitUntilSec + TailPipePath are the waitUntil(post-response
	// tail) primitive fields (issue #667 / ADR-078). Default 0/empty
	// = no tail — backwards-compatible with pre-#667 handlers. The
	// tail host wiring lives in PR 3; PR 2 ships the envelope shape
	// only so the JSON-tag parity test can pin the wire spelling.
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
	runtime := flag.String("runtime", "python313", "runtime id (informational)")
	handlerPath := flag.String("handler", "/app/handler.py", "path to customer handler")
	flag.Parse()
	if *runtime != "python313" {
		log.Printf("warning: --runtime=%s ignored; only python313 is supported by this binary", *runtime)
	}
	if _, err := os.Stat(*handlerPath); err != nil {
		log.Fatalf("python313 runner: handler not found at %s: %v", *handlerPath, err)
	}
	prewarmCtx, cancelPrewarm := context.WithTimeout(context.Background(), 30*time.Second)
	if _, err := workerpool.PrewarmIfSupported(prewarmCtx, workerpool.Spec{
		Executable: pythonBinary(), Args: []string{*handlerPath},
		Env: append(os.Environ(), "FAAS_RUNTIME=python313"), HandlerPath: *handlerPath,
	}); err != nil {
		log.Printf("python313 runner: handler prewarm unavailable: %v", err)
	}
	cancelPrewarm()

	// Issue #667 / ADR-078 (PR 3): per-request tail primitive
	// knobs (FAAS_TAIL_WAIT_SEC = per-task ceiling, FAAS_TAIL_PIPE_PATH
	// = JSONL pipe). Empty values = feature disabled. See
	// tail_host_integration.go.
	tailWaitSec := envIntDefault("FAAS_TAIL_WAIT_SEC", 0)
	tailPipePath := os.Getenv("FAAS_TAIL_PIPE_PATH")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// Issue #470 / PR #470-FU-B: framework_ready signal fires once
	// per wake when the runner's first non-5xx response lands.
	signal := internal.NewRunnerSignal("python313", time.Now())
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
	log.Printf("python313 runner: serving on %s (handler=%s, h2c-capable for app_protocol∈{http2,grpc})", addr, *handlerPath)
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

	resp, err := invokeHandler(r.Context(), handlerPath, env)
	if err != nil {
		log.Printf("python313 runner: handler error: %v", err)
		http.Error(w, "handler error", http.StatusInternalServerError)
		return
	}
	// Issue #667 / ADR-078 (PR 3): drain the tail pipe before
	// writing the response.
	drainTailHost(r.Context(), env, &resp)
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	if resp.Status == 0 {
		resp.Status = http.StatusOK
	}
	w.WriteHeader(resp.Status)
	if resp.BodyB64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(resp.BodyB64)
		if err != nil {
			log.Printf("python313 runner: bad body_b64: %v", err)
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

func invokeHandler(ctx context.Context, handlerPath string, env envelope) (response, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	binary := pythonBinary()
	handlerEnv := append(os.Environ(), "FAAS_RUNTIME=python313")
	var pooled response
	if handled, err := workerpool.InvokeIfSupported(timeoutCtx, workerpool.Spec{
		Executable: binary, Args: []string{handlerPath}, Env: handlerEnv, HandlerPath: handlerPath,
	}, env, &pooled); handled {
		return pooled, err
	}

	// guest-init deliberately starts workloads with a minimal environment.
	// Keep the PATH seam testable, but fall back to the stable runtime-image
	// path when the guest PATH does not contain the interpreter.
	cmd := exec.CommandContext(timeoutCtx, binary, handlerPath)
	cmd.Env = handlerEnv

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
		return response{}, fmt.Errorf("handler exec: %w (stderr=%s)", err, stderr.String())
	}
	var resp response
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
		return response{}, fmt.Errorf("decode response: %w (stdout=%s)", err, stdout.String())
	}
	return resp, nil
}

func pythonBinary() string {
	if path, err := exec.LookPath("python3"); err == nil {
		return path
	}
	return "/usr/local/bin/python3"
}

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
// (FAAS_TAIL_WAIT_SEC). A malformed value falls back to 0
// (feature disabled), which is the safe default per issue #667
// / ADR-078.
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
