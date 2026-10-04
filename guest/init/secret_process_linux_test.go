//go:build linux

// adr:438
package main

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRuntimeSecretProcessGenerationRetriesAndRestarts(t *testing.T) {
	var requests []runtimeConfigRequest
	lostResponse := errors.New("response lost")
	p := runtimeSecretProcessState{startBudget: time.Second, transport: func(req runtimeConfigRequest) error {
		requests = append(requests, req)
		if len(requests) == 1 {
			return lostResponse
		}
		return nil
	}}
	first, err := p.begin("worker", nil)
	if err != nil || !validGuestSecretProcessGeneration(first) || len(requests) != 2 || requests[0] != requests[1] {
		t.Fatalf("registration retry changed identity: %v requests=%+v", err, requests)
	}
	p.reconcile("worker")
	if requests[2].Generation != first || requests[2].PreviousGeneration != first {
		t.Fatal("reconciliation replaced live identity")
	}
	if err := p.retire("worker", first); err != nil {
		t.Fatal(err)
	}
	second, err := p.begin("worker", nil)
	if err != nil || first == second || requests[4].PreviousGeneration != first {
		t.Fatalf("restart reused generation: %v", err)
	}
	count := len(requests)
	if err := p.retire("worker", first); err != nil || len(requests) != count || !p.active {
		t.Fatal("stale cleanup retired replacement")
	}
	if err := p.retire("worker", second); err != nil {
		t.Fatal(err)
	}
	p.reconcile("worker")
	if last := requests[len(requests)-1]; last.Kind != "secret_generation_retire" || last.Generation != second || last.PreviousGeneration != "" {
		t.Fatal("retirement lost its identity")
	}
}

func TestRuntimeSecretProcessGenerationFailsClosed(t *testing.T) {
	for _, code := range []string{"invalid_request", "secret_generation_stale", "secrets_unavailable"} {
		t.Run(code, func(t *testing.T) {
			var requests []runtimeConfigRequest
			p := runtimeSecretProcessState{startBudget: 10 * time.Millisecond, transport: func(req runtimeConfigRequest) error {
				requests = append(requests, req)
				return &runtimeConfigResponseError{code: code}
			}}
			if _, err := p.begin("", nil); err == nil || p.active || len(requests) != 1 {
				t.Fatalf("unsafe registration: %v count=%d", err, len(requests))
			}
			pending := p.pending
			p.transport = func(req runtimeConfigRequest) error {
				if req.Generation != pending {
					t.Error("uncertain registration retry minted a different identity")
				}
				return nil
			}
			if _, err := p.begin("", nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeSecretProcessGenerationStopBeforeExec(t *testing.T) {
	for _, during := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "during"}[during], func(t *testing.T) {
			sup := &Supervisor{}
			if !during {
				sup.stopRequested.Store(true)
			}
			var requests []runtimeConfigRequest
			p := runtimeSecretProcessState{startBudget: time.Second, transport: func(req runtimeConfigRequest) error {
				requests = append(requests, req)
				sup.stopRequested.Store(true)
				return nil
			}}
			if _, err := p.begin("", sup); !errors.Is(err, context.Canceled) || p.active {
				t.Fatal("shutdown admitted an execution")
			}
			if !during && len(requests) != 0 || during && (len(requests) != 2 || requests[1].Kind != "secret_generation_retire") {
				t.Fatal("shutdown registration was not retired")
			}
		})
	}
}

func TestRuntimeSecretProcessGenerationStampedBeforeExec(t *testing.T) {
	secrets := newRuntimeSecretsState(nil)
	registered := false
	secrets.process.transport = func(req runtimeConfigRequest) error {
		if req.Kind == "secret_generation_start" {
			registered = true
		}
		return nil
	}
	cmd := exec.Command("/bin/sh", "-c", `printf '%s' "$FAAS_SECRETS_RELOAD_GENERATION"`)
	cmd.Env = StampSecretsFileEnv([]string{SecretsReloadGenerationEnv + "=spoof"}, true)
	cleanup, err := prepareRuntimeSecretProcess(cmd, secrets, nil, "")
	if err != nil || !registered {
		t.Fatalf("exec prepared without registration: %v", err)
	}
	body, err := cmd.Output()
	if err != nil || string(body) != secrets.process.generation || strings.Contains(string(body), "spoof") {
		t.Fatalf("wrong exec generation: %v", err)
	}
	cleanup()
	if secrets.process.active {
		t.Fatal("cleanup left execution active")
	}
}
