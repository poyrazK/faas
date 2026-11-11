package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type devLocalAppOptions struct {
	root, command string
	plan          devLocalAppPlan
	env           []string
	envConfig     devLocalEnvConfig
	port          int
	noReload      bool
	out, errOut   io.Writer
}

// Both the child and supervisor write live output. Serialize them because a
// writer supplied by a caller (or a test) need not support concurrent writes.
type devLocalAppWriter struct {
	mu  *sync.Mutex
	out io.Writer
}

func (w devLocalAppWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.out.Write(p)
}

type devLocalAppProcess struct {
	cmd     *exec.Cmd
	done    chan struct{}
	err     error // published by closing done
	stopped bool
}

func startDevLocalAppProcess(root string, plan devLocalAppPlan, env []string, out, errOut io.Writer) (*devLocalAppProcess, error) {
	cmd := exec.Command("sh", "-c", plan.command)
	cmd.Dir, cmd.Env = root, env
	cmd.Stdout, cmd.Stderr = out, errOut
	cmd.WaitDelay = time.Second
	if err := configureLocalAppProcess(cmd); err != nil {
		return nil, fmt.Errorf("local HTTP apps need process-group support on this platform; use gregale dev: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start local app: %w", err)
	}
	p := &devLocalAppProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

func (p *devLocalAppProcess) stop() error {
	if p == nil || p.stopped {
		return nil
	}
	p.stopped = true
	termErr := signalLocalAppProcess(p.cmd, false)
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
	}
	// A launcher can exit while its server, watcher or compiler survives.
	// Kill the whole owned group even when the leader has already exited.
	killErr := signalLocalAppProcess(p.cmd, true)
	select {
	case <-p.done:
		return errors.Join(termErr, killErr)
	case <-time.After(3 * time.Second):
		return errors.Join(termErr, killErr, errors.New("local app did not exit after forced shutdown"))
	}
}

func serveDevLocalApp(ctx context.Context, opts devLocalAppOptions) error {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(opts.port)))
	if err != nil {
		return fmt.Errorf("listen on port %d (choose another --port): %w", opts.port, err)
	}
	defer func() { _ = ln.Close() }()
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("reserve local app port: %w", err)
	}
	backend := reservation.Addr().String()
	_, port, _ := net.SplitHostPort(backend)
	_ = reservation.Close() // arbitrary servers must bind PORT themselves
	env := devLocalAppEnv(opts.root, opts.env, port)
	var outputMu sync.Mutex
	opts.out = devLocalAppWriter{mu: &outputMu, out: opts.out}
	opts.errOut = devLocalAppWriter{mu: &outputMu, out: opts.errOut}
	var ready atomic.Bool
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	handler := devLocalAppProxy(backend, &ready, transport)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return runCtx }}
	served := make(chan error, 1)
	go func() { served <- server.Serve(ln) }()
	defer func() { _ = server.Close() }()
	_, _ = fmt.Fprintf(opts.out, "Local HTTP app at http://localhost:%d\nCommand: %s\n", ln.Addr().(*net.TCPAddr).Port, opts.plan.describe())
	_, _ = fmt.Fprintln(opts.out, "Runs on this machine; use gregale dev to check memory limits and snapshot behavior.")
	return superviseDevLocalApp(runCtx, opts, env, backend, &ready, served)
}

func devLocalAppProxy(backend string, ready *atomic.Bool, transport http.RoundTripper) http.Handler {
	target := &url.URL{Scheme: "http", Host: backend}
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(target)
			p.Out.Host = p.In.Host
			p.SetXForwarded()
			for name := range p.Out.Header {
				if strings.HasPrefix(strings.ToLower(name), "x-faas-") || strings.EqualFold(name, devLocalTokenHeader) {
					p.Out.Header.Del(name)
				}
			}
			p.Out.Header.Set(api.InvocationIDHeader, uuid.NewString())
			p.Out.Header.Set(api.DeadlineHeader, strconv.FormatInt(api.RequestBudgetDefault.Milliseconds(), 10))
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "Local app is unavailable; check its terminal output.", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == devLocalTriggerPath {
			http.Error(w, "Local triggers are available for functions; call this HTTP app's routes directly.", http.StatusNotFound)
			return
		}
		if !ready.Load() {
			http.Error(w, "Local app is starting or stopped; check its terminal output and save to retry.", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

func superviseDevLocalApp(ctx context.Context, opts devLocalAppOptions, env []string, backend string, ready *atomic.Bool, served <-chan error) (result error) {
	previous, err := opts.envConfig.sourceFingerprint(opts.root)
	if err != nil {
		return fmt.Errorf("watch local app source: %w", err)
	}
	process, err := startDevLocalAppProcess(opts.root, opts.plan, env, opts.out, opts.errOut)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, process.stop()) }()
	exited := process.done
	started := time.Now()
	candidate := previous
	var changedAt time.Time
	ticker := time.NewTicker(devLocalPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-served:
			return fmt.Errorf("local proxy stopped: %w", err)
		case <-exited:
			ready.Store(false)
			if err := process.stop(); err != nil {
				return err
			}
			err := process.err
			if err == nil {
				err = errors.New("app command exited")
			}
			if opts.noReload {
				return err
			}
			_, _ = fmt.Fprintf(opts.errOut, "Local app stopped (%v). Fix the source and save to restart.\n", err)
			process, exited = nil, nil
		case <-ticker.C:
			if process != nil && !ready.Load() {
				if localDevAppListening(ctx, backend) {
					ready.Store(true)
					_, _ = fmt.Fprintln(opts.out, "Local app is listening. Ctrl-C to stop.")
				} else if time.Since(started) > time.Minute {
					if err := process.stop(); err != nil {
						return err
					}
					process, exited = nil, nil
					err := errors.New("local app did not listen within 1m; the command must use HOST and PORT (override with --watch-command)")
					if opts.noReload {
						return err
					}
					_, _ = fmt.Fprintln(opts.errOut, err.Error()+"; fix the source and save to retry")
				}
			}
			if opts.noReload {
				continue
			}
			current, err := opts.envConfig.sourceFingerprint(opts.root)
			if err != nil {
				continue // editors can rename/remove files during a scan
			}
			if current != candidate {
				candidate, changedAt = current, time.Now()
			}
			if candidate == previous || changedAt.IsZero() || time.Since(changedAt) < devWatchSettleInterval {
				continue
			}
			previous = candidate
			plan, err := planDevLocalApp(opts.root, opts.command)
			if err != nil {
				_, _ = fmt.Fprintf(opts.errOut, "Could not reload local app: %v; fix the configuration and save again.\n", err)
				continue
			}
			baseEnv, err := opts.envConfig.load()
			if err != nil {
				_, _ = fmt.Fprintf(opts.errOut, "Could not reload local config: %v; fix the env file and save again.\n", err)
				continue
			}
			_, port, _ := net.SplitHostPort(backend)
			env = devLocalAppEnv(opts.root, baseEnv, port)
			ready.Store(false)
			if err := process.stop(); err != nil {
				return err
			}
			process, err = startDevLocalAppProcess(opts.root, plan, env, opts.out, opts.errOut)
			if err != nil {
				return err
			}
			exited, started = process.done, time.Now()
			_, _ = fmt.Fprintf(opts.out, "Reloading local app (%s).\n", plan.describe())
		}
	}
}

func localDevAppListening(ctx context.Context, address string) bool {
	dialCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", address)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
