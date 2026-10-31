package main

// adr: 20261024085817

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

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

const (
	genOld = "registry.gregale.dev/base-minimal@sha256:db529ed00000000000000000000000000000000000000000000000000000000a"
	genNew = "registry.gregale.dev/base-minimal@sha256:5ca69ea00000000000000000000000000000000000000000000000000000000b"
)

func TestJudgeBaseGenerations(t *testing.T) {
	const key = "base/base-amd64.ext4"
	cases := []struct {
		name               string
		serving, candidate string
		cached             string
		marked, routed     bool
		wantCache          string
		wantChanged        bool
		wantIntact         bool
		wantExit           int
	}{
		{name: "unchanged release on its own cache", serving: genOld, candidate: genOld, cached: genOld, marked: true, routed: true,
			wantCache: baseCacheCurrent, wantIntact: true, wantExit: baseGenerationExitIntact},
		{name: "new generation not yet pre-staged here", serving: genOld, candidate: genNew, cached: genOld, marked: true, routed: true,
			wantCache: baseCacheCurrent, wantChanged: true, wantIntact: true, wantExit: baseGenerationExitChanged},
		// rc.290 on fsn-3: the candidate's pre-stage adopted 5ca69ea into the
		// cache while rc.288's vmmd still expected db529ed.
		{name: "pre-stage already adopted the new generation", serving: genOld, candidate: genNew, cached: genNew, marked: true, routed: true,
			wantCache: baseCacheOtherGeneration, wantChanged: true, wantExit: baseGenerationExitAbandoned},
		{name: "cache holds a generation neither release boots", serving: genOld, candidate: genOld, cached: genNew, marked: true, routed: true,
			wantCache: baseCacheOtherGeneration, wantExit: baseGenerationExitAbandoned},
		{name: "base not cached here", serving: genOld, candidate: genNew, routed: true,
			wantCache: baseCacheNotCached, wantChanged: true, wantIntact: true, wantExit: baseGenerationExitChanged},
		{name: "uncached single-box storage is not gated", serving: genOld, candidate: genNew,
			wantCache: baseCacheUncachedStorage, wantChanged: true, wantIntact: true, wantExit: baseGenerationExitChanged},
		{name: "serving vmmd gates no generation for the key", serving: "", candidate: genNew, cached: genNew, marked: true, routed: true,
			wantCache: baseCacheOtherGeneration, wantChanged: true, wantIntact: true, wantExit: baseGenerationExitChanged},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, err := judgeBaseGenerations(map[string]string{key: tc.serving}, map[string]string{key: tc.candidate},
				func(string) (string, bool, bool, error) { return tc.cached, tc.marked, tc.routed, nil })
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Bases) != 1 || report.Bases[0].Cache != tc.wantCache {
				t.Fatalf("bases = %+v, want one with cache %q", report.Bases, tc.wantCache)
			}
			if report.Changed != tc.wantChanged || report.ServingIntact != tc.wantIntact || report.exitCode() != tc.wantExit {
				t.Fatalf("report changed=%v intact=%v exit=%d; want %v %v %d", report.Changed, report.ServingIntact, report.exitCode(), tc.wantChanged, tc.wantIntact, tc.wantExit)
			}
		})
	}
	if _, err := judgeBaseGenerations(map[string]string{key: genOld}, map[string]string{key: genOld},
		func(string) (string, bool, bool, error) { return "", false, false, errors.New("disk") }); err == nil {
		t.Fatal("a cache read error must fail the check, not pass it")
	}
}

// The command reads the serving vmmd's environment and the candidate's
// runtime-bases.env, and the node cache the serving storage configuration
// names.
func TestBaseGenerationCheckCommand(t *testing.T) {
	baseKey := storagekey.BaseForArch("", goruntime.GOARCH)
	cases := []struct {
		name      string
		candidate string
		marker    string
		wantExit  int
	}{
		{name: "same generation", candidate: genOld, marker: genOld, wantExit: baseGenerationExitIntact},
		{name: "drain before pre-staging the new generation", candidate: genNew, marker: genOld, wantExit: baseGenerationExitChanged},
		{name: "never re-activate after the pre-stage adopted it", candidate: genNew, marker: genNew, wantExit: baseGenerationExitAbandoned},
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
			if err := cache.Put(t.Context(), baseKey, strings.NewReader("ext4")); err != nil {
				t.Fatal(err)
			}
			if err := cache.MarkGeneration(baseKey, tc.marker); err != nil {
				t.Fatal(err)
			}
			var gotEnv map[string]string
			previous := baseGenerationStorage
			baseGenerationStorage = func(_ context.Context, env map[string]string) (storage.StorageBackend, error) {
				gotEnv = env
				return cache, nil
			}
			t.Cleanup(func() { baseGenerationStorage = previous })

			environ := filepath.Join(dir, "environ")
			serving := "FAAS_STORAGE_BACKEND=gcs\x00FAAS_DEPLOY_BASE_REF_MINIMAL=" + genOld + "\x00"
			if err := os.WriteFile(environ, []byte(serving), 0o600); err != nil {
				t.Fatal(err)
			}
			env := filepath.Join(dir, "runtime-bases.env")
			if err := os.WriteFile(env, []byte("# release-bound\nFAAS_DEPLOY_BASE_REF_MINIMAL="+tc.candidate+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			code := baseGenerationCheckCommand([]string{"--serving-environ", environ, "--candidate-env-file", env}, &stdout, &stderr)
			if code != tc.wantExit {
				t.Fatalf("exit %d, want %d; stdout %s stderr %s", code, tc.wantExit, stdout.String(), stderr.String())
			}
			if gotEnv["FAAS_STORAGE_BACKEND"] != "gcs" {
				t.Fatalf("storage opened with %v, want the serving environment", gotEnv)
			}
			var report baseGenerationReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatalf("stdout is not one JSON report: %v\n%s", err, stdout.String())
			}
			for _, base := range report.Bases {
				if base.Key == baseKey && (base.Serving != genOld || base.Candidate != tc.candidate || base.Cached != tc.marker) {
					t.Fatalf("minimal base entry %+v", base)
				}
			}
		})
	}
}

func TestBaseGenerationCheckFailsWithoutTheServingEnvironment(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := baseGenerationCheckCommand([]string{"--serving-environ", filepath.Join(t.TempDir(), "missing")}, &stdout, &stderr); code != adoptionExitError {
		t.Fatalf("exit %d, want %d", code, adoptionExitError)
	}
	if code := baseGenerationCheckCommand(nil, &stdout, &stderr); code != adoptionExitError {
		t.Fatalf("no serving pid: exit %d, want %d", code, adoptionExitError)
	}
}

func TestParseEnvFileReadsRuntimeBasesEnv(t *testing.T) {
	got := parseEnvFile([]byte("# comment\n\nFAAS_DEPLOY_BASE_REF_MINIMAL=\"" + genOld + "\"\n ; also a comment\nNOEQUALS\nFAAS_NODE_NAME = fsn-3\n"))
	if got["FAAS_DEPLOY_BASE_REF_MINIMAL"] != genOld || got["FAAS_NODE_NAME"] != "fsn-3" || len(got) != 2 {
		t.Fatalf("parseEnvFile = %v", got)
	}
	environ := parseEnviron([]byte("A=1\x00B=x=y\x00\x00=bad\x00"))
	if environ["A"] != "1" || environ["B"] != "x=y" || len(environ) != 2 {
		t.Fatalf("parseEnviron = %v", environ)
	}
}
