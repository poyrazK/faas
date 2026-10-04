//go:build linux

// adr:438
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type runtimeSecretProcessState struct {
	mu                            sync.Mutex
	generation, pending, previous string
	active                        bool
	transport                     func(runtimeConfigRequest) error
	startBudget                   time.Duration
}

func validGuestSecretProcessGeneration(value string) bool {
	if len(value) != 32 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func sendRuntimeSecretProcessRequest(req runtimeConfigRequest) error {
	conn, err := dialRuntimeConfigHost()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if err = writeRuntimeConfigFrame(conn, body); err != nil {
		return err
	}
	body, err = readRuntimeConfigFrame(conn)
	if err != nil {
		return err
	}
	var response runtimeConfigResponse
	if err = json.Unmarshal(body, &response); err != nil {
		return err
	}
	if response.Error != "" {
		return &runtimeConfigResponseError{code: response.Error}
	}
	if !response.Accepted || response.Generation != req.Generation {
		return errors.New("secret generation registration was not confirmed")
	}
	return nil
}

func (p *runtimeSecretProcessState) begin(workload string, sup *Supervisor) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == "" {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return "", fmt.Errorf("create secret execution identity: %w", err)
		}
		p.pending, p.previous = hex.EncodeToString(id[:]), p.generation
	}
	req := runtimeConfigRequest{Kind: "secret_generation_start", WorkloadName: workload, Generation: p.pending, PreviousGeneration: p.previous}
	deadline := time.Now().Add(p.startBudget)
	backoff := 100 * time.Millisecond
	var lastErr error
	for {
		if lastErr != nil && !time.Now().Before(deadline) {
			return "", fmt.Errorf("register secret execution before deadline: %w", lastErr)
		}
		if sup != nil && sup.stopRequested.Load() {
			return "", context.Canceled
		}
		err := p.transport(req)
		if err == nil {
			p.generation, p.active, p.pending = req.Generation, true, ""
			if sup != nil && sup.stopRequested.Load() {
				p.active = false
				_ = p.transport(runtimeConfigRequest{Kind: "secret_generation_retire", WorkloadName: workload, Generation: p.generation})
				return "", context.Canceled
			}
			return p.generation, nil
		}
		var rejected *runtimeConfigResponseError
		if errors.As(err, &rejected) && (rejected.code == "invalid_request" || rejected.code == "secret_generation_stale") {
			return "", err
		}
		lastErr = err
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return "", fmt.Errorf("register secret execution before deadline: %w", err)
		}
		time.Sleep(min(backoff, remaining))
		backoff = min(2*time.Second, backoff*2)
	}
}

func (p *runtimeSecretProcessState) retire(workload, generation string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.generation != generation {
		return nil
	} // A stale cleanup cannot retire a replacement.
	p.active = false
	return p.transport(runtimeConfigRequest{Kind: "secret_generation_retire", WorkloadName: workload, Generation: generation})
}

func (p *runtimeSecretProcessState) reconcile(workload string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.generation == "" {
		return
	}
	kind := "secret_generation_retire"
	if p.active {
		kind = "secret_generation_start"
	}
	// Reconciliation can create an absent record after snapshot restoration,
	// but cannot replace a different generation already registered by a start.
	previous := ""
	if p.active {
		previous = p.generation
	}
	_ = p.transport(runtimeConfigRequest{Kind: kind, WorkloadName: workload, Generation: p.generation, PreviousGeneration: previous})
}

func prepareRuntimeSecretProcess(cmd *exec.Cmd, secrets *runtimeSecretsState, sup *Supervisor, workload string) (func(), error) {
	if secrets == nil {
		return func() {}, nil
	}
	generation, err := secrets.process.begin(workload, sup)
	if err != nil {
		return nil, fmt.Errorf("prepare secret execution: %w", err)
	}
	cmd.Env = append(cmd.Env, SecretsReloadGenerationEnv+"="+generation)
	return func() {
		if err := secrets.process.retire(workload, generation); err != nil {
			slog.Default().Debug("guest-init: secret execution retirement unavailable", "err_kind", "report_failed")
		}
	}, nil
}
