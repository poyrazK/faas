// node22 runner — hosts the customer's Node handler behind the §4.9
// envelope contract. Reads --handler <path.js>, --runtime <node22>,
// serves :8080. /healthz returns 200 unconditionally (the runner itself
// is up; the handler is the customer's responsibility).
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
	runtime := flag.String("runtime", "node22", "runtime id (informational)")
	handlerPath := flag.String("handler", "/app/node22.js", "path to customer handler")
	flag.Parse()
	if *runtime != "node22" {
		log.Printf("warning: --runtime=%s ignored; only node22 is supported by this binary", *runtime)
	}
	if _, err := os.Stat(*handlerPath); err != nil {
		log.Fatalf("node22 runner: handler not found at %s: %v", *handlerPath, err)
	}
	execution := internal.QualifiedExecutionRuntime()
	prewarmCtx, cancelPrewarm := context.WithTimeout(context.Background(), 30*time.Second)
	if _, err := workerpool.PrewarmIfSupported(prewarmCtx, execution.WorkerSpec(workerpool.Spec{
		Executable: nodeBinary(), Args: []string{*handlerPath},
		Env: append(os.Environ(), "FAAS_RUNTIME=node22"), HandlerPath: *handlerPath,
	})); err != nil {
		if execution != nil {
			cancelPrewarm()
			log.Fatalf("node22 runner: qualified handler prewarm failed: %v", err)
		}
		log.Printf("node22 runner: handler prewarm unavailable: %v", err)
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
	// Issue #470 / PR #470-FU-B: the framework-ready signal fires
	// once per wake when the runner's first non-5xx response lands.
	// The runner's start time is captured here (runner boot ≈
	// guest-init boot; the host sees the wake as the wire start)
	// and the signal is fired from inside the request handler
	// after the handler's response envelope is parsed.
	signal := internal.NewRunnerSignal("node22", time.Now())
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
	log.Printf("node22 runner: serving on %s (handler=%s, h2c-capable for app_protocol∈{http2,grpc})", addr, *handlerPath)
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
// about Node beyond "spawn the binary with the handler path" and "pipe
// the envelope JSON over stdin".
//
// handle runs one request through the customer's handler via the shared
// runner core (internal.Serve): a complete §4.9 response, or a streamed one
// flushed frame by frame (ADR-999).
func handle(w http.ResponseWriter, r *http.Request, handlerPath string, signal *internal.RunnerSignal, tailWaitSec int, tailPipePath string, executions ...*internal.ExecutionRuntime) {
	var execution *internal.ExecutionRuntime
	if len(executions) > 0 {
		execution = executions[0]
	}
	internal.Serve(w, r, internal.ServeConfig{
		Runtime:   "node22",
		Execution: execution,
		Spec: workerpool.Spec{
			Executable: nodeBinary(), Args: []string{handlerPath},
			Env: append(os.Environ(), "FAAS_RUNTIME="+"node22"), HandlerPath: handlerPath,
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
