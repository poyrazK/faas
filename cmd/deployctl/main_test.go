package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/releasebundle"
)

func TestDeploymentRecordsFromManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "release.sbom.json"), []byte("sbom"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := releasebundle.Manifest{
		ReleaseID: "0123456789abcdef0123456789abcdef01234567",
		CommitSHA: "0123456789abcdef0123456789abcdef01234567",
		CreatedAt: time.Now().UTC(),
		Files: []releasebundle.File{
			{Path: "bin/apid", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			{Path: "bin/deployctl", SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		},
	}
	records, err := deploymentRecords(root, manifest)
	if err != nil {
		t.Fatalf("deploymentRecords: %v", err)
	}
	if len(records) != 1 || records[0].Daemon != "apid" {
		t.Fatalf("records = %#v, want one apid record", records)
	}
	if records[0].Version != "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("version = %q", records[0].Version)
	}
	if records[0].SBOMSHA256 == "" {
		t.Fatal("SBOM digest missing")
	}
}

func TestDeploymentRecordsRejectBadCommit(t *testing.T) {
	_, err := deploymentRecords(t.TempDir(), releasebundle.Manifest{CommitSHA: "bad"})
	if err == nil {
		t.Fatal("bad commit unexpectedly accepted")
	}
}
