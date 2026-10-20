package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
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
	t.Setenv(main.DeveloperIDEnvironment, "")
	_ = os.Unsetenv(main.DeveloperIDEnvironment)
}

func TestLoadOrCreateDeveloperIDIsStableAndPrivate(t *testing.T) {
	useTemporaryDeveloperConfig(t)
	first, err := main.LoadOrCreateDeveloperID()
	if err != nil {
		t.Fatalf("create developer ID: %v", err)
	}
	second, err := main.LoadOrCreateDeveloperID()
	if err != nil {
		t.Fatalf("reload developer ID: %v", err)
	}
	if first != second || !main.ValidDeveloperID(first) {
		t.Fatalf("developer IDs = %q and %q, want same valid ID", first, second)
	}
	path, err := main.DeveloperIDPath()
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
			id, err := main.LoadOrCreateDeveloperID()
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
	t.Setenv(main.DeveloperIDEnvironment, id)
	got, err := main.LoadOrCreateDeveloperID()
	if err != nil || got != id {
		t.Fatalf("environment developer ID = %q, %v; want %q", got, err, id)
	}
	t.Setenv(main.DeveloperIDEnvironment, "ABCDEF0123456789ABCDEF0123456789")
	if _, err := main.LoadOrCreateDeveloperID(); err == nil {
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
	first, err := main.DeriveDevWorkspaceID(developerA, sourceA)
	if err != nil {
		t.Fatalf("derive first workspace: %v", err)
	}
	repeated, err := main.DeriveDevWorkspaceID(developerA, sourceA)
	if err != nil {
		t.Fatalf("derive repeated workspace: %v", err)
	}
	otherDeveloper, err := main.DeriveDevWorkspaceID(developerB, sourceA)
	if err != nil {
		t.Fatalf("derive other developer workspace: %v", err)
	}
	otherSource, err := main.DeriveDevWorkspaceID(developerA, sourceB)
	if err != nil {
		t.Fatalf("derive other source workspace: %v", err)
	}
	if first != repeated || !main.ValidDeveloperID(first) {
		t.Fatalf("workspace identity is not stable: first=%q repeated=%q", first, repeated)
	}
	if first == otherDeveloper || first == otherSource {
		t.Fatalf("workspace identity was not scoped: first=%q developer=%q source=%q", first, otherDeveloper, otherSource)
	}
}
