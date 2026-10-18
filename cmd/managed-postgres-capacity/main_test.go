package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestCapacityCommandValidatesTargetsBeforeOpeningConnections(t *testing.T) {
	for _, args := range [][]string{nil, {"--account", "private-credential"}, {"--account", uuid.NewString(), "--database", uuid.NewString()}, {"--account", uuid.Nil.String(), "--database", uuid.NewString(), "--allocation", "a.json"}} {
		if _, err := parseOptions(args, io.Discard); err == nil {
			t.Fatal("invalid target accepted")
		}
	}
	options, err := parseOptions([]string{"--account", uuid.NewString(), "--database", uuid.NewString(), "--allocation", "a.json", "--watch"}, io.Discard)
	if err != nil || !options.watch {
		t.Fatal(options, err)
	}
}

func TestCapacityCommandRereadsAllocationAndRedactsFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allocation.json")
	a := managedpostgres.QualifiedConnectionAllocation{Version: 1, Source: "provider_api", ProviderResourceID: "private-provider"}
	encoded, _ := json.Marshal(a)
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readAllocation(path); got == nil || got.ProviderResourceID != a.ProviderResourceID {
		t.Fatal("valid operator input lost")
	}
	if err := os.WriteFile(path, []byte(`{"unknown":"private-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	now := time.Now().UTC()
	err := runLoop(t.Context(), capacityOptions{allocation: path}, &output, io.Discard, func(_ context.Context, allocation *managedpostgres.QualifiedConnectionAllocation) (managedpostgres.ConnectionCapacityResult, error) {
		if allocation != nil {
			t.Error("invalid replacement reused old allocation")
		}
		return managedpostgres.ConnectionCapacityResult{State: "unknown", Reason: "allocation_unverified", CheckedAt: now, ExpiresAt: now}, nil
	})
	if err == nil || !strings.Contains(output.String(), "allocation_unverified") || strings.Contains(output.String(), "private") {
		t.Fatal(output.String(), err)
	}
	err = runLoop(t.Context(), capacityOptions{allocation: path}, io.Discard, io.Discard, func(context.Context, *managedpostgres.QualifiedConnectionAllocation) (managedpostgres.ConnectionCapacityResult, error) {
		return managedpostgres.ConnectionCapacityResult{}, errors.New("private-host private-password")
	})
	if err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("provider error escaped", err)
	}
}

func TestCapacityCommandWatchStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	err := runLoop(ctx, capacityOptions{allocation: filepath.Join(t.TempDir(), "missing.json"), watch: true}, io.Discard, io.Discard,
		func(context.Context, *managedpostgres.QualifiedConnectionAllocation) (managedpostgres.ConnectionCapacityResult, error) {
			calls++
			cancel()
			return managedpostgres.ConnectionCapacityResult{}, context.Canceled
		})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal(calls, err)
	}
}
