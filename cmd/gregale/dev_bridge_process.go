package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type bridgeProcess struct {
	command    *exec.Cmd
	done       chan struct{}
	err        error
	cleanupErr error
	outputDone [2]<-chan struct{}
	outputRead [2]*os.File
}

func bridgeProcessEnvironment(inherited []string, port int, sessionID, sessionURL string, dependencies, bindings map[string]string) ([]string, error) {
	values := make(map[string]string)
	for _, entry := range inherited {
		if key, value, ok := strings.Cut(entry, "="); ok {
			if key == "FAAS_TOKEN" || key == "GREGALE_API_TOKEN" || strings.HasPrefix(key, "GREGALE_DEV_BRIDGE_") || strings.HasPrefix(key, "GREGALE_SERVICE_") {
				continue
			}
			values[key] = value
		}
	}
	values["PORT"], values["HOST"] = strconv.Itoa(port), "127.0.0.1"
	values["GREGALE_DEV_BRIDGE_SESSION_ID"], values["GREGALE_DEV_SESSION_URL"] = sessionID, sessionURL
	used := make(map[string]string)
	for name, endpoint := range dependencies {
		key := "GREGALE_SERVICE_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name)) + "_URL"
		if !bridgeEnvKey(key) {
			return nil, fmt.Errorf("dependency %q needs an explicit environment binding", name)
		}
		if previous, ok := used[key]; ok && previous != name {
			return nil, fmt.Errorf("dependency environment names collide: %s and %s", previous, name)
		}
		used[key], values[key] = name, endpoint
	}
	for key, name := range bindings {
		if !bridgeEnvKey(key) || key == "PORT" || key == "HOST" || key == "FAAS_TOKEN" || strings.HasPrefix(key, "GREGALE_") {
			return nil, fmt.Errorf("invalid or reserved dependency environment key: %s", key)
		}
		endpoint, ok := dependencies[name]
		if !ok {
			return nil, fmt.Errorf("dependency %q is not in this session", name)
		}
		values[key] = endpoint
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result, nil
}

func bridgeEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, ch := range key {
		if ch != '_' && (ch < 'A' || ch > 'Z') && (ch < 'a' || ch > 'z') && (i == 0 || ch < '0' || ch > '9') {
			return false
		}
	}
	return true
}

func startBridgeProcess(args, env []string) (*bridgeProcess, error) {
	if len(args) == 0 {
		return nil, errors.New("local command is empty")
	}
	command := exec.Command(args[0], args[1:]...) //nolint:gosec // Explicit developer command, without a shell.
	command.Env, command.Stdin = env, os.Stdin
	if err := configureLocalAppProcess(command); err != nil {
		return nil, err
	}
	process := &bridgeProcess{command: command, done: make(chan struct{})}
	// Own output readers so Wait observes the leader immediately, even when a
	// descendant holds stdout open. Kill descendants before waiting for EOF.
	var writes [2]*os.File
	started := false
	defer func() {
		for i := range writes {
			if writes[i] != nil {
				_ = writes[i].Close()
			}
			if !started && process.outputRead[i] != nil {
				_ = process.outputRead[i].Close()
			}
		}
	}()
	for i, output := range []io.Writer{osStdout, osStderr} {
		read, write, err := os.Pipe()
		if err != nil {
			return nil, fmt.Errorf("open local command output: %w", err)
		}
		process.outputRead[i], writes[i] = read, write
		done := make(chan struct{})
		process.outputDone[i] = done
		go func() { defer close(done); defer func() { _ = read.Close() }(); _, _ = io.Copy(output, read) }()
	}
	command.Stdout, command.Stderr = writes[0], writes[1]
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start local command: %w", err)
	}
	started = true
	go func() {
		process.err = command.Wait()
		if localAppProcessGroupAlive(command) {
			process.cleanupErr = signalLocalAppProcess(command, true)
		}
		close(process.done)
	}()
	return process, nil
}

func (p *bridgeProcess) exitCode() int {
	<-p.done
	if p.err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(p.err, &exit) && exit.ExitCode() >= 0 {
		return exit.ExitCode()
	}
	return 1
}

func (p *bridgeProcess) stop() error {
	var stopErr error
	select {
	case <-p.done:
	default:
		stopErr = signalLocalAppProcess(p.command, false)
		if !bridgeProcessWait(p.done) {
			stopErr = errors.Join(stopErr, signalLocalAppProcess(p.command, true))
			if !bridgeProcessWait(p.done) {
				return errors.Join(stopErr, errors.New("local command did not stop after termination"))
			}
		}
	}
	for i, done := range p.outputDone {
		if !bridgeProcessWait(done) {
			_ = p.outputRead[i].Close()
			stopErr = errors.Join(stopErr, errors.New("local command output did not close"))
		}
	}
	return errors.Join(stopErr, p.cleanupErr)
}

func bridgeProcessWait(done <-chan struct{}) bool {
	timer := time.NewTimer(api.DevBridgeLocalStopTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func bridgeWaitLocalReady(ctx context.Context, address, path string, process *bridgeProcess) error {
	ready, cancel := context.WithTimeout(ctx, api.DevBridgeLocalReadyTimeout)
	defer cancel()
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for {
		if process != nil {
			select {
			case <-process.done:
				return fmt.Errorf("local command exited before readiness (exit %d)", process.exitCode())
			default:
			}
		}
		if path == "" {
			conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ready, "tcp", address)
			if err == nil {
				_ = conn.Close()
				return nil
			}
		} else {
			request, err := http.NewRequestWithContext(ready, http.MethodGet, "http://"+address+path, nil)
			if err != nil {
				return errors.New("ready-path must be a local absolute path")
			}
			response, err := client.Do(request)
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode >= 200 && response.StatusCode < 300 {
					return nil
				}
			}
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ready.Done():
			timer.Stop()
			return errors.New("local service did not become ready; check PORT, the command and --ready-path")
		case <-timer.C:
		}
	}
}
