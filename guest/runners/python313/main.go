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
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/guest/runners/internal"
	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
)

// envelope is the §4.9 request envelope (internal.Envelope), named here so
// the JSON-tag parity test can pin its wire spelling.
type envelope = internal.Envelope

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
	// = JSONL pipe). Empty values = feature disabled;
	// internal.Serve drains the pipe.
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

// handle runs one request through the customer's handler via the shared
// runner core (internal.Serve): a complete §4.9 response, or a streamed one
// flushed frame by frame (ADR-999).
func handle(w http.ResponseWriter, r *http.Request, handlerPath string, signal *internal.RunnerSignal, tailWaitSec int, tailPipePath string) {
	internal.Serve(w, r, internal.ServeConfig{
		Runtime: "python313",
		Spec: workerpool.Spec{
			Executable: pythonBinary(), Args: []string{handlerPath},
			Env: append(os.Environ(), "FAAS_RUNTIME="+"python313"), HandlerPath: handlerPath,
		},
		WaitUntilSec: tailWaitSec,
		TailPipePath: tailPipePath,
		Signal:       signal,
	})
}

func pythonBinary() string {
	if path, err := exec.LookPath("python3"); err == nil {
		return path
	}
	return "/usr/local/bin/python3"
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
