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
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/guest/runners/internal"
	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
)

// reportedRuntime is the runtime this runner reports in telemetry, to the
// restore barrier and to the handler (FAAS_RUNTIME). go124-alpine reports as
// go124, as before; binary keeps its own identity (ADR-987).
var reportedRuntime = "go124"

// envelope is the §4.9 request envelope (internal.Envelope), named here so
// the JSON-tag parity test can pin its wire spelling.
type envelope = internal.Envelope

func main() {
	runtime := flag.String("runtime", "go124", "runtime id: go124, go124-alpine or binary")
	handlerPath := flag.String("handler", "/app/handler", "path to customer handler binary")
	flag.Parse()
	switch *runtime {
	case "binary":
		// ADR-987: the binary runtime shares this runner and reports its
		// own identity in telemetry and to the handler.
		reportedRuntime = "binary"
	case "go124", "go124-alpine":
	default:
		log.Printf("warning: --runtime=%s ignored; this runner serves go124, go124-alpine and binary", *runtime)
	}
	if _, err := os.Stat(*handlerPath); err != nil {
		log.Fatalf("go124 runner: handler not found at %s: %v", *handlerPath, err)
	}
	// Handlers built on the persistent protocol stay alive between
	// requests. Starting one here puts the initialized process into the
	// init snapshot, so a restored VM skips process start and the
	// handler's own setup on its first request.
	prewarmCtx, cancelPrewarm := context.WithTimeout(context.Background(), 30*time.Second)
	persistent, err := workerpool.PrewarmIfSupported(prewarmCtx, handlerSpec(*handlerPath))
	if err != nil {
		log.Printf("go124 runner: handler prewarm unavailable: %v", err)
	}
	cancelPrewarm()
	if persistent {
		// A restored handler would resume with the in-process random state
		// it held at capture, shared with every sibling restored from the
		// same snapshot (GHSA-24j2-p895-mwc9, ADR-687). Compiled handlers
		// have no reseed preload, so on every resume the runner replaces
		// them before the instance serves traffic; a Go process starts in
		// milliseconds, and the replacement starts in the background.
		internal.RegisterRestoreRecycle(reportedRuntime, func() error {
			workerpool.Recycle()
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if _, err := workerpool.PrewarmIfSupported(ctx, handlerSpec(*handlerPath)); err != nil {
					log.Printf("go124 runner: handler restart after restore: %v", err)
				}
			}()
			return nil
		})
	}

	// Issue #667 / ADR-078 (PR 3): read the per-request tail
	// primitive knobs from env vars stamped by imaged at build time.
	// FAAS_TAIL_WAIT_SEC is the per-task wall-clock ceiling (Free 5 /
	// Hobby 15 / Pro 30 / Scale 60). FAAS_TAIL_PIPE_PATH is the
	// per-request JSONL pipe path the customer's handler appends
	// waitUntil(promise) registrations to. Empty values = feature
	// disabled on this request (backwards-compatible with pre-#667
	// handlers); internal.Serve drains the pipe.
	tailWaitSec := envIntDefault("FAAS_TAIL_WAIT_SEC", 0)
	tailPipePath := os.Getenv("FAAS_TAIL_PIPE_PATH")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// Issue #470 / PR #470-FU-B: framework_ready signal fires once
	// per wake when the runner's first non-5xx response lands.
	signal := internal.NewRunnerSignal(reportedRuntime, time.Now())
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

// handle runs one request through the customer's binary via the shared
// runner core (internal.Serve): a complete §4.9 response, or a streamed one
// flushed frame by frame (ADR-999).
func handle(w http.ResponseWriter, r *http.Request, handlerPath string, signal *internal.RunnerSignal, tailWaitSec int, tailPipePath string) {
	internal.Serve(w, r, internal.ServeConfig{
		Runtime:      reportedRuntime,
		Spec:         handlerSpec(handlerPath),
		WaitUntilSec: tailWaitSec,
		TailPipePath: tailPipePath,
		Signal:       signal,
	})
}

// handlerSpec describes the customer's binary to the worker pool. The
// binary is executed directly; its persistent-protocol marker sits in
// the compiled data section, so the pool scans the whole file.
func handlerSpec(handlerPath string) workerpool.Spec {
	return workerpool.Spec{
		Executable:     handlerPath,
		Env:            append(os.Environ(), "FAAS_RUNTIME="+reportedRuntime),
		HandlerPath:    handlerPath,
		MarkerAnywhere: true,
	}
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
