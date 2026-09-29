package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type testLocalAppSpec struct {
	Command         []string               `yaml:"command"`
	Readiness       testLocalReadinessSpec `yaml:"readiness"`
	ShutdownTimeout string                 `yaml:"shutdown_timeout"`
}

type testLocalReadinessSpec struct {
	Path    string `yaml:"path"`
	Status  int    `yaml:"status"`
	Timeout string `yaml:"timeout"`
}

type testLocalAppEvidence struct {
	Ready               bool   `json:"ready"`
	ReadinessAttempts   int    `json:"readiness_attempts"`
	ReadinessStatus     int    `json:"readiness_status,omitempty"`
	ReadinessDurationMS int64  `json:"readiness_duration_ms"`
	Shutdown            string `json:"shutdown"`
	ExitCode            *int   `json:"exit_code,omitempty"`
}

func validateTestLocalAppSpec(spec *testLocalAppSpec) error {
	if spec == nil {
		return nil
	}
	if len(spec.Command) == 0 || len(spec.Command) > 64 || strings.TrimSpace(spec.Command[0]) == "" {
		return errors.New("local.command needs an executable and at most 64 arguments")
	}
	argumentBytes := 0
	for _, argument := range spec.Command {
		argumentBytes += len(argument)
		if strings.ContainsRune(argument, 0) || argumentBytes > 32768 {
			return errors.New("local.command must fit in 32 KiB and contain no NUL bytes")
		}
		remaining := strings.NewReplacer("${local.port}", "", "${local.host}", "", "${local.url}", "").Replace(argument)
		if strings.Contains(remaining, "${local.") {
			return errors.New("local.command supports only ${local.port}, ${local.host}, and ${local.url} placeholders")
		}
	}
	path := spec.readinessPath()
	parsed, err := url.Parse(path)
	if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.ForceQuery || strings.ContainsAny(path, "#\r\n") || strings.Contains(path, "${") || len(path) > 2048 {
		return errors.New("local.readiness.path must be an application-relative path without a query, fragment, or template")
	}
	if spec.readinessStatus() < 200 || spec.readinessStatus() > 599 {
		return errors.New("local.readiness.status must be between 200 and 599")
	}
	for _, setting := range []struct {
		name, value string
		max         time.Duration
	}{
		{"local.readiness.timeout", spec.Readiness.Timeout, 5 * time.Minute},
		{"local.shutdown_timeout", spec.ShutdownTimeout, 30 * time.Second},
	} {
		if setting.value != "" {
			d, err := time.ParseDuration(setting.value)
			if err != nil || d < time.Second || d > setting.max {
				return fmt.Errorf("%s must be a duration between 1s and %s", setting.name, setting.max)
			}
		}
	}
	return nil
}

func (spec *testLocalAppSpec) readinessPath() string {
	if spec.Readiness.Path == "" {
		return "/"
	}
	return spec.Readiness.Path
}

func (spec *testLocalAppSpec) readinessStatus() int {
	if spec.Readiness.Status == 0 {
		return http.StatusOK
	}
	return spec.Readiness.Status
}

func (spec *testLocalAppSpec) readinessTimeout() time.Duration {
	if spec.Readiness.Timeout == "" {
		return 30 * time.Second
	}
	d, _ := time.ParseDuration(spec.Readiness.Timeout)
	return d
}

func (spec *testLocalAppSpec) shutdownTimeout() time.Duration {
	if spec.ShutdownTimeout == "" {
		return 5 * time.Second
	}
	d, _ := time.ParseDuration(spec.ShutdownTimeout)
	return d
}

// Hold the selected port until just before Start. Arbitrary app commands cannot
// inherit a bound listener, so they must bind PORT themselves after it is closed.
func reserveLocalAppEndpoint(baseURL string) (net.Listener, string, string, string, error) {
	host, port := "127.0.0.1", "0"
	if baseURL != "" {
		validated, err := validateLocalTestURL(baseURL)
		if err != nil {
			return nil, "", "", "", err
		}
		parsed, _ := url.Parse(validated)
		host, port = parsed.Hostname(), parsed.Port()
		if strings.EqualFold(host, "localhost") {
			host = "127.0.0.1"
		}
		if port == "" {
			port = "80"
		}
	}
	network := "tcp4"
	if strings.Contains(host, ":") {
		network = "tcp6"
	}
	listener, err := net.Listen(network, net.JoinHostPort(host, port))
	if err != nil {
		return nil, "", "", "", fmt.Errorf("reserve local app port (choose an unused --base-url or omit it): %w", err)
	}
	_, port, err = net.SplitHostPort(listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		return nil, "", "", "", err
	}
	return listener, "http://" + net.JoinHostPort(host, port), host, port, nil
}

func localAppCommandEnv(env []string, host, port string) []string {
	result := make([]string, 0, len(env)+4)
	for _, variable := range env {
		if !strings.HasPrefix(variable, "PORT=") && !strings.HasPrefix(variable, "HOST=") {
			result = append(result, variable)
		}
	}
	return append(result, "PORT="+port, "HOST="+host, "GREGALE_TEST_PORT="+port, "GREGALE_TEST_HOST="+host)
}

const localAppLogLimit = 64 << 10

// App output is collected privately and emitted only after all commands and load
// workers have stopped. This keeps --json stdout clean and bounds memory use.
type localAppLog struct {
	mu   sync.Mutex
	tail []byte
}

func (log *localAppLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	n := len(data)
	if len(data) >= localAppLogLimit {
		log.tail = append(log.tail[:0], data[len(data)-localAppLogLimit:]...)
	} else {
		if excess := len(log.tail) + len(data) - localAppLogLimit; excess > 0 {
			copy(log.tail, log.tail[excess:])
			log.tail = log.tail[:len(log.tail)-excess]
		}
		log.tail = append(log.tail, data...)
	}
	return n, nil
}

func (log *localAppLog) String() string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return string(log.tail)
}

type managedLocalApp struct {
	command           *exec.Cmd
	done              chan struct{}
	logDone           chan struct{}
	logRead           *os.File
	log               localAppLog
	mu                sync.Mutex
	stopping          bool
	unexpected        error
	unexpectedKillErr error
	unexpectedForced  bool
}

func startManagedLocalApp(ctx context.Context, cancel context.CancelCauseFunc, spec *testLocalAppSpec, dir string, env []string, baseURL, host, port string, reservation net.Listener) (*managedLocalApp, error) {
	defer func() { _ = reservation.Close() }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	replacer := strings.NewReplacer("${local.port}", port, "${local.host}", host, "${local.url}", baseURL)
	argv := make([]string, len(spec.Command))
	for i, argument := range spec.Command {
		argv[i] = replacer.Replace(argument)
	}
	// The supervisor owns termination so fixture cleanup can run after ctx is
	// canceled, while the app remains alive until cleanup finishes.
	command := exec.Command(argv[0], argv[1:]...)
	command.Dir, command.Env = dir, env
	if err := configureLocalAppProcess(command); err != nil {
		return nil, err
	}
	read, write, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("collect local app output: %w", err)
	}
	defer func() { _ = write.Close() }()
	started := false
	defer func() {
		if !started {
			_ = read.Close()
		}
	}()
	app := &managedLocalApp{command: command, done: make(chan struct{}), logDone: make(chan struct{}), logRead: read}
	// Own the pipe reader separately: Cmd.Wait must observe the leader exiting
	// immediately, even if descendants keep stdout/stderr open.
	command.Stdout, command.Stderr = write, write
	if err := reservation.Close(); err != nil {
		return nil, fmt.Errorf("release local app port reservation: %w", err)
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start local app: %w", err)
	}
	started = true
	go func() {
		defer close(app.logDone)
		defer func() { _ = read.Close() }()
		_, _ = io.Copy(&app.log, read)
	}()
	go func() {
		_ = command.Wait()
		app.mu.Lock()
		defer app.mu.Unlock()
		if !app.stopping {
			code := -1
			if command.ProcessState != nil {
				code = command.ProcessState.ExitCode()
			}
			app.unexpected = fmt.Errorf("local app exited before shutdown (exit code %d)", code)
			// Clean descendants immediately after an unexpected exit. Do not
			// signal this reaped PID again after a potentially long fixture cleanup.
			if localAppProcessGroupAlive(command) {
				app.unexpectedForced = true
				app.unexpectedKillErr = signalLocalAppProcess(command, true)
			}
			cancel(app.unexpected)
		}
		close(app.done)
	}()
	return app, nil
}

func (app *managedLocalApp) failure() error {
	app.mu.Lock()
	defer app.mu.Unlock()
	return app.unexpected
}

func (app *managedLocalApp) waitReady(ctx context.Context, baseURL string, spec *testLocalAppSpec, evidence *testLocalAppEvidence) error {
	started := time.Now()
	defer func() { evidence.ReadinessDurationMS = time.Since(started).Milliseconds() }()
	readyCtx, cancel := context.WithTimeout(ctx, spec.readinessTimeout())
	defer cancel()
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if readyCtx.Err() != nil {
			if err := app.failure(); err != nil {
				return err
			}
			return fmt.Errorf("local app readiness did not reach HTTP %d (last status %d): %w", spec.readinessStatus(), evidence.ReadinessStatus, readyCtx.Err())
		}
		request, err := http.NewRequestWithContext(readyCtx, http.MethodGet, baseURL+spec.readinessPath(), nil)
		if err != nil {
			return fmt.Errorf("local app readiness request: %w", err)
		}
		evidence.ReadinessAttempts++
		response, err := client.Do(request)
		if err == nil {
			evidence.ReadinessStatus = response.StatusCode
			_ = response.Body.Close()
			if response.StatusCode == spec.readinessStatus() {
				if readyCtx.Err() != nil {
					continue
				}
				evidence.Ready = true
				return nil
			}
		}
		select {
		case <-readyCtx.Done():
		case <-ticker.C:
		}
	}
}

func (app *managedLocalApp) stop(timeout time.Duration, evidence *testLocalAppEvidence) error {
	defer app.finishLogs()
	app.mu.Lock()
	app.stopping = true
	if app.unexpected != nil {
		evidence.Shutdown = "exited"
		if app.unexpectedForced {
			evidence.Shutdown = "forced"
		}
		err := app.unexpectedKillErr
		app.mu.Unlock()
		app.recordExitCode(evidence)
		if err != nil {
			evidence.Shutdown = "failed"
			return fmt.Errorf("kill local app descendants after unexpected exit: %w", err)
		}
		return nil
	}
	app.mu.Unlock()
	evidence.Shutdown = "graceful"
	signalErr := signalLocalAppProcess(app.command, false)
	if signalErr != nil {
		evidence.Shutdown = "failed"
		// Still attempt SIGKILL and reap the leader below.
		defer func() { evidence.Shutdown = "failed" }()
		if killErr := signalLocalAppProcess(app.command, true); killErr != nil {
			return fmt.Errorf("terminate local app: %w", errors.Join(signalErr, killErr))
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-app.done:
		// A launcher can exit before its children. Terminate any remaining
		// members of our process group, including children that ignored TERM.
		if localAppProcessGroupAlive(app.command) {
			evidence.Shutdown = "forced"
			if err := signalLocalAppProcess(app.command, true); err != nil {
				evidence.Shutdown = "failed"
				return fmt.Errorf("kill local app descendants: %w", err)
			}
		}
	case <-timer.C:
		evidence.Shutdown = "forced"
		if err := signalLocalAppProcess(app.command, true); err != nil {
			evidence.Shutdown = "failed"
			return fmt.Errorf("kill local app: %w", err)
		}
		select {
		case <-app.done:
		case <-time.After(3 * time.Second):
			evidence.Shutdown = "failed"
			return errors.New("local app did not exit after forced shutdown")
		}
	}
	app.recordExitCode(evidence)
	if signalErr != nil {
		return fmt.Errorf("terminate local app gracefully: %w", signalErr)
	}
	return nil
}

func (app *managedLocalApp) recordExitCode(evidence *testLocalAppEvidence) {
	if app.command.ProcessState != nil {
		code := app.command.ProcessState.ExitCode()
		evidence.ExitCode = &code
	}
}

func (app *managedLocalApp) finishLogs() {
	select {
	case <-app.logDone:
	case <-time.After(time.Second):
		// A detached process outside the owned group must not hold the reader
		// open indefinitely. Such launchers are not supported managed commands.
		_ = app.logRead.Close()
		<-app.logDone
	}
}

func printLocalAppFailureLog(app *managedLocalApp) {
	if output := app.log.String(); output != "" {
		_, _ = fmt.Fprintf(osStderr, "Local app output (last %s bytes):\n%s\n", strconv.Itoa(localAppLogLimit), output)
	}
}
