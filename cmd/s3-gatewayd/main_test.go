package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/wire"
)

// spec: §11
type readinessPingerStub struct {
	mu  sync.RWMutex
	err error
}

func (p *readinessPingerStub) Ping(context.Context) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.err
}

func (p *readinessPingerStub) setError(err error) {
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
}

func waitForReadiness(t *testing.T, probe *wire.ReadyzProbe, want bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if ready, _ := probe.All(); ready == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	ready, reason := probe.All()
	t.Fatalf("readiness = %v (%q), want %v", ready, reason, want)
}

func TestBuildReadinessProbeTracksDatabase(t *testing.T) {
	stub := &readinessPingerStub{}
	probe := buildReadinessProbe(context.Background(), stub, 20*time.Millisecond)
	defer probe.Drain("test", nil)

	waitForReadiness(t, probe, true)
	stub.setError(errors.New("database unavailable"))
	waitForReadiness(t, probe, false)
}

func TestBuildReadinessProbeFailsClosedWithoutDatabase(t *testing.T) {
	probe := buildReadinessProbe(context.Background(), nil, 20*time.Millisecond)
	defer probe.Drain("test", nil)

	ready, reason := probe.All()
	if ready {
		t.Fatal("readiness = true, want false")
	}
	if reason != "database pool unavailable" {
		t.Fatalf("reason = %q, want database pool unavailable", reason)
	}
}

func writeTestIdentity(t *testing.T, path string) {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(identity.String()), 0o400); err != nil {
		t.Fatal(err)
	}
}

func TestLoadIdentitiesAllowsMissingOptionalPreviousCredential(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "host.age")
	writeTestIdentity(t, current)

	identities, err := loadIdentities(func(name string) string {
		switch name {
		case "FAAS_HOST_AGE_IDENTITY_PATH":
			return current
		case "FAAS_HOST_AGE_PREVIOUS_IDENTITY_PATH":
			return filepath.Join(dir, "host.age.previous")
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 1 {
		t.Fatalf("identities = %d, want current identity only", len(identities))
	}
}

func TestLoadIdentitiesRejectsInvalidExistingPreviousCredential(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "host.age")
	previous := filepath.Join(dir, "host.age.previous")
	writeTestIdentity(t, current)
	if err := os.WriteFile(previous, []byte("not-an-age-identity\n"), 0o400); err != nil {
		t.Fatal(err)
	}

	_, err := loadIdentities(func(name string) string {
		if name == "FAAS_HOST_AGE_IDENTITY_PATH" {
			return current
		}
		if name == "FAAS_HOST_AGE_PREVIOUS_IDENTITY_PATH" {
			return previous
		}
		return ""
	})
	if err == nil {
		t.Fatal("err = nil, want invalid previous identity failure")
	}
}
