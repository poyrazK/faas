package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
)

type faultBackend struct {
	dropFails   atomic.Bool
	sampleFails atomic.Bool
	drops       atomic.Int32
}

func (b *faultBackend) Identity() string { return strings.Repeat("f", 64) }
func (b *faultBackend) Provision(context.Context, rehearsalpg.Resource, string, string) ([]rehearsalpg.Snapshot, error) {
	return []rehearsalpg.Snapshot{{Table: "public.imports", Rows: []json.RawMessage{}}}, nil
}
func (b *faultBackend) FenceAndCapture(context.Context, rehearsalpg.Resource) ([]rehearsalpg.Snapshot, error) {
	return []rehearsalpg.Snapshot{{Table: "public.imports", Rows: []json.RawMessage{}}}, nil
}
func (b *faultBackend) LockWait(context.Context, rehearsalpg.Resource) (int64, error) {
	if b.sampleFails.Load() {
		return 0, ErrFixture
	}
	return 0, nil
}
func (b *faultBackend) Drop(context.Context, rehearsalpg.Resource) error {
	b.drops.Add(1)
	if b.dropFails.Load() {
		return ErrFixture
	}
	return nil
}

func TestPostgresCollectorFailureAndCleanupFailureCannotPass(t *testing.T) {
	for _, failure := range []string{"cleanup", "collector"} {
		t.Run(failure, func(t *testing.T) {
			b := &faultBackend{}
			catalog := &memoryCatalog{rows: map[string]rehearsalpg.Resource{}}
			s := NewService(catalog, b)
			account, run, app := uuid.NewString(), strings.Repeat("a", 32), uuid.NewString()
			fixture := "fixture"
			spec := rehearsalpg.Spec{Version: 1, FixtureSHA256: rehearsalpg.HashBytes([]byte(fixture)), Tables: []rehearsalpg.Table{{Schema: "public", Name: "imports", Columns: []string{"item"}}}, MaxLockWaitMS: 50}
			if _, _, e := s.Create(t.Context(), account, run, app, "primary.svc.gregale", rehearsalpg.CreateRequest{Spec: spec, SQL: fixture}); e != nil {
				t.Fatal(e)
			}
			if failure == "cleanup" {
				b.dropFails.Store(true)
			} else {
				b.sampleFails.Store(true)
				resource, _ := catalog.GetScenarioPostgres(t.Context(), account, run)
				s.mu.Lock()
				w := s.sessions[resource.ID]
				s.mu.Unlock()
				select {
				case <-w.done:
				case <-time.After(time.Second):
					t.Fatal("collector did not report failure")
				}
			}
			evidence, e := s.Seal(t.Context(), account, run, uuid.NewString())
			if e == nil || evidence.Status == "complete" || b.drops.Load() == 0 {
				t.Fatalf("failed collection passed: %+v %v", evidence, e)
			}
			b.dropFails.Store(false)
			if e := s.Delete(t.Context(), account, run); e != nil {
				t.Fatal(e)
			}
		})
	}
}
