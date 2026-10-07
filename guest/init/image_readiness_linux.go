//go:build linux

// adr:643
package main

import (
	"context"
	"crypto/rand"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/healthcheckproto"
)

// Wiring only: every launched main command installs a new runtime. Retirement
// cancels in-flight probes; neither a restart nor a snapshot reuses a pass.
var mainImageReadiness atomic.Pointer[imageReadinessRuntime]

type imageReadinessRuntime struct {
	id          string
	monitoring  atomic.Bool
	manifest    api.AppManifest
	env         []string
	environment func() []string
	dir         string
	procAttr    *syscall.SysProcAttr
	startedAt   time.Time
	ctx         context.Context
	busy        chan struct{}
}

func installImageReadinessRuntime(cmd *exec.Cmd, manifest api.AppManifest, environment ...func() []string) func() {
	ctx, cancel := context.WithCancel(context.Background())
	attr := *cmd.SysProcAttr
	// Use the already opened main workload cgroup while this command is alive.
	runtime := &imageReadinessRuntime{id: rand.Text(), manifest: manifest, env: append([]string(nil), cmd.Env...),
		dir: cmd.Dir, procAttr: &attr, startedAt: time.Now(), ctx: ctx, busy: make(chan struct{}, 1)}
	if len(environment) > 0 {
		runtime.environment = environment[0]
	}
	mainImageReadiness.Store(runtime)
	return func() {
		mainImageReadiness.CompareAndSwap(runtime, nil)
		cancel()
	}
}

func handleImageReadinessConn(f *os.File, size uint32, log *slog.Logger) {
	var req healthcheckproto.Request
	if err := healthcheckproto.ReadBody(f, size, &req); err != nil {
		return
	}
	resp := healthcheckproto.Response{Nonce: req.Nonce}
	if err := req.Validate(int64(api.MaxAppManifestStartupDeadlineS) * 1000); err != nil {
		resp.Error = "invalid_request"
	} else if runtime := mainImageReadiness.Load(); runtime == nil {
		resp.Error = "runtime_unavailable"
	} else {
		resp.RuntimeID = runtime.id
		ctx, cancel := context.WithTimeout(runtime.ctx, time.Duration(req.BudgetMS)*time.Millisecond)
		defer cancelImageCheckOnDisconnect(ctx, cancel, f)()
		resp.Error = runtime.check(ctx, log)
		if resp.Error == "" && mainImageReadiness.Load() != runtime {
			resp.Error = "runtime_changed"
		}
		resp.Healthy = resp.Error == ""
	}
	_ = healthcheckproto.Write(f, healthcheckproto.Ack, resp)
}

// A host can cancel a command while the guest remains alive (park, daemon
// shutdown, or sibling recovery). Shut down only this request's read side to
// join its disconnect watcher without retaining a blocked socket goroutine.
func cancelImageCheckOnDisconnect(ctx context.Context, cancel context.CancelFunc, f *os.File) func() {
	readerDone, shutdownDone := make(chan struct{}), make(chan struct{})
	fd := int(f.Fd())
	context.AfterFunc(ctx, func() {
		_ = syscall.Shutdown(fd, syscall.SHUT_RD)
		close(shutdownDone)
	})
	go func() {
		defer close(readerDone)
		var extra [1]byte
		_, _ = f.Read(extra[:])
		cancel()
	}()
	return func() {
		cancel()
		<-shutdownDone
		<-readerDone
	}
}

func (r *imageReadinessRuntime) check(ctx context.Context, log *slog.Logger) string {
	return r.runCheck(ctx, log, false)
}

func (r *imageReadinessRuntime) runCheck(ctx context.Context, log *slog.Logger, once bool) string {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.ctx, cancel) //nolint:contextcheck // Process retirement independently cancels this request.
	defer cancel()
	defer stop()
	select {
	case r.busy <- struct{}{}:
		defer func() { <-r.busy }()
	case <-ctx.Done():
		return "deadline"
	}
	check := r.manifest.Healthcheck
	if check == nil {
		return "healthcheck_missing"
	}
	argv, _ := parseHealthcheckTest(check.Test)
	if len(argv) == 0 {
		return "healthcheck_missing"
	}
	interval, timeout, grace, retries := healthcheckDefaults(check)
	failures := 0
	for {
		if ctx.Err() != nil {
			return "deadline"
		}
		started := time.Now()
		env := r.env
		if r.environment != nil {
			env = r.environment()
		}
		probeArgv := append([]string(nil), argv...)
		probeArgv[0] = resolveWorkloadCommandPath("/", probeArgv[0], env)
		report := execHealthcheckWithOptions(ctx, probeArgv, timeout, 0, env, r.dir, r.procAttr, log)
		if r.ctx.Err() != nil {
			return "runtime_changed"
		}
		if ctx.Err() != nil {
			return "deadline"
		}
		if report.Status == healthcheckStatusPass {
			return ""
		}
		if once {
			if healthcheckWithinStartupGrace(check, r.startedAt, started, grace) {
				return "starting"
			}
			return "unhealthy"
		}
		if !healthcheckWithinStartupGrace(check, r.startedAt, started, grace) {
			failures++
			if failures >= retries {
				return "unhealthy"
			}
		}
		if !waitProbeDelay(ctx, imageHealthcheckPollDelay(check, time.Since(r.startedAt), interval, grace)) {
			return "deadline"
		}
	}
}

// Refresh only scoped secret bindings, retaining the main process's stamped
// platform environment. Removed bindings recover the manifest/API fallback.
func imageReadinessSecretEnvironment(started []string, manifest api.AppManifest, initial, current, apiEnv map[string]string) []string {
	fallback := make(map[string]string)
	for key := range initial {
		if _, exists := current[key]; exists {
			continue
		}
		if value, exists := apiEnv[key]; exists {
			fallback[key] = value
		} else if value, exists := manifest.Env[key]; exists {
			fallback[key] = value
		}
	}
	base := make([]string, 0, len(started))
	for _, binding := range started {
		key, _, _ := strings.Cut(binding, "=")
		_, oldSecret := initial[key]
		_, newSecret := current[key]
		if !oldSecret && !newSecret {
			base = append(base, binding)
		}
	}
	return StampOverridePortEnv(BuildEnvWithSecrets(base, api.AppManifest{Env: fallback}, current, nil), manifest.EffectivePort())
}
