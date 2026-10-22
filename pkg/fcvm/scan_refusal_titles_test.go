// adr: 20261022160000
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// schedd re-places a wake on another node when the boot gate refuses it for
// a node-local reason, and tells those refusals apart by title. Pin the gate
// to the shared titles so a reworded refusal cannot silently turn off
// failover, and so an artifact-wide refusal never takes a node-local title.
func TestBringUpScanCheck_RefusalTitlesSchedulerFailsOverOn(t *testing.T) {
	const (
		baseKey = "base/runner-node22-amd64.ext4"
		oldRef  = "ghcr.io/example/node22@sha256:old"
		newRef  = "ghcr.io/example/node22@sha256:new"
	)
	scanKey := wire.ScanKeyForBaseKey(baseKey)
	scan := func(image string, fixable int) string {
		b, _ := json.Marshal(map[string]any{"image": image,
			"findings": map[string]int{"CRITICAL": fixable}, "fix_available_findings": map[string]int{"CRITICAL": fixable}})
		return string(b)
	}
	for _, tc := range []struct {
		name      string
		manager   func(t *testing.T) *Manager
		wantTitle string
		nodeLocal bool
	}{{
		// production-us 2026-10-22: a node rolled back to rc.288 expected the
		// generation a peer's newer imaged had already replaced.
		name: "configured generation no longer published",
		manager: func(t *testing.T) *Manager {
			parent := &memStorage{}
			if err := parent.Put(context.Background(), scanKey, strings.NewReader(scan(newRef, 0))); err != nil {
				t.Fatal(err)
			}
			cache, err := storage.NewLocalCacheBackend(parent, t.TempDir(), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			m := &Manager{}
			m.WithBaseGenerations(map[string]string{baseKey: oldRef})
			m.WithStorage(cache)
			return m
		},
		wantTitle: api.ScanCriticalTitleGenerationUnavailable,
		nodeLocal: true,
	}, {
		name: "sidecar names another generation",
		manager: func(*testing.T) *Manager {
			m := &Manager{}
			m.WithBaseGenerations(map[string]string{baseKey: newRef})
			m.WithStorage(&memStorage{blobs: map[string][]byte{scanKey: []byte(scan(oldRef, 0))}})
			return m
		},
		wantTitle: api.ScanCriticalTitleGenerationMismatch,
		nodeLocal: true,
	}, {
		name: "sidecar missing",
		manager: func(*testing.T) *Manager {
			m := &Manager{}
			m.WithStorage(&memStorage{})
			return m
		},
		wantTitle: api.ScanCriticalTitleSidecarMissing,
		nodeLocal: true,
	}, {
		name: "fixable critical finding refuses on every node",
		manager: func(*testing.T) *Manager {
			m := &Manager{}
			m.WithStorage(&memStorage{blobs: map[string][]byte{scanKey: []byte(scan(newRef, 2))}})
			return m
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.manager(t).bringUpScanCheck(context.Background(), baseKey)
			var p *api.Problem
			if !errors.As(err, &p) || p.Code != api.CodeScanCritical {
				t.Fatalf("bringUpScanCheck = %v, want a scan_critical Problem", err)
			}
			nodeLocal := false
			for _, title := range []string{api.ScanCriticalTitleGenerationUnavailable, api.ScanCriticalTitleGenerationMismatch, api.ScanCriticalTitleSidecarMissing} {
				nodeLocal = nodeLocal || p.Title == title
			}
			if nodeLocal != tc.nodeLocal || (tc.wantTitle != "" && p.Title != tc.wantTitle) {
				t.Fatalf("title = %q (node-local %t), want %q (node-local %t)", p.Title, nodeLocal, tc.wantTitle, tc.nodeLocal)
			}
		})
	}
}
