package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// Observe audit 2026-11-21 (finding 7): the manifest staged one read-write
// archive key on every host; compute hosts now take the write-only envelope
// and a create-only key.
func TestRenderLogArchiveVarsComputeWriteOnly(t *testing.T) {
	static := manifest.LogArchive{
		Bucket: "logs", Endpoint: "https://s3.bhs.io.cloud.ovh.net", Region: "bhs", Auth: "static",
		CredentialsSrc: "/operator/rw.json", ComputeWriteOnly: true, ComputeCredentialsSrc: "/operator/put.json",
	}
	for _, tc := range []struct {
		name        string
		l           manifest.LogArchive
		computeOnly bool
		want        []string
		absent      []string
	}{
		{"control plane keeps the read-write key", static, false,
			[]string{`faas_log_archive_credentials_src: "/operator/rw.json"`}, []string{"faas_log_archive_write_only", "put.json"}},
		{"compute stages the create-only key", static, true,
			[]string{`faas_log_archive_credentials_src: "/operator/put.json"`, "faas_log_archive_write_only: true"}, []string{"rw.json"}},
		{"keyless compute is write-only", manifest.LogArchive{Bucket: "logs", Endpoint: "https://storage.googleapis.com", Auth: "gcp_metadata", ComputeWriteOnly: true}, true,
			[]string{`faas_log_archive_auth_mode: "gcp_metadata"`, "faas_log_archive_write_only: true"}, []string{"credentials_src"}},
		{"without compute_write_only compute matches the control plane", manifest.LogArchive{Bucket: "logs", Endpoint: "https://s3.bhs.io.cloud.ovh.net", Auth: "static", CredentialsSrc: "/operator/rw.json"}, true,
			[]string{`faas_log_archive_credentials_src: "/operator/rw.json"`}, []string{"faas_log_archive_write_only"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := renderLogArchiveVars(tc.l, tc.computeOnly)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(got, a) {
					t.Errorf("unexpected %q in:\n%s", a, got)
				}
			}
		})
	}
}
