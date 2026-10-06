//go:build linux

// adr: 435

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

func runtimeSecretSnapshotFixture(t *testing.T) (*runtimeSecretsState, runtimeSecretProjection) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "projection")
	p := runtimeSecretProjection{secretsPath: filepath.Join(dir, "secrets.json"), revisionPath: filepath.Join(dir, "revision"), uid: os.Getuid(), dirUID: os.Getuid(), dirGID: os.Getgid()}
	s, err := newProjectedRuntimeSecretsState(map[string]string{"DATABASE_URL": "old", "TOKEN": "old"}, p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.publishProjection(s.snapshot(), strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	return s, p
}

func readRuntimeSecretSnapshot(t *testing.T, path string) runtimeSecretSnapshot {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot runtimeSecretSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		t.Fatal("invalid snapshot JSON")
	}
	return snapshot
}

func TestRuntimeSecretSnapshotRevisionAndRevocation(t *testing.T) {
	s, p := runtimeSecretSnapshotFixture(t)
	snapshotPath := filepath.Join(filepath.Dir(p.secretsPath), secretReloadSnapshotFileName)
	for index, values := range []map[string]string{{"DATABASE_URL": "new"}, {}} {
		revision := strings.Repeat(string(rune('b'+index)), 64)
		if err := s.publishProjection(values, revision); err != nil {
			t.Fatal(err)
		}
		got := readRuntimeSecretSnapshot(t, snapshotPath)
		if got.Revision != revision || !reflect.DeepEqual(got.Secrets, values) || !reflect.DeepEqual(s.snapshot(), values) {
			t.Fatal("snapshot and restart state did not commit together")
		}
		body, err := os.ReadFile(p.secretsPath)
		var legacy map[string]string
		if err != nil || json.Unmarshal(body, &legacy) != nil || !reflect.DeepEqual(legacy, values) {
			t.Fatal("legacy projection diverged")
		}
		for _, path := range []string{p.secretsPath, p.revisionPath, snapshotPath} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0400 || int(info.Sys().(*syscall.Stat_t).Uid) != p.uid {
				t.Fatalf("projection owner or mode changed: %v %v", info, err)
			}
		}
	}
	revision := strings.Repeat("d", 64)
	if err := s.publishRevision(revision); err != nil {
		t.Fatal(err)
	}
	got := readRuntimeSecretSnapshot(t, snapshotPath)
	if got.Revision != revision || len(got.Secrets) != 0 || len(s.snapshot()) != 0 {
		t.Fatal("unchanged-value revision resurrected revoked secrets")
	}
	body, err := os.ReadFile(p.revisionPath)
	if err != nil || string(body) != revision {
		t.Fatal("legacy revision diverged")
	}
	entries, err := os.ReadDir(filepath.Dir(p.secretsPath))
	if err != nil {
		t.Fatal(err)
	}
	generations := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), runtimeSecretGenerationPrefix) {
			generations++
		}
	}
	if generations != 1 {
		t.Fatalf("obsolete secret generations retained: %d", generations)
	}
}

func TestRuntimeSecretSnapshotStagingFailurePreservesPublishedFiles(t *testing.T) {
	for _, failAfter := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprint(failAfter), func(t *testing.T) {
			_, p := runtimeSecretSnapshotFixture(t)
			dir := filepath.Dir(p.secretsPath)
			before := map[string]string{}
			for _, path := range []string{p.secretsPath, p.revisionPath, filepath.Join(dir, secretReloadSnapshotFileName)} {
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				before[path] = string(body)
			}
			failure := errors.New("injected staging failure")
			err := p.publishWithWriter(runtimeSecretSnapshot{Revision: strings.Repeat("b", 64), Secrets: map[string]string{"DATABASE_URL": "new"}}, func(staged string, snapshot runtimeSecretSnapshot) error {
				paths := []string{filepath.Base(p.secretsPath), filepath.Base(p.revisionPath), secretReloadSnapshotFileName}
				for index := 0; index < failAfter; index++ {
					if err := writePrivateRuntimeSecretFile(filepath.Join(staged, paths[index]), p.uid, p.dirUID, p.dirGID, []byte("partially staged")); err != nil {
						return err
					}
				}
				return failure
			})
			if !errors.Is(err, failure) {
				t.Fatalf("staging error lost: %v", err)
			}
			for path, want := range before {
				body, err := os.ReadFile(path)
				if err != nil || string(body) != want {
					t.Fatal("failed staging exposed a partial publication")
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 5 {
				t.Fatalf("failed generation was not removed: %d %v", len(entries), err)
			}
		})
	}
}

func TestRuntimeSecretSnapshotFailedPublicationKeepsRestartState(t *testing.T) {
	s, p := runtimeSecretSnapshotFixture(t)
	if err := s.publishProjection(map[string]string{"DATABASE_URL": "new"}, "invalid"); err == nil {
		t.Fatal("invalid revision committed")
	}
	got := readRuntimeSecretSnapshot(t, filepath.Join(filepath.Dir(p.secretsPath), secretReloadSnapshotFileName))
	if got.Revision != strings.Repeat("a", 64) || !reflect.DeepEqual(got.Secrets, s.snapshot()) || got.Secrets["DATABASE_URL"] != "old" {
		t.Fatal("failed publication changed disk or restart state")
	}
}

func TestRuntimeSecretSnapshotConcurrentPublication(t *testing.T) {
	s, p := runtimeSecretSnapshotFixture(t)
	path := filepath.Join(filepath.Dir(p.secretsPath), secretReloadSnapshotFileName)
	initial := strings.Repeat("0", 64)
	if err := s.publishProjection(map[string]string{"DATABASE_URL": initial}, initial); err != nil {
		t.Fatal(err)
	}
	// An open old generation remains a consistent snapshot after replacement.
	old, err := os.Open(path) //nolint:forbidigo // Test-owned projection; retain a descriptor across atomic replacement.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Close() }()
	var wg sync.WaitGroup
	var reads atomic.Int64
	start := make(chan struct{})
	errorsCh := make(chan error, 4)
	for reader := 0; reader < 3; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for index := 0; index < 200; index++ {
				body, err := os.ReadFile(path)
				if os.IsNotExist(err) {
					continue // Retry a lookup that raced old-generation cleanup.
				}
				var got runtimeSecretSnapshot
				if err != nil || json.Unmarshal(body, &got) != nil || !validGuestRuntimeSecretRevision(got.Revision) || (len(got.Secrets) != 0 && got.Secrets["DATABASE_URL"] != got.Revision) {
					errorsCh <- errors.New("reader observed inconsistent snapshot")
					return
				}
				reads.Add(1)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for index := 1; index <= 48; index++ {
			revision := fmt.Sprintf("%064x", index)
			values := map[string]string{}
			if index%2 != 0 {
				values["DATABASE_URL"] = revision
			}
			if err := s.publishProjection(values, revision); err != nil {
				errorsCh <- err
				return
			}
		}
	}()
	close(start)
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Error(err)
	}
	if reads.Load() == 0 {
		t.Fatal("no concurrent snapshots read")
	}
	body, err := io.ReadAll(old)
	var got runtimeSecretSnapshot
	if err != nil || json.Unmarshal(body, &got) != nil || got.Revision != initial || got.Secrets["DATABASE_URL"] != initial {
		t.Fatal("open snapshot changed after replacement")
	}
}
