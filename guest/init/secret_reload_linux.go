//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const runtimeSecretPollInterval = 10 * time.Second

type runtimeSecretsState struct {
	mu         sync.RWMutex
	secrets    map[string]string
	revision   string
	projection *runtimeSecretProjection
	process    runtimeSecretProcessState
}

// Projection identity is resolved once against the workload's image, before
// either its supervisor or reload worker starts.
type runtimeSecretProjection struct {
	secretsPath, revisionPath string
	uid, dirUID, dirGID       int
}

func newProjectedRuntimeSecretsState(initial map[string]string, projection runtimeSecretProjection) (*runtimeSecretsState, error) {
	s := newRuntimeSecretsState(initial)
	s.projection = &projection
	if err := s.publishProjection(initial, ""); err != nil {
		return nil, fmt.Errorf("prepare runtime secret projection: %w", err)
	}
	return s, nil
}

func (s *runtimeSecretsState) publishProjection(secrets map[string]string, revision string) error {
	if s == nil || s.projection == nil {
		return errors.New("runtime secret projection is unavailable")
	}
	p := s.projection
	return s.publishForOwner(p.secretsPath, p.revisionPath, p.uid, p.dirUID, p.dirGID, secrets, revision)
}

func (s *runtimeSecretsState) publishRevision(revision string) error {
	if s == nil || s.projection == nil {
		return errors.New("runtime secret projection is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projection
	if err := p.publish(s.secrets, revision); err != nil {
		return err
	}
	s.revision = revision
	return nil
}

func newRuntimeSecretsState(initial map[string]string) *runtimeSecretsState {
	return &runtimeSecretsState{secrets: cloneRuntimeSecrets(initial), process: runtimeSecretProcessState{transport: sendRuntimeSecretProcessRequest, startBudget: 30 * time.Second}}
}

func (s *runtimeSecretsState) snapshot() map[string]string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneRuntimeSecrets(s.secrets)
}

func (s *runtimeSecretsState) startupSnapshot() runtimeSecretSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return runtimeSecretSnapshot{Revision: s.revision, Secrets: cloneRuntimeSecrets(s.secrets)}
}

// Publish a complete generation before changing the restart snapshot. Readers
// of snapshot.json get the values and revision from the same immutable file.
func (s *runtimeSecretsState) publishForOwner(path, revisionPath string, uid, dirUID, dirGID int, secrets map[string]string, revision string) error {
	if s == nil {
		return errors.New("runtime secrets state is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := runtimeSecretProjection{secretsPath: path, revisionPath: revisionPath, uid: uid, dirUID: dirUID, dirGID: dirGID}
	if err := p.publish(secrets, revision); err != nil {
		return err
	}
	s.secrets = cloneRuntimeSecrets(secrets)
	s.revision = revision
	return nil
}

func cloneRuntimeSecrets(secrets map[string]string) map[string]string {
	clone := make(map[string]string, len(secrets))
	for key, value := range secrets {
		clone[key] = value
	}
	return clone
}

func runtimeSecretsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if other, ok := b[key]; !ok || other != value {
			return false
		}
	}
	return true
}

func startRuntimeSecretReloader(ctx context.Context, manifest api.AppManifest, secrets *runtimeSecretsState, sup *Supervisor, log *slog.Logger) {
	startRuntimeSecretReloaderForWorkload(ctx, manifest, secrets, sup, log, "")
}

func startRuntimeSecretReloaderForWorkload(ctx context.Context, manifest api.AppManifest, secrets *runtimeSecretsState, sup *Supervisor, log *slog.Logger, workloadName string) <-chan struct{} {
	if ctx == nil || secrets == nil || secrets.projection == nil || sup == nil || manifest.SecretReloadSignal == "" {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runRuntimeSecretReloadWorker(ctx, secrets, sup, log, workloadName, secretReloadSyscall(manifest.SecretReloadSignal), runtimeSecretReloadIO{
			fetch: fetchRuntimeSecretsForWorkload, report: sendRuntimeSecretReloadReport,
			send: sendRuntimeSecretSignal, pollInterval: runtimeSecretPollInterval, tickInterval: time.Second,
		})
	}()
	return done
}

type runtimeSecretReloadIO struct {
	fetch                      func(string, string) (runtimeConfigResponse, error)
	report                     func(runtimeSecretReloadReport) (bool, bool, error)
	send                       func(*os.Process, syscall.Signal) error
	pollInterval, tickInterval time.Duration
}

// The worker owns one pending notification, fenced to the latest published
// revision. Fetch, delivery and observation retries are independent.
func runRuntimeSecretReloadWorker(ctx context.Context, secrets *runtimeSecretsState, sup *Supervisor, log *slog.Logger, workloadName string, signal syscall.Signal, transport runtimeSecretReloadIO) {
	ticker := time.NewTicker(transport.tickInterval)
	defer ticker.Stop()
	lastRevision := ""
	var nextFetch time.Time
	var notification *runtimeSecretNotification
	needsNotification := false
	var nextProcessSync time.Time
	var pendingReport, lastReport *runtimeSecretReloadReport
	setReport := func(report runtimeSecretReloadReport) {
		if lastReport == nil || *lastReport != report {
			lastReport = &report
			pendingReport = &report
		}
	}
	for {
		if ctx.Err() != nil {
			return
		}
		now := time.Now()
		if !now.Before(nextFetch) {
			nextFetch = now.Add(transport.pollInterval)
			response, err := transport.fetch(workloadName, lastRevision)
			switch {
			case err != nil:
				log.Debug("guest-init: runtime secret refresh unavailable", "err_kind", "fetch_failed")
			case response.Error != "":
				log.Debug("guest-init: runtime secret refresh rejected", "reason", response.Error)
			case !validGuestRuntimeSecretRevision(response.Revision):
				lastRevision = ""
				log.Debug("guest-init: runtime secret revision was invalid", "err_kind", "invalid_response")
			case response.Unchanged:
				if response.Revision != lastRevision {
					lastRevision = ""
				}
			case response.Secrets == nil:
				lastRevision = ""
				log.Debug("guest-init: runtime secret response omitted its payload", "err_kind", "invalid_response")
			default:
				fresh := cloneRuntimeSecrets(*response.Secrets)
				changed := !runtimeSecretsEqual(secrets.snapshot(), fresh)
				if changed {
					err = secrets.publishProjection(fresh, response.Revision)
				} else {
					err = secrets.publishRevision(response.Revision)
				}
				if err != nil {
					// Never notify an older revision after a known newer fetch failed to
					// publish, nor let a failed write advance the restart snapshot.
					notification = nil
					lastRevision = ""
					setReport(runtimeSecretReloadReport{Revision: response.Revision, WorkloadName: workloadName, Projection: "failed", Signal: "not_attempted", ErrorCode: "projection_failed"})
					log.Warn("guest-init: runtime secret projection update failed", "err_kind", "write_failed")
				} else {
					lastRevision = response.Revision
					if changed || needsNotification {
						needsNotification = true
						if notification == nil || notification.revision != response.Revision {
							notification = &runtimeSecretNotification{revision: response.Revision, values: fresh}
						}
					} else {
						setReport(runtimeSecretReloadReport{Revision: response.Revision, WorkloadName: workloadName, Projection: "unchanged", Signal: "not_attempted"})
					}
				}
			}
		}
		if ctx.Err() != nil {
			return
		}
		if notification != nil {
			// A fetch may block for several seconds. Backoff starts at the
			// delivery attempt, rather than the beginning of that fetch.
			status, attempted := notification.attempt(time.Now(), sup, signal, transport.send)
			if attempted {
				report := runtimeSecretReloadReport{Revision: notification.revision, WorkloadName: workloadName, Projection: "updated", Signal: status}
				if status == "failed" {
					report.ErrorCode = "signal_failed"
				}
				setReport(report)
				if status == "sent" || status == "not_attempted" {
					notification = nil
					needsNotification = false
				}
			}
		}
		if pendingReport != nil {
			accepted, stale, err := transport.report(*pendingReport)
			if err == nil && (accepted || stale) {
				pendingReport = nil
			}
		}
		if time.Now().After(nextProcessSync) {
			secrets.process.reconcile(workloadName)
			nextProcessSync = time.Now().Add(runtimeSecretPollInterval)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type runtimeSecretReloadReport struct {
	Revision     string
	WorkloadName string
	Projection   string
	Signal       string
	ErrorCode    string
}

func sendRuntimeSecretReloadReport(report runtimeSecretReloadReport) (accepted, stale bool, err error) {
	conn, err := dialRuntimeConfigHost()
	if err != nil {
		return false, false, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	body, err := json.Marshal(runtimeConfigRequest{
		Kind: "secret_reload_status", WorkloadName: report.WorkloadName, Revision: report.Revision,
		Projection: report.Projection, Signal: report.Signal, ErrorCode: report.ErrorCode,
	})
	if err != nil {
		return false, false, err
	}
	if err := writeRuntimeConfigFrame(conn, body); err != nil {
		return false, false, err
	}
	frame, err := readRuntimeConfigFrame(conn)
	if err != nil {
		return false, false, err
	}
	var response runtimeConfigResponse
	if err := json.Unmarshal(frame, &response); err != nil {
		return false, false, err
	}
	if response.Error == "secret_reload_stale" {
		return false, true, nil
	}
	if response.Error != "" || !response.Accepted {
		return false, false, errors.New("runtime secret reload status rejected")
	}
	return true, false, nil
}

func validGuestRuntimeSecretRevision(revision string) bool {
	if len(revision) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(revision)
	return err == nil && len(decoded) == sha256.Size
}

func fetchRuntimeSecretsForWorkload(workloadName, revision string) (runtimeConfigResponse, error) {
	conn, err := dialRuntimeConfigHost()
	if err != nil {
		return runtimeConfigResponse{}, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	body, err := json.Marshal(runtimeConfigRequest{Kind: "secrets", WorkloadName: workloadName, Revision: revision})
	if err != nil {
		return runtimeConfigResponse{}, err
	}
	if err := writeRuntimeConfigFrame(conn, body); err != nil {
		return runtimeConfigResponse{}, err
	}
	frame, err := readRuntimeConfigFrame(conn)
	if err != nil {
		return runtimeConfigResponse{}, err
	}
	var response runtimeConfigResponse
	if err := json.Unmarshal(frame, &response); err != nil {
		return runtimeConfigResponse{}, err
	}
	return response, nil
}

func secretReloadSyscall(name string) syscall.Signal {
	switch name {
	case "SIGUSR1":
		return syscall.SIGUSR1
	case "SIGUSR2":
		return syscall.SIGUSR2
	default:
		return syscall.SIGHUP
	}
}

func writeRuntimeSecretRevisionProjectionForOwner(path string, uid, dirUID, dirGID int, revision string) error {
	if revision != "" && !validGuestRuntimeSecretRevision(revision) {
		return errors.New("invalid runtime secret revision")
	}
	return writePrivateRuntimeSecretFile(path, uid, dirUID, dirGID, []byte(revision))
}

func writeRuntimeSecretsProjectionForOwner(path string, uid, dirUID, dirGID int, secrets map[string]string) error {
	body, err := json.Marshal(cloneRuntimeSecrets(secrets))
	if err != nil {
		return fmt.Errorf("encode runtime secret projection: %w", err)
	}
	return writePrivateRuntimeSecretFile(path, uid, dirUID, dirGID, body)
}
