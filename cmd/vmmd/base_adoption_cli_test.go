package main

// adr: 20261024191249

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestJudgeBaseAdoptions(t *testing.T) {
	configured := map[string]string{"base/b.ext4": genNew, "base/a.ext4": genNew}
	cases := []struct {
		name     string
		verdicts map[string]string
		judgeErr error
		wantExit int
		wantErr  bool
	}{
		{name: "every base adoptable", verdicts: map[string]string{"base/a.ext4": imaged.BaseAdoptionCached, "base/b.ext4": imaged.BaseAdoptionPublished},
			wantExit: baseAdoptionExitAdoptable},
		{name: "an unpublished base needs nothing yet", verdicts: map[string]string{"base/a.ext4": imaged.BaseAdoptionUnpublished, "base/b.ext4": imaged.BaseAdoptionPublished},
			wantExit: baseAdoptionExitAdoptable},
		{name: "one stale base keeps the node out of service", verdicts: map[string]string{"base/a.ext4": imaged.BaseAdoptionCached, "base/b.ext4": imaged.BaseAdoptionStale},
			wantExit: baseAdoptionExitStale},
		{name: "a judge failure fails the check", judgeErr: errors.New("registry unavailable"), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, err := judgeBaseAdoptions(configured, func(key, generation string) (imaged.BaseAdoption, error) {
				if tc.judgeErr != nil {
					return imaged.BaseAdoption{}, tc.judgeErr
				}
				return imaged.BaseAdoption{Key: key, Generation: generation, Verdict: tc.verdicts[key]}, nil
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if report.exitCode() != tc.wantExit {
				t.Fatalf("exit %d, want %d: %+v", report.exitCode(), tc.wantExit, report)
			}
			if len(report.Bases) != 2 || report.Bases[0].Key != "base/a.ext4" {
				t.Fatalf("bases = %+v, want both in key order", report.Bases)
			}
		})
	}
}

// The command reads the serving vmmd's environment and judges each
// configured base against this node's cache and the shared store that
// environment names.
func TestBaseAdoptionCheckCommand(t *testing.T) {
	baseKey := storagekey.BaseForArch("", goruntime.GOARCH)
	cases := []struct {
		name      string
		marker    string // this node's cache marker for the minimal base
		published string // what the shared scan sidecar names; "" = never published
		wantExit  int
		wantVerd  string
	}{
		// production-us 2026-10-24, rc.293 on fsn-2.
		{name: "rc.293: nobody published the configured generation", marker: genOld, published: genOld,
			wantExit: baseAdoptionExitStale, wantVerd: imaged.BaseAdoptionStale},
		{name: "this node's pre-stage committed it", marker: genNew, published: genOld,
			wantExit: baseAdoptionExitAdoptable, wantVerd: imaged.BaseAdoptionCached},
		{name: "another node published it", marker: genOld, published: genNew,
			wantExit: baseAdoptionExitAdoptable, wantVerd: imaged.BaseAdoptionPublished},
		{name: "never published by anyone",
			wantExit: baseAdoptionExitAdoptable, wantVerd: imaged.BaseAdoptionUnpublished},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			parent, err := storage.NewLocalStorageBackend(filepath.Join(dir, "shared"))
			if err != nil {
				t.Fatal(err)
			}
			cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(dir, "cache"), 1<<30)
			if err != nil {
				t.Fatal(err)
			}
			if tc.marker != "" {
				if err := cache.Put(t.Context(), baseKey, strings.NewReader("ext4")); err != nil {
					t.Fatal(err)
				}
				if err := cache.MarkGeneration(baseKey, tc.marker); err != nil {
					t.Fatal(err)
				}
			}
			if tc.published != "" {
				for key, body := range map[string]string{
					baseKey + ".digest":             "sha256:config",
					wire.ScanKeyForBaseKey(baseKey): `{"image":"` + tc.published + `","findings":{"CRITICAL":0}}`,
				} {
					if err := parent.Put(t.Context(), key, strings.NewReader(body)); err != nil {
						t.Fatal(err)
					}
				}
			}
			var gotEnv map[string]string
			previous := baseGenerationStorage
			baseGenerationStorage = func(_ context.Context, env map[string]string) (storage.StorageBackend, error) {
				gotEnv = env
				return cache, nil
			}
			t.Cleanup(func() { baseGenerationStorage = previous })

			environ := filepath.Join(dir, "environ")
			serving := "FAAS_STORAGE_BACKEND=oci\x00FAAS_DEPLOY_BASE_REF_MINIMAL=" + genNew + "\x00"
			if err := os.WriteFile(environ, []byte(serving), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			code := baseAdoptionCheckCommand([]string{"--serving-environ", environ}, &stdout, &stderr)
			if code != tc.wantExit {
				t.Fatalf("exit %d, want %d; stdout %s stderr %s", code, tc.wantExit, stdout.String(), stderr.String())
			}
			if gotEnv["FAAS_STORAGE_BACKEND"] != "oci" {
				t.Fatalf("storage opened with %v, want the serving environment", gotEnv)
			}
			var report baseAdoptionReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatalf("stdout is not one JSON report: %v\n%s", err, stdout.String())
			}
			if len(report.Bases) != len(imaged.GatedRuntimeBases()) {
				t.Fatalf("report covers %d bases, want every gated base: %+v", len(report.Bases), report.Bases)
			}
			for _, base := range report.Bases {
				if base.Key == baseKey && (base.Generation != genNew || base.Verdict != tc.wantVerd) {
					t.Fatalf("minimal base %+v, want generation %s verdict %s", base, genNew, tc.wantVerd)
				}
			}
		})
	}
}

func TestBaseAdoptionCheckFailsWithoutTheServingEnvironment(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := baseAdoptionCheckCommand(nil, &stdout, &stderr); code != adoptionExitError {
		t.Fatalf("exit %d, want %d", code, adoptionExitError)
	}
	if code, ok := runAdoptionCommand([]string{"base-adoption-check", "--serving-environ", filepath.Join(t.TempDir(), "missing")}, &stdout, &stderr); !ok || code != adoptionExitError {
		t.Fatalf("dispatch = %d, %v; want the subcommand to run and fail", code, ok)
	}
}
