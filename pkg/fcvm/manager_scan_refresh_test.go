package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
)

// H10-103 follow-up: vmmd reads a base's scan sidecar through its node cache.
// On production-us fsn-3 kept a cached refusal for runner-python312 after
// fsn-2 published a complete scan, so cold boots there would have been
// refused until fsn-3's imaged re-staged the base. A refusal is re-checked
// once against the canonical copy; a canonical refusal still refuses.
func TestBringUpScanCheck_RechecksCachedRefusalAgainstCanonicalCopy(t *testing.T) {
	const (
		baseKey = "base/runner-python312-amd64.ext4"
		scanKey = "scans/runner-python312-amd64.ext4.scan.json"
	)
	refusal, _ := json.Marshal(map[string]any{"image": "ghcr.io/example/python312@sha256:a", "findings": map[string]int{"CRITICAL": 9999}, "fix_available_findings": map[string]int{"CRITICAL": 9999}})
	complete, _ := json.Marshal(map[string]any{"image": "ghcr.io/example/python312@sha256:a", "findings": map[string]int{"CRITICAL": 0}, "fix_available_findings": map[string]int{"CRITICAL": 0}})
	for _, tc := range []struct {
		name      string
		canonical []byte
		wantOK    bool
	}{
		{name: "canonical complete", canonical: complete, wantOK: true},
		{name: "canonical refusal", canonical: refusal, wantOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			parent := &memStorage{}
			if err := parent.Put(ctx, scanKey, strings.NewReader(string(refusal))); err != nil {
				t.Fatal(err)
			}
			cache, err := storage.NewLocalCacheBackend(parent, t.TempDir(), 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			rc, err := cache.Get(ctx, scanKey) // this node caches the refusal
			if err != nil {
				t.Fatal(err)
			}
			_ = rc.Close()
			if err := parent.Put(ctx, scanKey, strings.NewReader(string(tc.canonical))); err != nil {
				t.Fatal(err)
			}
			m := &Manager{}
			m.WithStorage(cache)
			err = m.bringUpScanCheck(ctx, baseKey)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("stale cached refusal blocked the boot: %v", err)
				}
				return
			}
			var prob *api.Problem
			if !errors.As(err, &prob) || prob.Code != api.CodeScanCritical {
				t.Fatalf("canonical refusal must still refuse, got %v", err)
			}
		})
	}
}
