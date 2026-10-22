// projectworker is the native ADR-20261019120008 acceptance workload. It has no network
// dependencies and runs as either the HTTP app or its push-queue worker.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/e2etest/migrationprobe"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

type observation struct {
	DeploymentID                    string         `json:"deployment_id,omitempty"`
	InstanceID                      string         `json:"instance_id,omitempty"`
	BackendDeploymentID             string         `json:"backend_deployment_id,omitempty"`
	BackendInstanceID               string         `json:"backend_instance_id,omitempty"`
	BackendDestinationSecretMatches bool           `json:"backend_destination_secret_matches"`
	BindingTransport                string         `json:"binding_transport,omitempty"`
	DestinationSecretMatches        bool           `json:"destination_secret_matches"`
	SecretMatchesGeneration         bool           `json:"secret_matches_generation"`
	BackendGeneration               string         `json:"backend_generation,omitempty"`
	BackendMode                     string         `json:"backend_mode,omitempty"`
	ReleaseID                       string         `json:"release_id,omitempty"`
	BackendReleaseID                string         `json:"backend_release_id,omitempty"`
	Generation                      string         `json:"generation"`
	Mode                            string         `json:"mode"`
	Received                        map[string]int `json:"received"`
	Completed                       map[string]int `json:"completed"`
	Active                          int            `json:"active"`
	Stopping                        bool           `json:"stopping"`
	IgnoreStop                      bool           `json:"ignore_stop"`
	SQLValue                        string         `json:"sql_value,omitempty"`
	SQLWrites                       int64          `json:"sql_writes,omitempty"`
}

type workload struct {
	mu         sync.Mutex
	role       string
	report     observation
	regressed  bool
	ignoreStop bool
	crashNext  bool
	failNext   bool
	stop       chan struct{}
}

func newWorkload(role, generation, mode string) *workload {
	return &workload{role: role, report: observation{Generation: generation, Mode: mode,
		Received: map[string]int{}, Completed: map[string]int{}}, stop: make(chan struct{})}
}

func (w *workload) snapshot() observation {
	w.mu.Lock()
	defer w.mu.Unlock()
	r := w.report
	r.DeploymentID, r.InstanceID = os.Getenv("FAAS_DEPLOYMENT_ID"), os.Getenv("FAAS_INSTANCE_ID")
	r.DestinationSecretMatches = os.Getenv("TOKEN") == "promotion-production-secret"
	r.SecretMatchesGeneration = os.Getenv("TOKEN") == r.Generation+"-secret"
	r.IgnoreStop = w.ignoreStop
	r.Received, r.Completed = map[string]int{}, map[string]int{}
	for label, count := range w.report.Received {
		r.Received[label] = count
	}
	for label, count := range w.report.Completed {
		r.Completed[label] = count
	}
	return r
}

func (w *workload) beginStop() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ignoreStop {
		return false
	}
	if !w.report.Stopping {
		w.report.Stopping = true
		close(w.stop)
	}
	return true
}

func (w *workload) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if os.Getenv("PROMOTION_SERVICE_GRAPH") == "1" && w.servePromotionGraph(rw, r) {
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/protocol":
		report := w.snapshot()
		if r.Header.Get("X-Fixture-Protocol") != report.Generation {
			http.Error(rw, "incompatible wire generation", http.StatusConflict)
			return
		}
		report.ReleaseID = r.Header.Get("X-Gregale-Release")
		writeObservation(rw, report)
	case r.Method == http.MethodGet && (r.URL.Path == "/state" || r.URL.Path == "/"):
		report := w.snapshot()
		config := migrationprobe.FromEnv(os.Getenv)
		if w.role == "api" && config.Enabled() {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			proof, err := config.Check(ctx, currentSQLGeneration(report.Generation))
			if err != nil {
				http.Error(rw, migrationprobe.ErrProbe.Error(), http.StatusServiceUnavailable)
				return
			}
			report.SQLValue, report.SQLWrites = proof.Value, proof.Writes
		}
		if r.URL.Path == "/" {
			if err := w.downstream(r, &report); err != nil {
				http.Error(rw, err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		writeObservation(rw, report)
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		if w.role == "api" && migrationprobe.FromEnv(os.Getenv).MigrationURI != "" {
			http.Error(rw, migrationprobe.ErrProbe.Error(), http.StatusServiceUnavailable)
			return
		}
		rw.WriteHeader(http.StatusOK)
	case r.Method == http.MethodGet && r.URL.Path == "/critical":
		w.mu.Lock()
		regressed := w.regressed
		w.mu.Unlock()
		config := migrationprobe.FromEnv(os.Getenv)
		if w.role == "api" && config.Enabled() {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			var err error
			if os.Getenv("POSTGRES_PROMOTION_QUALIFICATION") == "1" {
				err = config.Read(ctx, currentSQLGeneration(w.snapshot().Generation))
			} else {
				_, err = config.Check(ctx, currentSQLGeneration(w.snapshot().Generation))
			}
			if err != nil {
				http.Error(rw, migrationprobe.ErrProbe.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		if err := w.downstream(r, newReport(w)); err != nil {
			http.Error(rw, err.Error(), http.StatusServiceUnavailable)
			return
		}
		if os.Getenv("RELEASE_PROOF") == "sealed" && !w.snapshot().SecretMatchesGeneration {
			rw.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if regressed {
			rw.WriteHeader(http.StatusServiceUnavailable)
		} else {
			rw.WriteHeader(http.StatusOK)
		}
	case r.Method == http.MethodPost && r.URL.Path == "/control/regress" && w.role == "api":
		w.mu.Lock()
		w.regressed = true
		w.mu.Unlock()
		rw.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && r.URL.Path == "/control/ignore-stop" && w.role == "worker":
		w.mu.Lock()
		w.ignoreStop = true
		w.mu.Unlock()
		rw.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && r.URL.Path == "/control/crash-next" && w.role == "worker":
		w.mu.Lock()
		w.crashNext = true
		w.mu.Unlock()
		rw.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && r.URL.Path == "/control/fail-next" && w.role == "worker":
		w.mu.Lock()
		w.failNext = true
		w.mu.Unlock()
		rw.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && r.URL.Path == "/" && w.role == "worker":
		w.deliver(rw, r)
	default:
		rw.WriteHeader(http.StatusNotFound)
	}
}

func (w *workload) deliver(rw http.ResponseWriter, r *http.Request) {
	var job struct {
		Label string `json:"label"`
		Hold  bool   `json:"hold"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&job); err != nil || job.Label == "" {
		rw.WriteHeader(http.StatusBadRequest)
		return
	}
	w.mu.Lock()
	if w.failNext {
		w.failNext = false
		w.mu.Unlock()
		rw.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if w.crashNext {
		// Native acceptance arms one process. Its replacement starts unarmed;
		// the actual queue lease/retry owner must recover the unacknowledged job.
		w.mu.Unlock()
		os.Exit(77)
	}
	if w.report.Stopping {
		w.mu.Unlock()
		rw.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.report.Active++
	w.report.Received[job.Label]++
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.report.Active--
		w.mu.Unlock()
	}()
	if job.Hold {
		select {
		case <-w.stop:
		case <-r.Context().Done():
			return
		}
	}
	w.mu.Lock()
	w.report.Completed[job.Label]++
	w.mu.Unlock()
	writeObservation(rw, w.snapshot())
}

func writeObservation(rw http.ResponseWriter, r observation) {
	rw.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(rw).Encode(r); err != nil {
		fmt.Fprintln(os.Stderr, "fixture response:", err)
	}
}

func main() {
	if os.Args[0] == "/bin/sh" {
		if !promotionShellCommand(os.Args) {
			os.Exit(2)
		}
		os.Exit(runMigration("migration-promotion", os.Stdout))
	}
	if len(os.Args) == 2 && strings.HasPrefix(os.Args[1], "migration-") {
		os.Exit(runMigration(os.Args[1], os.Stdout))
	}
	if len(os.Args) != 2 || (os.Args[1] != "api" && os.Args[1] != "worker") {
		fmt.Fprintln(os.Stderr, "usage: projectworker api|worker")
		os.Exit(2)
	}
	raw, err := os.ReadFile("/release-version")
	if err != nil {
		fmt.Fprintln(os.Stderr, "read fixture generation:", err)
		os.Exit(2)
	}
	w := newWorkload(os.Args[1], strings.TrimSpace(string(raw)), os.Getenv("MODE"))
	srv := &http.Server{Addr: ":8080", Handler: w, ReadHeaderTimeout: 5 * time.Second}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(stopped)
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		for range stopped {
			if !w.beginStop() {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			if err := srv.Shutdown(ctx); err != nil {
				fmt.Fprintln(os.Stderr, "fixture shutdown:", err)
			}
			return
		}
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "fixture listen:", err)
		os.Exit(1)
	}
	// Shutdown closes the listener before waiting for in-flight replies.
	<-shutdownDone
}

// The scratch promotion fixture supplies this binary as /bin/sh solely for
// its immutable Procfile command. It cannot evaluate arbitrary shell source.
func promotionShellCommand(args []string) bool {
	return len(args) == 3 && args[0] == "/bin/sh" && args[1] == "-lc" && args[2] == "/projectworker migration-promotion"
}

// Task modes execute inside the native task VM. Output identifies the actual
// artifact and paired configuration; no host-side effect stands in for a guest.
func runMigration(mode string, out io.Writer) int {
	raw, err := os.ReadFile("/release-version")
	if err != nil {
		return 2
	}
	config := migrationprobe.FromEnv(os.Getenv)
	if mode == "migration-promotion" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		err := config.Promote(ctx)
		if errors.Is(err, migrationprobe.ErrPromotionCommand) {
			return 23
		}
		if err != nil {
			return 2
		}
		_, _ = fmt.Fprintf(out, "native promotion expansion generation=%s mode=%s\n", strings.TrimSpace(string(raw)), os.Getenv("MODE"))
		return 0
	}
	if mode == "migration-check-runtime" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := config.Check(ctx, true); err != nil {
			return 2
		}
		return 0
	}
	if mode == "migration-expand" {
		if config.Enabled() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if config.Expand(ctx) != nil {
				return 2
			}
		}
		_, _ = fmt.Fprintf(out, "native expansion generation=%s mode=%s\n", strings.TrimSpace(string(raw)), os.Getenv("MODE"))
		return 0
	}
	if mode != "migration-success" && mode != "migration-failure" && mode != "migration-interrupted" {
		return 2
	}
	_, _ = fmt.Fprintf(out, "native contract started generation=%s mode=%s\n", strings.TrimSpace(string(raw)), os.Getenv("MODE"))
	if config.Enabled() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if config.Contract(ctx, mode == "migration-interrupted") != nil {
			return 2
		}
	} else if mode == "migration-interrupted" {
		time.Sleep(2 * time.Minute)
	}
	if mode == "migration-failure" {
		return 23
	}
	_, _ = fmt.Fprintln(out, "native contract completed")
	return 0
}

func newReport(w *workload) *observation { report := w.snapshot(); return &report }

func currentSQLGeneration(generation string) bool {
	return generation != "stable" && generation != "baseline"
}
