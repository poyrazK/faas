package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMemStoreBuildVMCleanup_RetryAndTokenGuard(t *testing.T) {
	m := NewMemStore()
	m.builderVMCleanup["build-1"] = builderVMCleanupRow{
		nextAttemptAt: time.Now().UTC().Add(-time.Minute),
	}

	claims, err := m.ClaimBuildVMCleanup(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].BuildID != "build-1" || claims[0].ClaimToken == "" {
		t.Fatalf("first claims = %+v", claims)
	}
	firstToken := claims[0].ClaimToken

	if err := m.CompleteBuildVMCleanup(context.Background(), "build-1", firstToken, errors.New(strings.Repeat("x", 5000))); err != nil {
		t.Fatal(err)
	}
	claims, err = m.ClaimBuildVMCleanup(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].ClaimToken == firstToken {
		t.Fatalf("retry claims = %+v, want a new token", claims)
	}
	secondToken := claims[0].ClaimToken

	// A late result from the first attempt must not delete the second claim.
	if err := m.CompleteBuildVMCleanup(context.Background(), "build-1", firstToken, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.builderVMCleanup["build-1"]; !ok {
		t.Fatal("stale completion deleted the newer cleanup claim")
	}
	if err := m.CompleteBuildVMCleanup(context.Background(), "build-1", secondToken, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.builderVMCleanup["build-1"]; ok {
		t.Fatal("successful completion left a cleanup row")
	}
}
