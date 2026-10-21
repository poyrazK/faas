// node24 runner — Node 24 LTS variant of the §4.9 function runner.
// Hosts the customer's Node 24 handler behind the envelope contract,
// reads --handler <path>, --runtime <node24>, serves :8080.
//
// Differs from guest/runners/node22/main.go only in:
//   - default --runtime flag value ("node24" instead of "node22")
//   - default --handler path (/app/node24.js, versioned for clarity)
//   - the runtime-mismatch guard text + env var name
//
// The interpreter binary is the same `node` from the underlying base
// image — versions vary by images/runner-node24.Dockerfile (PR 2 of
// Tier 1), not by this Go binary. The split-by-runtime-bin's pattern
// exists so each runner can be wired independently by imaged and so a
// future opt-out (e.g. dropping a runtime) deletes one binary, not a
// shared one with conditional logic.
//
// §4.9 envelope (request):
//
//	{ "method":"POST", "path":"/foo", "headers":{...},
//	  "query":"a=1&b=2", "body_b64":"SGVsbG8=" }
//
// §4.9 envelope (response):
//
//	{ "status":200, "headers":{...}, "body_b64":"..." }
//
// Generated adapters advertise FAAS_PERSISTENT_PROTOCOL_V1 and stay alive
// across requests, including their preloaded customer module. Legacy custom
// protocol handlers retain the one-process-per-request fallback below.
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
	runtime := flag.String("runtime", "node24", "runtime id (informational)")
	handlerPath := flag.String("handler", "/app/node24.js", "path to customer handler")
	flag.Parse()
	if *runtime != "node24" {
		log.Printf("warning: --runtime=%s ignored; only node24 is supported by this binary", *runtime)
	}
	if _, err := os.Stat(*handlerPath); err != nil {
		log.Fatalf("node24 runner: handler not found at %s: %v", *handlerPath, err)
	}
	execution := internal.QualifiedExecutionRuntime()
	prewarmCtx, cancelPrewarm := context.WithTimeout(context.Background(), 30*time.Second)
	if _, err := workerpool.PrewarmIfSupported(prewarmCtx, execution.WorkerSpec(workerpool.Spec{
		Executable: nodeBinary(), Args: []string{*handlerPath},
		Env: append(os.Environ(), "FAAS_RUNTIME=node24"), HandlerPath: *handlerPath,
	})); err != nil {
		if execution != nil {
			cancelPrewarm()
			log.Fatalf("node24 runner: qualified handler prewarm failed: %v", err)
		}
		log.Printf("node24 runner: handler prewarm unavailable: %v", err)
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
	signal := internal.NewRunnerSignal("node24", time.Now())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, *handlerPath, signal, tailWaitSec, tailPipePath, execution)
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
	log.Printf("node24 runner: serving on %s (handler=%s, h2c-capable for app_protocol∈{http2,grpc})", addr, *handlerPath)
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
func handle(w http.ResponseWriter, r *http.Request, handlerPath string, signal *internal.RunnerSignal, tailWaitSec int, tailPipePath string, executions ...*internal.ExecutionRuntime) {
	var execution *internal.ExecutionRuntime
	if len(executions) > 0 {
		execution = executions[0]
	}
	internal.Serve(w, r, internal.ServeConfig{
		Runtime:   "node24",
		Execution: execution,
		Spec: workerpool.Spec{
			Executable: nodeBinary(), Args: []string{handlerPath},
			Env: append(os.Environ(), "FAAS_RUNTIME="+"node24"), HandlerPath: handlerPath,
		},
		WaitUntilSec: tailWaitSec,
		TailPipePath: tailPipePath,
		Signal:       signal,
	})
}

func nodeBinary() string {
	if path, err := exec.LookPath("node"); err == nil {
		return path
	}
	return "/usr/local/bin/node"
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
