//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// adr:436 A ready marker belongs to one exec generation, rather than the
// supervisor lifetime. Its platform-owned parent prevents path replacement.
func prepareRuntimeSecretReadyFile(projection *runtimeSecretProjection, imageRoot string, required bool) (hostPath, guestPath string, err error) {
	if !required {
		return "", "", nil
	}
	if projection == nil {
		return "", "", errors.New("runtime secret readiness requires a projection")
	}
	file, err := os.CreateTemp(filepath.Dir(projection.secretsPath), "ready-*")
	if err != nil {
		return "", "", fmt.Errorf("prepare runtime secret ready marker: %w", err)
	}
	hostPath = file.Name()
	defer func() {
		_ = file.Close()
		if err != nil {
			_ = os.Remove(hostPath)
		}
	}()
	if err = file.Chown(projection.uid, -1); err != nil {
		return hostPath, "", fmt.Errorf("own runtime secret ready marker: %w", err)
	}
	guestPath = hostPath
	if imageRoot != "" && imageRoot != "/" {
		relative, relErr := filepath.Rel(imageRoot, hostPath)
		if relErr != nil {
			return hostPath, "", fmt.Errorf("resolve runtime secret ready marker: %w", relErr)
		}
		if relative == ".." || strings.HasPrefix(relative, "../") {
			return hostPath, "", errors.New("runtime secret ready marker is outside the image")
		}
		guestPath = "/" + relative
	}
	return hostPath, guestPath, nil
}

func runtimeSecretReady(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	// The parent is the prepared, platform-owned projection directory; the
	// tenant can write this generation's file but cannot replace its path.
	file, err := os.Open(path) //nolint:forbidigo // platform-created per-exec marker, not a customer path.
	if err != nil {
		return false, fmt.Errorf("read runtime secret ready marker: %w", err)
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, 7))
	if err != nil {
		return false, fmt.Errorf("read runtime secret ready marker: %w", err)
	}
	return string(body) == "ready\n", nil
}

func (s *Supervisor) trackRuntimeSecretCommand(cmd *exec.Cmd, snapshot runtimeSecretSnapshot, readyPath string) {
	s.TrackCommand(cmd)
	s.startSignalMu.Lock()
	s.runtimeSecretStart = &runtimeSecretProcessStart{
		cmd: cmd, secrets: cloneRuntimeSecrets(snapshot.Secrets), revision: snapshot.Revision, readyPath: readyPath,
	}
	s.startSignalMu.Unlock()
}

func (s *Supervisor) retireRuntimeSecretCommand(cmd *exec.Cmd) {
	s.startSignalMu.Lock()
	defer s.startSignalMu.Unlock()
	if start := s.runtimeSecretStart; start != nil && start.cmd == cmd {
		start.process = nil
	}
}

// not_attempted means this exec already received the current values. It is
// neither a signal syscall nor proof that the application applied them.
func (s *Supervisor) deliverRuntimeSecretReload(sig syscall.Signal, values map[string]string, send func(*os.Process, syscall.Signal) error) (string, error) {
	s.startSignalMu.Lock()
	defer s.startSignalMu.Unlock()
	start := s.runtimeSecretStart
	if start == nil || start.process == nil || s.stopRequested.Load() {
		return "queued", nil
	}
	if err := start.process.Signal(syscall.Signal(0)); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return "queued", nil
		}
		return "failed", fmt.Errorf("check runtime secret reload process: %w", err)
	}
	if !start.notified && runtimeSecretsEqual(start.secrets, values) {
		return "not_attempted", nil
	}
	ready := start.healthy // legacy images keep their startup-gate contract.
	if start.readyPath != "" {
		var err error
		ready, err = runtimeSecretReady(start.readyPath)
		if err != nil {
			return "failed", err
		}
	}
	if !ready {
		return "queued", nil
	}
	if err := send(start.process, sig); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return "queued", nil
		}
		return "failed", fmt.Errorf("deliver runtime secret reload signal: %w", err)
	}
	start.notified = true
	return "sent", nil
}

func sendRuntimeSecretSignal(process *os.Process, sig syscall.Signal) error {
	return process.Signal(sig)
}

type runtimeSecretNotification struct {
	revision    string
	values      map[string]string
	nextAttempt time.Time
	backoff     time.Duration
	start       *runtimeSecretProcessStart
}

func (n *runtimeSecretNotification) attempt(now time.Time, sup *Supervisor, sig syscall.Signal, send func(*os.Process, syscall.Signal) error) (status string, attempted bool) {
	sup.startSignalMu.Lock()
	start := sup.runtimeSecretStart
	sup.startSignalMu.Unlock()
	if n.start != start {
		n.start = start
		n.nextAttempt = time.Time{}
		n.backoff = 0
	}
	if now.Before(n.nextAttempt) {
		return "", false
	}
	status, _ = sup.deliverRuntimeSecretReload(sig, n.values, send)
	if status == "failed" {
		if n.backoff == 0 {
			n.backoff = time.Second
		} else {
			n.backoff = min(2*n.backoff, time.Minute)
		}
		n.nextAttempt = now.Add(n.backoff)
	} else {
		n.nextAttempt = now.Add(time.Second)
	}
	return status, true
}
