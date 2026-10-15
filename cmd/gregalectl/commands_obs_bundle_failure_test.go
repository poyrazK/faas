package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261015161000000: failed or incomplete HTTP reads never publish a bundle.
func TestCmdObsBundleRejectsInvalidInputBeforeHTTP(t *testing.T) {
	_, _ = obsBundleTestStreams(t)
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	base := []string{"--incident", obsBundleTestRef, "--output", "-"}
	for _, args := range [][]string{
		{}, {"--incident", obsBundleTestRef}, {"--output", "-"},
		{"--incident", "deployment/../../health", "--output", "-"},
		{"--incident", "bad/id", "--output", "-"},
		append(slices.Clone(base), "--limit", "0"), append(slices.Clone(base), "--limit", "101"),
		append(slices.Clone(base), "--history-limit", "0"), append(slices.Clone(base), "--history-limit", "101"),
		append(slices.Clone(base), "--timeout", "0"), append(slices.Clone(base), "--timeout", "61s"),
		append(slices.Clone(base), "--unknown"), append(slices.Clone(base), "extra"),
	} {
		if code := cmdObsBundle(args); code != 2 {
			t.Errorf("args=%v exit=%d", args, code)
		}
	}
	t.Setenv("FAAS_ADMIN_TOKEN", "")
	if code := cmdObsBundle(base); code != 2 {
		t.Fatalf("missing token exit=%d", code)
	}
}

func TestCmdObsBundleFailuresNeverPublishOrLeakResponses(t *testing.T) {
	for _, section := range []string{"evidence", "context", "health"} {
		for _, failure := range []string{"403", "404", "503", "invalid_json", "null", "empty", "oversized", "wrong_identity", "missing_timestamp"} {
			if section == "health" && failure == "wrong_identity" {
				continue
			}
			t.Run(section+"/"+failure, func(t *testing.T) {
				out, stderr := obsBundleTestStreams(t)
				b := obsBundleTestSnapshot(obsBundleTestRef, 25, 25)
				var calls atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if !strings.HasSuffix(r.URL.Path, "/"+section) {
						obsBundleTestSection(w, r, b)
						return
					}
					switch failure {
					case "403", "404", "503":
						code := map[string]int{"403": 403, "404": 404, "503": 503}[failure]
						http.Error(w, "private-secret", code)
					case "invalid_json":
						_, _ = io.WriteString(w, "{\"generated_at\":\"private-secret\"}")
					case "null":
						_, _ = io.WriteString(w, "null")
					case "empty":
						_, _ = io.WriteString(w, "{}")
					case "oversized":
						_, _ = io.WriteString(w, strings.Repeat(" ", api.ObsIncidentBundleResponseMaxBytes+1))
					case "wrong_identity":
						b.Evidence.Data.Incident.ID = "compute_node/22222222-2222-4222-8222-222222222222"
						b.Context.Data.IncidentID = b.Evidence.Data.Incident.ID
						obsBundleTestSection(w, r, b)
					case "missing_timestamp":
						b.Evidence.Data.GeneratedAt, b.Context.Data.AsOf, b.Health.Data.GeneratedAt = time.Time{}, time.Time{}, time.Time{}
						obsBundleTestSection(w, r, b)
					}
				}))
				defer srv.Close()
				t.Setenv("FAAS_APID_URL", srv.URL)
				t.Setenv("FAAS_ADMIN_TOKEN", "private-token")
				path := filepath.Join(t.TempDir(), "incident.json")
				if code := cmdObsBundle([]string{"--incident", obsBundleTestRef, "--output", path}); code != 1 || out.Len() != 0 {
					t.Fatalf("failure became success: code=%d out=%s", code, out.String())
				}
				if strings.Contains(stderr.String(), "private-") {
					t.Fatalf("raw response/token leaked: %s", stderr.String())
				}
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("failed capture created destination: %v", err)
				}
				if want := map[string]int32{"evidence": 1, "context": 2, "health": 3}[section]; calls.Load() != want {
					t.Fatalf("continued after failure: calls=%d want=%d", calls.Load(), want)
				}
				assertObsBundleNoTemps(t, filepath.Dir(path))
			})
		}
	}
}

func TestCmdObsBundleRefusesRedirectAndSharesDeadline(t *testing.T) {
	for _, redirect := range []bool{true, false} {
		t.Run(fmt.Sprintf("redirect=%t", redirect), func(t *testing.T) {
			out, stderr := obsBundleTestStreams(t)
			b := obsBundleTestSnapshot(obsBundleTestRef, 25, 25)
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if redirect {
					http.Redirect(w, r, "/unexpected-target", http.StatusFound)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/context") {
					<-r.Context().Done()
					return
				}
				obsBundleTestSection(w, r, b)
			}))
			defer srv.Close()
			t.Setenv("FAAS_APID_URL", srv.URL)
			t.Setenv("FAAS_ADMIN_TOKEN", "token")
			started := time.Now()
			if code := cmdObsBundle([]string{"--incident", obsBundleTestRef, "--output", "-", "--timeout", "200ms"}); code != 1 || out.Len() != 0 {
				t.Fatalf("failure became success code=%d stderr=%s", code, stderr.String())
			}
			if elapsed := time.Since(started); elapsed > 2*time.Second {
				t.Fatalf("collection was not bounded: %s", elapsed)
			}
			want := int32(2)
			if redirect {
				want = 1
			}
			if calls.Load() != want {
				t.Fatalf("unexpected requests: %d want=%d", calls.Load(), want)
			}
		})
	}
}

func TestWriteObsBundleFileRefusesExistingFilesAndSymlinks(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(fmt.Sprintf("symlink=%t", symlink), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "incident.json")
			target := path
			if symlink {
				target = filepath.Join(dir, "target")
			}
			if err := os.WriteFile(target, []byte("existing-content"), 0o644); err != nil {
				t.Fatal(err)
			}
			if symlink {
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if err := writeObsBundleFile(path, []byte("new-content")); err == nil {
				t.Fatal("existing destination overwritten")
			}
			body, err := os.ReadFile(target)
			if err != nil || string(body) != "existing-content" {
				t.Fatalf("destination changed: body=%q err=%v", body, err)
			}
			assertObsBundleNoTemps(t, dir)
		})
	}
	if err := writeObsBundleFile(filepath.Join(t.TempDir(), "missing", "incident.json"), []byte("test")); err == nil {
		t.Fatal("missing parent accepted")
	}
}

func TestMarshalObsBundleRejectsOversizedArtifact(t *testing.T) {
	b := obsBundleTestSnapshot(obsBundleTestRef, 25, 25)
	b.Limitations = []string{strings.Repeat("x", api.ObsIncidentBundleMaxBytes)}
	if body, err := marshalObsBundle(b); err == nil || body != nil {
		t.Fatal("oversized artifact accepted")
	}
}

func TestObsBundleEvidenceIdentityRejectsUnrelatedAliases(t *testing.T) {
	b := obsBundleTestSnapshot("platform_alert/"+strings.Repeat("b", 64), 25, 25)
	b.Evidence.Data.Incident.AlertEpisode = &api.ObsAlertEpisode{SeriesID: strings.Repeat("c", 64)}
	if validObsBundleEvidence("platform_alert/"+strings.Repeat("a", 64), b.Evidence.Data, 25) {
		t.Fatal("unrelated series accepted")
	}
	if validObsBundleEvidence(obsBundleTestRef, b.Evidence.Data, 25) {
		t.Fatal("unrelated resource accepted")
	}
}

func assertObsBundleNoTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(dir, ".gregalectl-obs-bundle-*"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary artifacts left: %v err=%v", entries, err)
	}
}

func TestCmdObsBundleHumanReceiptAndOtherResourceKinds(t *testing.T) {
	for _, kind := range []string{"job_run", "compute_node", "platform_alert"} {
		t.Run(kind, func(t *testing.T) {
			out, stderr := obsBundleTestStreams(t)
			ref := kind + "/11111111-1111-4111-8111-111111111111"
			if kind == "platform_alert" {
				ref = kind + "/" + strings.Repeat("a", 64)
			}
			b := obsBundleTestSnapshot(ref, 25, 25)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { obsBundleTestSection(w, r, b) }))
			defer srv.Close()
			t.Setenv("FAAS_APID_URL", srv.URL)
			t.Setenv("FAAS_ADMIN_TOKEN", "token")
			path := filepath.Join(t.TempDir(), "incident.json")
			if code := cmdObsBundle([]string{"--incident", ref, "--output", path}); code != 0 || !strings.Contains(out.String(), "Wrote operator incident bundle for "+ref) {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, out.String(), stderr.String())
			}
			body, err := os.ReadFile(path)
			if err != nil || !json.Valid(body) {
				t.Fatalf("invalid artifact: err=%v", err)
			}
		})
	}
}
