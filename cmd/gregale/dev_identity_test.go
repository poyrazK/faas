package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func useTemporaryDeveloperConfig(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(developerIDEnvironment, "")
	_ = os.Unsetenv(developerIDEnvironment)
}

func TestLoadOrCreateDeveloperIDIsStableAndPrivate(t *testing.T) {
	useTemporaryDeveloperConfig(t)
	first, err := loadOrCreateDeveloperID()
	if err != nil {
		t.Fatalf("create developer ID: %v", err)
	}
	second, err := loadOrCreateDeveloperID()
	if err != nil {
		t.Fatalf("reload developer ID: %v", err)
	}
	if first != second || !validDeveloperID(first) {
		t.Fatalf("developer IDs = %q and %q, want same valid ID", first, second)
	}
	path, err := developerIDPath()
	if err != nil {
		t.Fatalf("developer ID path: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat developer ID: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("developer ID mode = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadOrCreateDeveloperIDConvergesAcrossConcurrentCalls(t *testing.T) {
	useTemporaryDeveloperConfig(t)
	const callers = 8
	ids := make(chan string, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := loadOrCreateDeveloperID()
			ids <- id
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent developer ID: %v", err)
		}
	}
	var want string
	for id := range ids {
		if want == "" {
			want = id
		}
		if id != want {
			t.Fatalf("concurrent IDs differ: got %q, want %q", id, want)
		}
	}
}

func TestLoadOrCreateDeveloperIDEnvironmentOverride(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	t.Setenv(developerIDEnvironment, id)
	got, err := loadOrCreateDeveloperID()
	if err != nil || got != id {
		t.Fatalf("environment developer ID = %q, %v; want %q", got, err, id)
	}
	t.Setenv(developerIDEnvironment, "ABCDEF0123456789ABCDEF0123456789")
	if _, err := loadOrCreateDeveloperID(); err == nil {
		t.Fatal("uppercase environment developer ID was accepted")
	}
}

func TestDeriveDevWorkspaceIDScopesDeveloperAndSource(t *testing.T) {
	const (
		developerA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		developerB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	sourceA := t.TempDir()
	sourceB := t.TempDir()
	first, err := deriveDevWorkspaceID(developerA, sourceA)
	if err != nil {
		t.Fatalf("derive first workspace: %v", err)
	}
	repeated, err := deriveDevWorkspaceID(developerA, sourceA)
	if err != nil {
		t.Fatalf("derive repeated workspace: %v", err)
	}
	otherDeveloper, err := deriveDevWorkspaceID(developerB, sourceA)
	if err != nil {
		t.Fatalf("derive other developer workspace: %v", err)
	}
	otherSource, err := deriveDevWorkspaceID(developerA, sourceB)
	if err != nil {
		t.Fatalf("derive other source workspace: %v", err)
	}
	if first != repeated || !validDeveloperID(first) {
		t.Fatalf("workspace identity is not stable: first=%q repeated=%q", first, repeated)
	}
	if first == otherDeveloper || first == otherSource {
		t.Fatalf("workspace identity was not scoped: first=%q developer=%q source=%q", first, otherDeveloper, otherSource)
	}
}
