package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale dev --all` runs one developer loop per deployable workspace member.
//
// Each member runs as a child `gregale dev --path DIR --name PROJECT` process.
// The single-app loop writes through package-level terminal state (osStdout,
// osStderr, jsonOutput) all the way down the deploy path, so N in-process
// loops could neither be told apart nor isolated from each other. A child per
// app reuses the single-app path unchanged: its output is prefixed by this
// supervisor, a failed build or crash in one app never stops the others, and
// Ctrl-C reaches every child through the shared terminal process group (and is
// forwarded explicitly for a SIGTERM or a non-terminal parent).

// devAllPreflightTimeout bounds the read-only budget check: one account read
// plus one existence probe per selected app.
const devAllPreflightTimeout = 30 * time.Second

// devAllApp is one developer loop selected by --all.
type devAllApp struct {
	Project   string // developer-session project name passed as --name
	RootDir   string // slash path relative to the discovery root, "." for the root
	SourceDir string // absolute source directory passed as --path
}

// devAllOptions are the single-app flags forwarded unchanged to every child.
type devAllOptions struct {
	once           bool
	stop           bool
	noLogs         bool
	open           bool
	postgres       bool
	postgresRegion string
	ttl            string
}

func (o devAllOptions) childArgs(app devAllApp, jsonMode bool) []string {
	args := []string{"dev", "--path", app.SourceDir, "--name", app.Project}
	for _, flag := range []struct {
		set  bool
		name string
	}{{o.once, "--once"}, {o.stop, "--stop"}, {o.noLogs, "--no-logs"}, {o.open, "--open"}, {o.postgres, "--postgres"}} {
		if flag.set {
			args = append(args, flag.name)
		}
	}
	if o.postgresRegion != "" {
		args = append(args, "--postgres-region", o.postgresRegion)
	}
	if o.ttl != "" {
		args = append(args, "--ttl", o.ttl)
	}
	// Pass the output mode explicitly: a child would otherwise fall back to a
	// persisted JSON preference that the parent's --json=false overrode.
	if jsonMode {
		return append(args, "--json")
	}
	return append(args, "--json=false")
}

// discoverDevAllApps selects every deployable workspace or convention member
// below root. A deployable root with no members is the single app; when
// members exist the root is treated as the workspace container (for example a
// package.json that only declares workspaces) and is not started.
func discoverDevAllApps(root string, discover func(string) ([]projectSource, error), rootDeployable func(string) bool) ([]devAllApp, error) {
	sources, err := discover(root)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		if !rootDeployable(root) {
			return nil, fmt.Errorf("no deployable apps found below %s; run `gregale scan --path .` to inspect the workspace", root)
		}
		return []devAllApp{{Project: devAllProjectName(filepath.Base(root), "."), RootDir: ".", SourceDir: root}}, nil
	}
	apps := make([]devAllApp, 0, len(sources))
	owners := make(map[string]string, len(sources))
	for _, source := range sources {
		project := devAllProjectName(source.Name, source.RootDir)
		if previous, ok := owners[project]; ok {
			return nil, fmt.Errorf("apps %s and %s both map to developer project %q; rename one workspace or run them separately with --path and --name", previous, source.RootDir, project)
		}
		owners[project] = source.RootDir
		apps = append(apps, devAllApp{Project: project, RootDir: source.RootDir, SourceDir: source.Path})
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].RootDir < apps[j].RootDir })
	return apps, nil
}

// devAllProjectName prefers the scanner's workload name and falls back to the
// member directory name when the workload name is empty.
func devAllProjectName(name, rootDir string) string {
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(filepath.FromSlash(rootDir))
	}
	return sanitizeSlug(name)
}

// devAllQuotaClient is the API surface the quota preflight needs.
type devAllQuotaClient interface {
	Whoami(context.Context) (api.AccountResponse, error)
	GetDevSyncHistory(context.Context, string, string, int) (api.DevSyncHistoryResponse, error)
}

// preflightDevAllQuota fails before any remote mutation when starting the
// selected apps would exceed the plan's developer-environment budget. An app
// whose environment already exists (a resumed workspace) reuses its slot, so
// only new environments count against the available budget.
func preflightDevAllQuota(ctx context.Context, client devAllQuotaClient, apps []devAllApp, workspaceIDs []string) error {
	account, err := client.Whoami(ctx)
	if err != nil {
		return fmt.Errorf("load developer environment budget: %w", err)
	}
	var missing []string
	for i, app := range apps {
		_, histErr := client.GetDevSyncHistory(ctx, app.Project, workspaceIDs[i], 1)
		switch {
		case histErr == nil:
		case isNotFound(histErr):
			missing = append(missing, app.Project)
		default:
			return fmt.Errorf("check developer environment %s: %w", app.Project, histErr)
		}
	}
	available := account.Limits.DeveloperApps - account.DeveloperAppCount
	if available < 0 {
		available = 0
	}
	if len(missing) > available {
		return fmt.Errorf("%d new developer environments are needed (%s) but only %d of %d are available on the %s plan; stop unused ones with `gregale dev --stop` or start fewer apps with --path",
			len(missing), strings.Join(missing, ", "), available, account.Limits.DeveloperApps, account.Plan)
	}
	return nil
}

// devAllProcess is a running child developer loop.
type devAllProcess interface {
	Wait() (int, error)
	Signal(os.Signal) error
}

// devAllLauncher starts one child loop writing to the supplied streams.
type devAllLauncher func(args []string, stdout, stderr io.Writer) (devAllProcess, error)

type execDevAllProcess struct{ cmd *exec.Cmd }

func (p execDevAllProcess) Wait() (int, error) {
	err := p.cmd.Wait()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

func (p execDevAllProcess) Signal(sig os.Signal) error { return p.cmd.Process.Signal(sig) }

func launchDevAllChild(args []string, stdout, stderr io.Writer) (devAllProcess, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate gregale executable: %w", err)
	}
	cmd := exec.Command(executable, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, stdout, stderr
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return execDevAllProcess{cmd: cmd}, nil
}

// devPrefixWriter writes whole lines to out, each prefixed with the app label.
// A shared mutex keeps lines from concurrent apps from interleaving mid-line.
type devPrefixWriter struct {
	mu     *sync.Mutex
	out    io.Writer
	prefix string
	buf    []byte
}

func (w *devPrefixWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		if err := w.emit(w.buf[:i]); err != nil {
			return len(p), err
		}
		w.buf = w.buf[i+1:]
	}
}

// Close flushes a trailing partial line.
func (w *devPrefixWriter) Close() error {
	if len(w.buf) == 0 {
		return nil
	}
	err := w.emit(w.buf)
	w.buf = nil
	return err
}

func (w *devPrefixWriter) emit(line []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := fmt.Fprintf(w.out, "%s%s\n", w.prefix, line)
	return err
}

// newDevJSONTagger returns a writer that decodes the child's JSON value
// stream (NDJSON receipts, diagnostics, or an indented object) and re-emits
// each value as one compact NDJSON line tagged with the app it came from.
// Undecodable output is preserved on errOut with the app prefix.
func newDevJSONTagger(app devAllApp, mu *sync.Mutex, out, errOut io.Writer) io.WriteCloser {
	reader, writer := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		decoder := json.NewDecoder(reader)
		for {
			var value json.RawMessage
			if err := decoder.Decode(&value); err != nil {
				if !errors.Is(err, io.EOF) {
					buffered, _ := io.ReadAll(decoder.Buffered())
					rest := io.MultiReader(bytes.NewReader(bytes.TrimLeft(buffered, " \t\r\n")), reader)
					raw := &devPrefixWriter{mu: mu, out: errOut, prefix: "[" + app.Project + "] "}
					_, _ = io.Copy(raw, rest)
					_ = raw.Close()
				}
				_, _ = io.Copy(io.Discard, reader)
				return
			}
			line := tagDevJSONValue(app, value)
			mu.Lock()
			_, _ = fmt.Fprintf(out, "%s\n", line)
			mu.Unlock()
		}
	}()
	return &devJSONTagger{writer: writer, done: done}
}

type devJSONTagger struct {
	writer *io.PipeWriter
	done   chan struct{}
}

func (t *devJSONTagger) Write(p []byte) (int, error) { return t.writer.Write(p) }

func (t *devJSONTagger) Close() error {
	err := t.writer.Close()
	<-t.done
	return err
}

// tagDevJSONValue adds dev_project and dev_path to an object, or wraps any
// other JSON value under "value".
func tagDevJSONValue(app devAllApp, value json.RawMessage) []byte {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(value, &fields); err != nil || fields == nil {
		fields = map[string]json.RawMessage{"value": value}
	}
	fields["dev_project"], _ = json.Marshal(app.Project)
	fields["dev_path"], _ = json.Marshal(app.RootDir)
	line, err := json.Marshal(fields)
	if err != nil {
		return value
	}
	return line
}

// runDevAll supervises one child loop per app until every child exits. A child
// that exits early is reported and the remaining apps keep running. The exit
// code is 0 only when every child exits 0.
func runDevAll(ctx context.Context, apps []devAllApp, opts devAllOptions, launch devAllLauncher, stdout, stderr io.Writer) int {
	var mu sync.Mutex
	type result struct {
		app  devAllApp
		code int
	}
	results := make(chan result, len(apps))
	var (
		procMu    sync.Mutex
		processes []devAllProcess
	)
	for _, app := range apps {
		prefix := "[" + app.Project + "] "
		errOut := &devPrefixWriter{mu: &mu, out: stderr, prefix: prefix}
		var out io.WriteCloser = &devPrefixWriter{mu: &mu, out: stdout, prefix: prefix}
		if jsonOutput {
			out = newDevJSONTagger(app, &mu, stdout, stderr)
		}
		process, err := launch(opts.childArgs(app, jsonOutput), out, errOut)
		if err != nil {
			_ = out.Close()
			mu.Lock()
			_, _ = fmt.Fprintf(stderr, "%scould not start developer loop: %v\n", prefix, err)
			mu.Unlock()
			results <- result{app: app, code: 1}
			continue
		}
		procMu.Lock()
		processes = append(processes, process)
		procMu.Unlock()
		go func(app devAllApp, process devAllProcess, out, errOut io.Closer) {
			code, waitErr := process.Wait()
			_ = out.Close()
			_ = errOut.Close()
			if waitErr != nil {
				mu.Lock()
				_, _ = fmt.Fprintf(stderr, "[%s] developer loop failed: %v\n", app.Project, waitErr)
				mu.Unlock()
			}
			results <- result{app: app, code: code}
		}(app, process, out, errOut)
	}
	forwardDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			procMu.Lock()
			for _, process := range processes {
				_ = process.Signal(os.Interrupt)
			}
			procMu.Unlock()
		case <-forwardDone:
		}
	}()
	defer close(forwardDone)
	exit := 0
	for range apps {
		r := <-results
		if r.code != 0 {
			exit = 1
		}
		if !jsonOutput && (r.code != 0 || !opts.once && !opts.stop && ctx.Err() == nil) {
			mu.Lock()
			if r.code != 0 {
				PrintWarn(stderr, "[%s] developer loop exited (code %d); other apps keep running", r.app.Project, r.code)
			} else {
				PrintWarn(stderr, "[%s] developer loop stopped; other apps keep running", r.app.Project)
			}
			mu.Unlock()
		}
	}
	return exit
}

// cmdDevAll resolves the workspace members, checks the developer-environment
// budget, and supervises one developer loop per app.
func cmdDevAll(sourceDir string, opts devAllOptions) int {
	apps, err := discoverDevAllApps(sourceDir, discoverProjectSources, func(dir string) bool { return detectShape(dir) != shapeUnknown })
	if err != nil {
		return printErr("No developer apps selected", err)
	}
	if !opts.stop {
		developerID, err := loadOrCreateDeveloperID()
		if err != nil {
			return printErr("Could not load local developer identity", err)
		}
		workspaceIDs := make([]string, len(apps))
		for i, app := range apps {
			if workspaceIDs[i], err = deriveDevWorkspaceID(developerID, app.SourceDir); err != nil {
				return printErr("Could not identify developer workspace", err)
			}
		}
		client, err := authedClient()
		if err != nil {
			return printErr("Not logged in", err)
		}
		preflightCtx, cancel := context.WithTimeout(context.Background(), devAllPreflightTimeout)
		err = preflightDevAllQuota(preflightCtx, client, apps, workspaceIDs)
		cancel()
		if err != nil {
			return printErr("Developer environment quota exceeded", err)
		}
	}
	if !jsonOutput {
		labels := make([]string, len(apps))
		for i, app := range apps {
			labels[i] = app.Project + " (" + app.RootDir + ")"
		}
		PrintProgress(osStdout, "Starting %d developer loops: %s", len(apps), strings.Join(labels, ", "))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runDevAll(ctx, apps, opts, launchDevAllChild, osStdout, osStderr)
}

// cmdDevAllFromFlags validates the flags that cannot apply to several apps at
// once, then starts the supervisor rooted at --path (default: current
// directory). Per-app config belongs in each member's gregale.yaml dev block,
// which every child loop reads for its own source root.
func cmdDevAllFromFlags(cwd, sourcePath string, explicit map[string]bool, opts devAllOptions) int {
	for _, flag := range []string{"name", "env-file", "service-override-file", "postgres-seed", "reseed", "debug", "debug-port"} {
		if explicit[flag] {
			return printErr("Invalid flags", fmt.Errorf("--%s cannot be combined with --all; set per-app values in each app's gregale.yaml dev block", flag))
		}
	}
	// Validate a shared --ttl once here rather than failing in every child.
	if opts.ttl != "" {
		if _, err := resolveDevTTL(opts.ttl); err != nil {
			return printErr("Invalid --ttl", err)
		}
	}
	if !opts.postgres && opts.postgresRegion != "" {
		return printErr("Invalid flags", errors.New("--postgres-region requires --postgres"))
	}
	root, err := resolveDeploySourceDir(cwd, sourcePath)
	if err != nil {
		return printErr("Invalid developer source", err)
	}
	return cmdDevAll(root, opts)
}
