package main

// adr: 20261022184531

import (
	"errors"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestJudgeBaseGenerationReadiness(t *testing.T) {
	configured := map[string]string{"base/a.ext4": genNew, "base/b.ext4": genNew}
	for _, tc := range []struct {
		name          string
		verdict       string
		err           error
		wantAdoptable bool
	}{
		{name: "cached", verdict: imaged.BaseAdoptionCached, wantAdoptable: true},
		{name: "published", verdict: imaged.BaseAdoptionPublished, wantAdoptable: true},
		{name: "unpublished", verdict: imaged.BaseAdoptionUnpublished, wantAdoptable: true},
		{name: "stale", verdict: imaged.BaseAdoptionStale},
		{name: "storage unreachable is no evidence", err: errors.New("gcs: 503"), wantAdoptable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := judgeBaseGenerationReadiness(configured, func(key, generation string) (imaged.BaseAdoption, error) {
				if key == "base/a.ext4" {
					return imaged.BaseAdoption{Key: key, Generation: generation, Verdict: imaged.BaseAdoptionCached}, nil
				}
				return imaged.BaseAdoption{Key: key, Generation: generation, Verdict: tc.verdict, Published: genOld}, tc.err
			})
			if len(report) != 2 || report[0].BaseKey != "base/a.ext4" || !report[0].Adoptable || report[1].BaseKey != "base/b.ext4" {
				t.Fatalf("report = %+v, want both bases in key order", report)
			}
			if report[1].Adoptable != tc.wantAdoptable || report[1].Generation != genNew || (report[1].Reason == "") != tc.wantAdoptable {
				t.Fatalf("base b = %+v, want adoptable=%t", report[1], tc.wantAdoptable)
			}
		})
	}
}

// production-us 2026-10-22: fsn-3, rolled back, expected the generation fsn-2's
// newer imaged had replaced in shared storage. Its Ping must say so, and the
// check must not move the node's cache.
func TestBaseGenerationReadinessCheck_RolledBackNode(t *testing.T) {
	baseKey := storagekey.BaseForArch("", goruntime.GOARCH)
	dir := t.TempDir()
	parent, err := storage.NewLocalStorageBackend(filepath.Join(dir, "shared"))
	if err != nil {
		t.Fatal(err)
	}
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(dir, "cache"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	for key, body := range map[string]string{
		baseKey + ".digest":             "sha256:config",
		wire.ScanKeyForBaseKey(baseKey): `{"image":"` + genNew + `","findings":{"CRITICAL":0}}`,
	} {
		if err := parent.Put(t.Context(), key, strings.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	report := baseGenerationReadinessCheck(cache, map[string]string{baseKey: genOld})(t.Context())
	if len(report) != 1 || report[0].Adoptable || !strings.Contains(report[0].Reason, genNew) {
		t.Fatalf("report = %+v, want the configured generation not adoptable, naming the published one", report)
	}
	if _, cached, err := cache.LocalPath(wire.ScanKeyForBaseKey(baseKey)); err != nil || cached {
		t.Fatalf("the check cached the shared scan sidecar (cached=%t, %v)", cached, err)
	}
}
