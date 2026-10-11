// adr: 948
package validatorbundle

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceBuildPublicationAndPromotionTransfer(t *testing.T) {
	artifacts, _, b := artifactFixture()
	body, err := json.Marshal(Source{Runtime: b.Runtime, Entrypoint: b.Entrypoint, Files: b.Files})
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "source.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err = tw.WriteHeader(&tar.Header{Name: "apps/api/gregale.validator.json", Mode: 0600, Typeflag: tar.TypeReg, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err = tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err = tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if err = artifacts.Build(t.Context(), b.AppID, b.DeploymentID, archive, "apps/api"); err != nil {
		t.Fatal(err)
	}
	got, err := artifacts.Resolve(t.Context(), b.AppID, b.DeploymentID)
	if err != nil || got.SHA256 != b.SHA256 {
		t.Fatal(got, err)
	}
	target := uuid.NewString()
	if err = artifacts.Transfer(t.Context(), b.AppID, b.DeploymentID, target); err != nil {
		t.Fatal(err)
	}
	got, err = artifacts.Resolve(t.Context(), b.AppID, target)
	if err != nil || got.SHA256 != b.SHA256 || got.DeploymentID != target {
		t.Fatal(got, err)
	}
	if err = artifacts.Build(t.Context(), b.AppID, uuid.NewString(), archive, "other"); err == nil {
		t.Fatal("missing source accepted")
	}
	if err = artifacts.Build(t.Context(), b.AppID, uuid.NewString(), archive, "../apps/api"); err == nil {
		t.Fatal("unsafe source root accepted")
	}
}

func TestSourceBuildRejectsDuplicateLinkedAndMalformedDescriptors(t *testing.T) {
	for _, kind := range []string{"duplicate", "symlink", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			artifacts, memory, b := artifactFixture()
			body, err := json.Marshal(Source{Runtime: b.Runtime, Entrypoint: b.Entrypoint, Files: b.Files})
			if err != nil {
				t.Fatal(err)
			}
			if kind == "malformed" {
				body = []byte(`{"runtime":"node22","entrypoint":"v.mjs","files":[],"secret_env":{}}`)
			}
			archive := filepath.Join(t.TempDir(), "source.tar.gz")
			f, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(f)
			tw := tar.NewWriter(gz)
			repeats := 1
			if kind == "duplicate" {
				repeats = 2
			}
			for range repeats {
				header := &tar.Header{Name: "gregale.validator.json", Mode: 0600, Typeflag: tar.TypeReg, Size: int64(len(body))}
				if kind == "symlink" {
					header.Typeflag = tar.TypeSymlink
					header.Size = 0
					header.Linkname = "other.json"
				}
				if err = tw.WriteHeader(header); err != nil {
					t.Fatal(err)
				}
				if header.Typeflag == tar.TypeReg {
					if _, err = tw.Write(body); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err = tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err = gz.Close(); err != nil {
				t.Fatal(err)
			}
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
			if err = artifacts.Build(t.Context(), b.AppID, b.DeploymentID, archive, ""); err == nil {
				t.Fatal("invalid descriptor accepted")
			}
			if memory.writes != 0 {
				t.Fatal("invalid source published", memory.writes)
			}
		})
	}
}
