package sched_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type templateCapturer struct {
	root              string
	backend           storage.StorageBackend
	mu                sync.Mutex
	captured, retired []string
	partial           bool
}

func (c *templateCapturer) CaptureExecutionTemplate(ctx context.Context, spec sched.ExecutionTemplateSpec) (sched.ExecutionTemplateCapture, error) {
	key := state.SnapshotCaptureMemKey(uuid.Nil.String(), "", uuid.NewString())
	snap := state.Snapshot{StorageKey: key}
	path := filepath.Join(c.root, key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return sched.ExecutionTemplateCapture{}, err
	}
	file, err := os.Create(path)
	if err != nil {
		return sched.ExecutionTemplateCapture{}, err
	}
	if err := file.Truncate(int64(spec.Identity.MemoryMB) << 20); err != nil {
		_ = file.Close()
		return sched.ExecutionTemplateCapture{}, err
	}
	if err := file.Close(); err != nil {
		return sched.ExecutionTemplateCapture{}, err
	}
	backing, _ := json.Marshal(map[string]any{"version": 2, "kernel": "sha256:" + spec.Identity.KernelDigest, "base": "sha256:" + spec.Identity.BaseImageDigest, "timer": "noapic"})
	objects := map[string][]byte{state.SnapshotVMStateKey(snap): []byte("state"), state.SnapshotBackingKey(snap): backing}
	if !c.partial {
		objects[state.SnapshotDriveKey(snap)] = []byte("private-drive")
	}
	for key, body := range objects {
		if err := c.backend.Put(ctx, key, strings.NewReader(string(body))); err != nil {
			return sched.ExecutionTemplateCapture{}, err
		}
	}
	c.mu.Lock()
	c.captured = append(c.captured, key)
	c.mu.Unlock()
	return sched.ExecutionTemplateCapture{NodeID: spec.NodeID, Snapshot: sched.SnapshotRef{StorageKey: key, VMStateStorageKey: state.SnapshotVMStateKey(snap), FCVersion: spec.Artifacts.FCVersion, MemBytes: int64(spec.Identity.MemoryMB) << 20, Networkless: true}, VMStateBytes: 5}, nil
}

func (c *templateCapturer) RetireExecutionTemplate(ctx context.Context, ref sched.ExecutionTemplateCapture) error {
	c.mu.Lock()
	c.retired = append(c.retired, ref.Snapshot.StorageKey)
	c.mu.Unlock()
	snap := state.Snapshot{StorageKey: ref.Snapshot.StorageKey}
	for _, key := range []string{snap.StorageKey, state.SnapshotVMStateKey(snap), state.SnapshotDriveKey(snap), state.SnapshotBackingKey(snap)} {
		if err := c.backend.Delete(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func templatePublicationFixture(t *testing.T) (*state.MemStore, *templateCapturer, sched.ExecutionTemplateSpec) {
	t.Helper()
	root := t.TempDir()
	backend, err := storage.NewLocalStorageBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	i := sched.RuntimeSnapshotIdentity{FCVersion: "1.7.0", Runtime: api.ExecutionRuntimeNode24, Architecture: "amd64", KernelDigest: strings.Repeat("a", 64), GuestExecutorDigest: strings.Repeat("b", 64), BaseImageDigest: strings.Repeat("c", 64), MemoryMB: 128, EphemeralDiskMB: 64, FormatVersion: sched.CurrentRuntimeSnapshotFormatVersion}
	a := sched.ExecutionRuntimeArtifacts{Architecture: i.Architecture, KernelDigest: i.KernelDigest, GuestExecutorDigest: i.GuestExecutorDigest, BaseImageDigest: i.BaseImageDigest, KernelKey: "kernel/fc", BaseKey: "base/runner-node24.ext4", LayerKey: "layers/run-node24.ext4", FCVersion: "1.7.0"}
	return state.NewMemStore(), &templateCapturer{root: root, backend: backend}, sched.ExecutionTemplateSpec{AccountID: uuid.NewString(), NodeID: "node-a", Identity: i, Artifacts: a}
}

func TestExecutionTemplatePublicationIsCompleteAndIdempotent(t *testing.T) {
	store, capture, spec := templatePublicationFixture(t)
	p := sched.NewExecutionTemplatePublisher(store, capture.backend, capture)
	entry, err := p.Ensure(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.Ensure(t.Context(), spec)
	if err != nil || again.StorageKey != entry.StorageKey || len(capture.captured) != 1 || len(capture.retired) != 0 {
		t.Fatalf("retry recaptured or retired a ready bundle: %v", err)
	}
	if err := store.RetireRuntimeSnapshot(t.Context(), mustIdentityKey(spec.Identity), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Ensure(t.Context(), spec); !errors.Is(err, sched.ErrRuntimeSnapshotConflict) {
		t.Fatalf("retirement overwritten: %v", err)
	}
	rows, err := store.RuntimeSnapshotRecords(t.Context())
	if err != nil || len(rows) != 1 || rows[0].State != state.RuntimeSnapshotStateRetired {
		t.Fatal("retention lost the retired catalog row")
	}
}

func TestRuntimeSnapshotBundleAuthenticatesEveryObject(t *testing.T) {
	store, capture, spec := templatePublicationFixture(t)
	entry, err := sched.NewExecutionTemplatePublisher(store, capture.backend, capture).Ensure(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	snap := state.Snapshot{StorageKey: entry.StorageKey}
	for name, key := range map[string]string{"mem": snap.StorageKey, "vmstate": state.SnapshotVMStateKey(snap), "drive": state.SnapshotDriveKey(snap), "backing": state.SnapshotBackingKey(snap)} {
		t.Run(name, func(t *testing.T) {
			file, err := os.OpenFile(filepath.Join(capture.root, key), os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			var original [1]byte
			if _, err := file.ReadAt(original[:], 0); err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteAt([]byte{original[0] ^ 1}, 0); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := file.WriteAt(original[:], 0); err != nil {
					t.Error(err)
				}
			}()
			if err := sched.NewStorageRuntimeSnapshotVerifier(capture.backend).VerifyRuntimeSnapshot(t.Context(), entry); !errors.Is(err, sched.ErrRuntimeSnapshotCorrupt) {
				t.Fatalf("tampered %s accepted: %v", name, err)
			}
		})
	}
}

func TestExecutionTemplatePartialCaptureCannotPublish(t *testing.T) {
	store, capture, spec := templatePublicationFixture(t)
	capture.partial = true
	_, err := sched.NewExecutionTemplatePublisher(store, capture.backend, capture).Ensure(t.Context(), spec)
	if !errors.Is(err, sched.ErrRuntimeSnapshotCorrupt) || len(capture.retired) != 1 {
		t.Fatalf("partial capture was accepted or not retired: %v", err)
	}
	if _, err := store.LookupRuntimeSnapshot(t.Context(), mustIdentityKey(spec.Identity)); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("partial capture published a ready row")
	}
}

// Keep the store named: embedding a full state store generates thousands of
// compiler wrappers and is prohibited by the repository's teststorecheck.
type templateAckStore struct {
	mem       *state.MemStore
	mu        sync.Mutex
	published bool
	unknown   bool
	barrier   chan struct{}
	arrived   int
}

var templateAckError = errors.New("injected lost commit acknowledgement")

func (s *templateAckStore) LookupRuntimeSnapshot(ctx context.Context, key string) (state.RuntimeSnapshotRecord, error) {
	s.mu.Lock()
	blocked := s.unknown && s.published
	s.mu.Unlock()
	if blocked {
		return state.RuntimeSnapshotRecord{}, templateAckError
	}
	return s.mem.LookupRuntimeSnapshot(ctx, key)
}
func (s *templateAckStore) RetireRuntimeSnapshot(ctx context.Context, key string, at time.Time) error {
	return s.mem.RetireRuntimeSnapshot(ctx, key, at)
}
func (s *templateAckStore) PublishRuntimeSnapshot(ctx context.Context, row state.RuntimeSnapshotRecord) (state.RuntimeSnapshotRecord, error) {
	s.mu.Lock()
	s.published = true
	s.arrived++
	if s.barrier != nil && s.arrived == 2 {
		close(s.barrier)
	}
	barrier, unknown := s.barrier, s.unknown
	s.mu.Unlock()
	if barrier != nil {
		select {
		case <-barrier:
		case <-ctx.Done():
			return state.RuntimeSnapshotRecord{}, ctx.Err()
		}
		return s.mem.PublishRuntimeSnapshot(ctx, row)
	}
	if !unknown {
		if _, err := s.mem.PublishRuntimeSnapshot(ctx, row); err != nil {
			return state.RuntimeSnapshotRecord{}, err
		}
	}
	return state.RuntimeSnapshotRecord{}, templateAckError
}

func TestExecutionTemplateLostAcknowledgementKeepsTheWinner(t *testing.T) {
	mem, capture, spec := templatePublicationFixture(t)
	store := &templateAckStore{mem: mem}
	entry, err := sched.NewExecutionTemplatePublisher(store, capture.backend, capture).Ensure(t.Context(), spec)
	if err != nil || entry.StorageKey == "" || len(capture.retired) != 0 {
		t.Fatalf("committed capture lost after acknowledgement failure: %v", err)
	}
}

func TestExecutionTemplateUnknownCommitRetainsArtifacts(t *testing.T) {
	mem, capture, spec := templatePublicationFixture(t)
	store := &templateAckStore{mem: mem, unknown: true}
	_, err := sched.NewExecutionTemplatePublisher(store, capture.backend, capture).Ensure(t.Context(), spec)
	if !errors.Is(err, templateAckError) || len(capture.retired) != 0 || len(capture.captured) != 1 {
		t.Fatal("uncertain commit lost its artifacts")
	}
	if _, err := os.Stat(filepath.Join(capture.root, capture.captured[0])); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionTemplatePublicationRaceRetiresOnlyItsLoser(t *testing.T) {
	mem, capture, spec := templatePublicationFixture(t)
	store := &templateAckStore{mem: mem, barrier: make(chan struct{})}
	p := sched.NewExecutionTemplatePublisher(store, capture.backend, capture)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	type outcome struct {
		entry sched.RuntimeSnapshot
		err   error
	}
	results := make(chan outcome, 2)
	for range 2 {
		go func() { entry, err := p.Ensure(ctx, spec); results <- outcome{entry, err} }()
	}
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || a.entry.StorageKey != b.entry.StorageKey || len(capture.captured) != 2 || len(capture.retired) != 1 || capture.retired[0] == a.entry.StorageKey {
		t.Fatalf("publication race did not preserve its sole winner: %v %v", a.err, b.err)
	}
	if err := sched.NewStorageRuntimeSnapshotVerifier(capture.backend).VerifyRuntimeSnapshot(ctx, a.entry); err != nil {
		t.Fatal(err)
	}
}
