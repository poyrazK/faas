package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFallbackStorageBackendMigrationSemantics(t *testing.T) {
	primary, err := NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("primary: %v", err)
	}
	legacy, err := NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("legacy: %v", err)
	}
	backend, err := NewFallbackStorageBackend(primary, legacy)
	if err != nil {
		t.Fatalf("NewFallbackStorageBackend: %v", err)
	}
	ctx := t.Context()
	if err := legacy.Put(ctx, "kernel/legacy", strings.NewReader("old")); err != nil {
		t.Fatalf("legacy Put: %v", err)
	}
	reader, err := backend.Get(ctx, "kernel/legacy")
	if err != nil {
		t.Fatalf("fallback Get: %v", err)
	}
	body, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(body) != "old" {
		t.Fatalf("fallback body=%q err=%v", body, err)
	}
	if err := backend.Put(ctx, "kernel/current", strings.NewReader("new")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := legacy.Get(ctx, "kernel/current"); !IsNotFound(err) {
		t.Fatalf("write leaked to legacy backend: %v", err)
	}
	keys, err := backend.List(ctx, "kernel/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 || keys[0] != "kernel/current" || keys[1] != "kernel/legacy" {
		t.Fatalf("keys = %v", keys)
	}
	if err := backend.Delete(ctx, "kernel/legacy"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := legacy.Get(ctx, "kernel/legacy"); !IsNotFound(err) {
		t.Fatalf("legacy key survived Delete: %v", err)
	}
}

type errorStorageBackend struct {
	err error
}

func (b errorStorageBackend) Put(_ context.Context, _ string, _ io.Reader) error { return b.err }
func (b errorStorageBackend) Get(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, b.err
}
func (b errorStorageBackend) Delete(_ context.Context, _ string) error { return b.err }
func (b errorStorageBackend) List(_ context.Context, _ string) ([]string, error) {
	return nil, b.err
}

func TestFallbackStorageBackendDoesNotFailOpenOnPrimaryError(t *testing.T) {
	primaryErr := errors.New("GCS permission denied")
	legacy, err := NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("legacy: %v", err)
	}
	if err := legacy.Put(t.Context(), "kernel/1.7.0", strings.NewReader("legacy")); err != nil {
		t.Fatalf("legacy Put: %v", err)
	}
	backend, err := NewFallbackStorageBackend(errorStorageBackend{err: primaryErr}, legacy)
	if err != nil {
		t.Fatalf("NewFallbackStorageBackend: %v", err)
	}
	if _, err := backend.Get(t.Context(), "kernel/1.7.0"); !errors.Is(err, primaryErr) {
		t.Fatalf("Get err = %v, want primary error", err)
	}
}
