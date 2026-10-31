package wire

// Root-only Go profiling for the platform daemons (ADR-20261023200002).
//
// BootStamps starts an HTTP server on the Linux abstract unix socket
// @faas-debug/<daemon>. The listener (debugprofile_linux.go) accepts only
// peers whose SO_PEERCRED uid is root or the daemon's own, so nothing is
// reachable over the network and no unit or sandbox path changes. The
// handlers below use runtime/pprof directly: importing net/http/pprof would
// register the same handlers on http.DefaultServeMux as a side effect.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"strconv"
	"strings"
	"time"
)

const (
	// DebugProfileSocketPrefix is the abstract-socket name prefix; the full
	// name is DebugProfileSocketPrefix + daemon (or + daemon + "." + pid).
	DebugProfileSocketPrefix = "faas-debug/"
	// DebugProfileMaxSeconds caps CPU profiles and execution traces.
	DebugProfileMaxSeconds = 60
	// EnvDebugProfiling turns the endpoint off when set to off, false or 0.
	EnvDebugProfiling = "FAAS_DEBUG_PROFILING"

	debugProfileDefaultSeconds = 30
)

// debugProfileNames are the runtime profiles served by name. cmdline is
// deliberately absent (ADR-20261023200002 §3).
var debugProfileNames = []string{"heap", "allocs", "goroutine", "threadcreate", "block", "mutex"}

// debugProfilingEnabled reports whether the endpoint should start.
func debugProfilingEnabled(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(EnvDebugProfiling))) {
	case "off", "false", "0", "no":
		return false
	}
	return true
}

// DebugProfileHandler serves the profiles under /debug/pprof/.
func DebugProfileHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", serveDebugProfileIndex)
	for _, name := range debugProfileNames {
		mux.HandleFunc("/debug/pprof/"+name, serveNamedProfile(name))
	}
	mux.HandleFunc("/debug/pprof/profile", serveCPUProfile)
	mux.HandleFunc("/debug/pprof/trace", serveExecutionTrace)
	return mux
}

func serveDebugProfileIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/debug/pprof/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprintln(w, "profiles: profile (CPU, ?seconds=N), trace (?seconds=N), "+strings.Join(debugProfileNames, ", "))
	_, _ = fmt.Fprintf(w, "seconds is capped at %d; named profiles take ?debug=0|1|2\n", DebugProfileMaxSeconds)
}

func serveNamedProfile(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := pprof.Lookup(name)
		if p == nil {
			http.Error(w, "unknown profile", http.StatusNotFound)
			return
		}
		debug, err := strconv.Atoi(r.URL.Query().Get("debug"))
		if r.URL.Query().Get("debug") == "" {
			debug, err = 0, nil
		}
		if err != nil || debug < 0 || debug > 2 {
			http.Error(w, "debug must be 0, 1 or 2", http.StatusBadRequest)
			return
		}
		if name == "heap" && r.URL.Query().Get("gc") == "1" {
			runtime.GC()
		}
		if debug == 0 {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		} else {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
		_ = p.WriteTo(w, debug)
	}
}

// debugProfileSeconds parses ?seconds=N within (0, DebugProfileMaxSeconds].
func debugProfileSeconds(r *http.Request) (time.Duration, error) {
	raw := r.URL.Query().Get("seconds")
	if raw == "" {
		return debugProfileDefaultSeconds * time.Second, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 || n > DebugProfileMaxSeconds {
		return 0, fmt.Errorf("seconds must be 1..%d", DebugProfileMaxSeconds)
	}
	return time.Duration(n) * time.Second, nil
}

func serveCPUProfile(w http.ResponseWriter, r *http.Request) {
	d, err := debugProfileSeconds(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="profile"`)
	if err := pprof.StartCPUProfile(w); err != nil {
		// The runtime allows one CPU profile at a time.
		w.Header().Del("Content-Disposition")
		http.Error(w, "could not start CPU profile: "+err.Error(), http.StatusConflict)
		return
	}
	sleepOrDone(r.Context(), d)
	pprof.StopCPUProfile()
}

func serveExecutionTrace(w http.ResponseWriter, r *http.Request) {
	d, err := debugProfileSeconds(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="trace"`)
	if err := trace.Start(w); err != nil {
		w.Header().Del("Content-Disposition")
		http.Error(w, "could not start trace: "+err.Error(), http.StatusConflict)
		return
	}
	sleepOrDone(r.Context(), d)
	trace.Stop()
}

func sleepOrDone(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// startDebugProfiling serves DebugProfileHandler on the daemon's abstract
// socket until ctx ends. It never fails the daemon: a listener error is
// logged and the daemon runs without the endpoint.
func startDebugProfiling(ctx context.Context, name string, log *slog.Logger) {
	if !debugProfilingEnabled(os.Getenv) {
		return
	}
	ln, addr, err := listenDebugProfile(name, log)
	if errors.Is(err, errDebugProfileUnsupported) {
		return
	}
	if err != nil {
		log.Warn("debug profiling endpoint not started", "daemon", name, "err", err)
		return
	}
	srv := &http.Server{
		Handler:           DebugProfileHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      (DebugProfileMaxSeconds + 15) * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	go func() {
		<-ctx.Done()
		// ctx is already done here; keep its values but not its cancellation.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			log.Warn("debug profiling endpoint stopped", "daemon", name, "err", err)
		}
	}()
	log.Info("debug profiling endpoint listening", "daemon", name, "socket", "@"+addr)
}

var errDebugProfileUnsupported = errors.New("debug profiling needs Linux abstract sockets")

var debugProfileNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// validDebugProfileName keeps the socket name to a daemon-like token.
func validDebugProfileName(name string) bool { return debugProfileNameRE.MatchString(name) }
