package cirunner

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

func TestArtifactInputDownloadVerifiesBytesAndRetries(t *testing.T) {
	for _, mode := range []string{"binary", "empty", "lost-body", "corrupt", "wrong-digest", "missing", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			raw := []byte{'a', 0, 255, 'z'}
			if mode == "empty" {
				raw = nil
			}
			input := ci.JobsInput{ArtifactInput: ci.ArtifactInput{Name: "binary", Job: "build", Path: "dist/program"}, AttemptID: uuid.NewString(), Bytes: int64(len(raw)), Digest: blobDigest(raw)}
			proof := ci.RuntimeProof{RunID: uuid.NewString(), InstanceID: uuid.NewString(), Capability: strings.Repeat("a", 64)}
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if r.URL.Path != "/v1/internal/ci/runtime/"+proof.RunID+"/inputs/binary" || r.Header.Get(ci.RuntimeCapabilityHeader) != proof.Capability {
					t.Error("input request lost scope")
				}
				if mode == "missing" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				digest := input.Digest
				if mode == "wrong-digest" {
					digest = blobDigest([]byte("other"))
				}
				w.Header().Set(ci.RuntimeDigestHeader, digest)
				w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
				if mode == "lost-body" && n == 1 {
					_, _ = w.Write(raw[:1])
					return
				}
				if mode == "corrupt" {
					_, _ = w.Write(bytes.Repeat([]byte{'x'}, len(raw)))
					return
				}
				_, _ = w.Write(raw)
			}))
			defer server.Close()
			transport, err := NewTransport(server.URL, proof, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			runner := Runner{Manifest: ci.JobsManifest{Inputs: []ci.JobsInput{input}}, Transport: transport}
			err = runner.prepareInputs(ctx, root)
			success := mode == "binary" || mode == "empty" || mode == "lost-body"
			data, readErr := os.ReadFile(filepath.Join(dir, ci.ArtifactInputsDirectory, "binary"))
			if success {
				if err != nil || readErr != nil || !bytes.Equal(data, raw) {
					t.Fatalf("verified input: %v %v", err, readErr)
				}
				if mode == "lost-body" && calls.Load() != 2 {
					t.Fatal("lost body did not retry immutable input")
				}
			} else if err == nil || !os.IsNotExist(readErr) {
				t.Fatal("invalid input left consumable bytes")
			}
			if mode == "missing" && calls.Load() != 1 || (mode == "corrupt" || mode == "wrong-digest") && calls.Load() != api.CIJobsTransferMaxAttempts {
				t.Fatal("input retry budget changed")
			}
		})
	}
}

func TestArtifactInputDownloadRefusesSourceAndSymlinkCollisions(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		dir := t.TempDir()
		outside := t.TempDir()
		name := filepath.Join(dir, ci.ArtifactInputsDirectory)
		if symlink {
			if err := os.Symlink(outside, name); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.Mkdir(name, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		runner := Runner{Manifest: ci.JobsManifest{Inputs: []ci.JobsInput{{ArtifactInput: ci.ArtifactInput{Name: "file"}}}}}
		if err := runner.prepareInputs(t.Context(), root); err == nil {
			t.Fatal("source input directory collision accepted")
		}
		_ = root.Close()
		entries, err := os.ReadDir(outside)
		if err != nil || len(entries) != 0 {
			t.Fatal("input escaped guest workspace")
		}
	}
}
