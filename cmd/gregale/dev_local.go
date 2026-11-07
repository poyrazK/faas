package main

// `gregale dev --local` (ADR-20261107210000): serve the project's functions
// on this machine through the production adapters and runner core, and
// reload a function when its source changes.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/guest/runners/local"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	devLocalUsage        = "usage: gregale dev --local [--path DIR] [--port N] [--env-file PATH] [--no-reload]"
	devLocalDefaultPort  = 8787
	devLocalTokenHeader  = "X-Gregale-Local-Token"
	devLocalTriggerPath  = "/__gregale/trigger"
	devLocalPollInterval = 400 * time.Millisecond
	devLocalMaxScanFiles = 20000
)

// devLocalState lets `gregale dev trigger --local` find the running server.
type devLocalState struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

// devLocalTrigger is one platform-style call the trigger command asks for.
type devLocalTrigger struct {
	Function string `json:"function"`
	Source   string `json:"source"` // cron or async_invoke
	Method   string `json:"method"`
	Path     string `json:"path"`
	Body     string `json:"body"`
}

type devLocalTriggerResult struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// hasLocalFlag reports whether args ask for --local (and removes it).
func hasLocalFlag(args []string) ([]string, bool) {
	out := make([]string, 0, len(args))
	found := false
	for _, a := range args {
		if a == "--local" || a == "-local" {
			found = true
			continue
		}
		out = append(out, a)
	}
	return out, found
}

func cmdDevLocal(args []string) int {
	fs := newFlagSet("dev --local", flag.ContinueOnError)
	sourcePath := fs.String("path", "", "source directory (relative to the current directory)")
	port := fs.Int("port", devLocalDefaultPort, "local port")
	envFile := fs.String("env-file", "", "KEY=VALUE file whose entries every function gets")
	noReload := fs.Bool("no-reload", false, "do not reload functions when their source changes")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 0 {
		PrintUsage(osStderr, devLocalUsage, "dev")
		return 1
	}
	projectDir, baseEnv, err := devLocalInputs(*sourcePath, *envFile)
	if err != nil {
		return printErr("Could not start local functions", err)
	}
	fns, err := planLocalFunctions(projectDir)
	if err != nil {
		return printErr("Could not start local functions", err)
	}
	cacheDir, err := localCacheDir(projectDir)
	if err != nil {
		return printErr("Could not start local functions", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for _, fn := range fns {
		if err := fn.prepare(ctx, cacheDir, baseEnv); err != nil {
			return printErr("Could not start function "+fn.Name, err)
		}
	}
	srv, err := newDevLocalServer(fns, projectDir)
	if err != nil {
		return printErr("Could not start local functions", err)
	}
	srv.baseEnv = baseEnv
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(*port))
	if err != nil {
		return printErr("Could not listen", fmt.Errorf("port %d: %w; pass --port", *port, err))
	}
	srv.port = ln.Addr().(*net.TCPAddr).Port
	statePath := filepath.Join(cacheDir, "state.json")
	if err := writeDevLocalState(statePath, devLocalState{Port: srv.port, Token: srv.token, PID: os.Getpid()}); err != nil {
		return printErr("Could not start local functions", err)
	}
	defer func() { _ = os.Remove(statePath) }()
	srv.printBanner(osStdout)
	for _, fn := range fns {
		srv.prewarm(ctx, fn)
	}
	if !*noReload {
		go srv.watch(ctx, cacheDir)
	}
	httpSrv := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()
	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return printErr("Local server stopped", err)
	}
	for _, fn := range fns {
		local.Reload(fn.proc)
	}
	_, _ = fmt.Fprintln(osStdout, "\nStopped.")
	return 0
}

// devLocalInputs resolves the project directory and the base environment:
// this shell's environment plus the --env-file entries.
func devLocalInputs(sourcePath, envFile string) (string, []string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", nil, err
	}
	projectDir := cwd
	if sourcePath != "" {
		projectDir = filepath.Join(cwd, sourcePath)
		if filepath.IsAbs(sourcePath) {
			projectDir = sourcePath
		}
	}
	if projectDir, err = filepath.Abs(projectDir); err != nil {
		return "", nil, err
	}
	env := os.Environ()
	if envFile != "" {
		path, err := resolveDevEnvFilePath(cwd, envFile)
		if err != nil {
			return "", nil, err
		}
		pairs, _, err := readDevEnvFile(path)
		if err != nil {
			return "", nil, err
		}
		for _, p := range pairs {
			env = append(env, p.Key+"="+p.Value)
		}
	}
	return projectDir, env, nil
}

func writeDevLocalState(path string, st devLocalState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func readDevLocalState(projectDir string) (devLocalState, error) {
	var st devLocalState
	cacheDir, err := localCacheDir(projectDir)
	if err != nil {
		return st, err
	}
	b, err := os.ReadFile(filepath.Join(cacheDir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return st, errors.New("no local functions are running for this directory; start them with gregale dev --local")
	}
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

type devLocalServer struct {
	fns        []*localFunction
	byName     map[string]*localFunction
	entry      *localFunction
	projectDir string
	baseEnv    []string
	token      string
	port       int
	out        io.Writer
	mu         sync.Mutex // serializes reload output and Go rebuilds
}

func newDevLocalServer(fns []*localFunction, projectDir string) (*devLocalServer, error) {
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	s := &devLocalServer{fns: fns, byName: map[string]*localFunction{}, projectDir: projectDir, token: hex.EncodeToString(token), out: osStdout}
	for _, fn := range fns {
		s.byName[fn.Name] = fn
		if fn.Entry {
			s.entry = fn
		}
	}
	return s, nil
}

func (s *devLocalServer) printBanner(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Serving %d function(s) on this machine (ctrl-c to stop):\n", len(s.fns))
	for _, fn := range s.fns {
		rel, _ := filepath.Rel(s.projectDir, fn.Root)
		extra := ""
		if len(fn.Crons) > 0 {
			extra = "  cron " + strings.Join(fn.Crons, ", ")
		}
		_, _ = fmt.Fprintf(w, "  %-16s http://%s.localhost:%d  %-6s %s%s\n", fn.Name, fn.Name, s.port, fn.Family, filepath.ToSlash(rel), extra)
	}
	switch {
	case len(s.fns) == 1:
		_, _ = fmt.Fprintf(w, "  also at http://localhost:%d\n", s.port)
	case s.entry != nil:
		_, _ = fmt.Fprintf(w, "  http://localhost:%d routes like the entry address (entry: %s)\n", s.port, s.entry.Name)
	}
	_, _ = fmt.Fprintln(w, "\nSame adapters and runner as production, but no memory cap, pause or snapshot; check those with gregale dev.")
	for _, fn := range s.fns {
		if len(fn.Crons) > 0 {
			_, _ = fmt.Fprintf(w, "Run a cron now: gregale dev trigger --local cron %s\n", fn.Name)
			break
		}
	}
	_, _ = fmt.Fprintln(w)
}

func (s *devLocalServer) prewarm(ctx context.Context, fn *localFunction) {
	warm, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := local.Prewarm(warm, fn.proc); err != nil {
		_, _ = fmt.Fprintf(s.out, "! %s did not start: %v (fix it and save; it reloads)\n", fn.Name, err)
	}
}

func (s *devLocalServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == devLocalTriggerPath {
		s.serveTrigger(w, r)
		return
	}
	fn := s.route(r.Host, r.URL.Path)
	if fn == nil {
		s.serveIndex(w)
		return
	}
	s.invoke(w, r, fn, "")
}

// invoke runs one request with the platform headers a gateway would set.
func (s *devLocalServer) invoke(w http.ResponseWriter, r *http.Request, fn *localFunction, source string) {
	for name := range r.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-faas-") || strings.EqualFold(name, devLocalTokenHeader) {
			r.Header.Del(name)
		}
	}
	timeout := time.Duration(fn.TimeoutS) * time.Second
	if timeout <= 0 {
		timeout = api.RequestBudgetDefault
	}
	r.Header.Set(api.InvocationIDHeader, uuid.NewString())
	r.Header.Set(api.DeadlineHeader, strconv.FormatInt(timeout.Milliseconds(), 10))
	if source != "" {
		r.Header.Set(api.InvocationSourceHeader, source)
		r.Header.Set(api.InvocationAttemptHeader, "1")
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	started := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	local.Handler(fn.proc).ServeHTTP(rec, r.WithContext(ctx))
	_, _ = fmt.Fprintf(s.out, "%s %-6s %s → %d (%s)\n", fn.Name, r.Method, r.URL.RequestURI(), rec.status, time.Since(started).Round(time.Millisecond))
}

func (s *devLocalServer) serveIndex(w http.ResponseWriter) {
	names := make([]string, 0, len(s.fns))
	for _, fn := range s.fns {
		names = append(names, fmt.Sprintf("http://%s.localhost:%d", fn.Name, s.port))
	}
	sort.Strings(names)
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": "no function at this address", "functions": names})
}

func (s *devLocalServer) serveTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get(devLocalTokenHeader) != s.token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var t devLocalTrigger
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&t); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fn := s.byName[t.Function]
	if fn == nil {
		http.Error(w, fmt.Sprintf("no function %q", t.Function), http.StatusNotFound)
		return
	}
	if t.Method == "" {
		t.Method = http.MethodPost
	}
	if t.Path == "" {
		t.Path = "/"
	}
	req := httptest.NewRequest(t.Method, t.Path, strings.NewReader(t.Body))
	req = req.WithContext(r.Context())
	if t.Body != "" && json.Valid([]byte(t.Body)) {
		req.Header.Set("content-type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.invoke(rec, req, fn, t.Source)
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(devLocalTriggerResult{Status: rec.Code, Body: rec.Body.String()})
}

// route picks the function for a request, as the platform's hosts would.
func (s *devLocalServer) route(host, path string) *localFunction {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if name, ok := strings.CutSuffix(strings.ToLower(host), ".localhost"); ok {
		return s.byName[name]
	}
	if len(s.fns) == 1 {
		return s.fns[0]
	}
	if s.entry == nil {
		return nil
	}
	best, bestLen := s.entry, -1
	for _, fn := range s.fns {
		for _, pattern := range fn.Routes {
			if n, ok := matchFunctionRoute(pattern, path); ok && n > bestLen {
				best, bestLen = fn, n
			}
		}
	}
	return best
}

// matchFunctionRoute matches a functions: route glob and returns its
// specificity (the literal prefix length). "/users/*" matches everything
// below /users/; "/users/**" also matches /users itself.
func matchFunctionRoute(pattern, path string) (int, bool) {
	switch {
	case strings.HasSuffix(pattern, "/**"):
		base := strings.TrimSuffix(pattern, "/**")
		return len(base), path == base || strings.HasPrefix(path, base+"/")
	case strings.HasSuffix(pattern, "/*"):
		base := strings.TrimSuffix(pattern, "*")
		return len(base), strings.HasPrefix(path, base) && len(path) > len(base)
	}
	return len(pattern), path == pattern
}

// watch reloads a function when a file under its root changes.
func (s *devLocalServer) watch(ctx context.Context, cacheDir string) {
	sigs := map[string]string{}
	for _, fn := range s.fns {
		sigs[fn.Name] = treeSignature(fn.Root, cacheDir)
	}
	ticker := time.NewTicker(devLocalPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, fn := range s.fns {
			sig := treeSignature(fn.Root, cacheDir)
			if sig == sigs[fn.Name] {
				continue
			}
			sigs[fn.Name] = sig
			s.reload(ctx, fn, cacheDir)
		}
	}
}

func (s *devLocalServer) reload(ctx context.Context, fn *localFunction, cacheDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	started := time.Now()
	old := fn.proc
	if fn.Family == "go" {
		if err := fn.prepare(ctx, cacheDir, s.baseEnv); err != nil {
			_, _ = fmt.Fprintf(s.out, "! %s: %v\n", fn.Name, err)
			return
		}
	}
	local.Reload(old)
	s.prewarm(ctx, fn)
	_, _ = fmt.Fprintf(s.out, "↻ reloaded %s (%s)\n", fn.Name, time.Since(started).Round(time.Millisecond))
}

// treeSignature summarizes the files under root: names, sizes and mtimes,
// skipping dependency and cache directories.
func treeSignature(root, cacheDir string) string {
	var b strings.Builder
	count := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "__pycache__" || name == "venv" || path == cacheDir) {
				return filepath.SkipDir
			}
			return nil
		}
		count++
		if count > devLocalMaxScanFiles {
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		fmt.Fprintf(&b, "%s:%d:%d;", path, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	return b.String()
}

// statusRecorder notes the status while passing writes and flushes through,
// so streamed responses still stream.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
