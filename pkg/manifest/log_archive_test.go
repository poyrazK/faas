package manifest

import (
	"strings"
	"testing"
)

func TestLogArchiveValidation(t *testing.T) {
	gcs := LogArchive{Bucket: "logs", Endpoint: "https://storage.googleapis.com", Auth: "gcp_metadata"}
	for _, tc := range []struct {
		name string
		l    LogArchive
		want string
	}{
		{name: "absent"},
		{name: "gcs keyless", l: gcs},
		{name: "static keys", l: LogArchive{Bucket: "logs", Endpoint: "https://s3.gra.io.cloud.ovh.net", Region: "gra", Auth: "static", CredentialsSrc: "/operator/secrets/log-archive.json"}},
		{name: "no bucket", l: LogArchive{Endpoint: gcs.Endpoint, Auth: gcs.Auth}, want: "log_archive.bucket"},
		{name: "plain http endpoint", l: LogArchive{Bucket: "logs", Endpoint: "http://minio:9000", Auth: "gcp_metadata"}, want: "log_archive.endpoint"},
		{name: "no endpoint", l: LogArchive{Bucket: "logs", Auth: "gcp_metadata"}, want: "log_archive.endpoint"},
		{name: "unknown auth", l: LogArchive{Bucket: "logs", Endpoint: gcs.Endpoint, Auth: "hmac"}, want: "log_archive.auth"},
		{name: "no auth", l: LogArchive{Bucket: "logs", Endpoint: gcs.Endpoint}, want: "log_archive.auth"},
		{name: "static without keys", l: LogArchive{Bucket: "logs", Endpoint: gcs.Endpoint, Auth: "static"}, want: "log_archive.credentials_src"},
		{name: "static relative keys", l: LogArchive{Bucket: "logs", Endpoint: gcs.Endpoint, Auth: "static", CredentialsSrc: "keys.json"}, want: "log_archive.credentials_src"},
		{name: "keyless with keys", l: LogArchive{Bucket: "logs", Endpoint: gcs.Endpoint, Auth: "gcp_metadata", CredentialsSrc: "/k.json"}, want: "log_archive.credentials_src"},
		// Observe audit 2026-11-21 (finding 7): compute hosts take a
		// create-only key.
		{name: "static compute write-only", l: LogArchive{Bucket: "logs", Endpoint: gcs.Endpoint, Auth: "static", CredentialsSrc: "/rw.json", ComputeWriteOnly: true, ComputeCredentialsSrc: "/put.json"}},
		{name: "keyless compute write-only", l: LogArchive{Bucket: "logs", Endpoint: gcs.Endpoint, Auth: "gcp_metadata", ComputeWriteOnly: true}},
		{name: "static write-only without compute key", l: LogArchive{Bucket: "logs", Endpoint: gcs.Endpoint, Auth: "static", CredentialsSrc: "/rw.json", ComputeWriteOnly: true}, want: "log_archive.compute_credentials_src"},
		{name: "compute key without write-only", l: LogArchive{Bucket: "logs", Endpoint: gcs.Endpoint, Auth: "static", CredentialsSrc: "/rw.json", ComputeCredentialsSrc: "/put.json"}, want: "log_archive.compute_credentials_src"},
		{name: "compute key with keyless auth", l: LogArchive{Bucket: "logs", Endpoint: gcs.Endpoint, Auth: "gcp_metadata", ComputeWriteOnly: true, ComputeCredentialsSrc: "/put.json"}, want: "log_archive.compute_credentials_src"},
		{name: "relative compute key", l: LogArchive{Bucket: "logs", Endpoint: gcs.Endpoint, Auth: "static", CredentialsSrc: "/rw.json", ComputeWriteOnly: true, ComputeCredentialsSrc: "put.json"}, want: "log_archive.compute_credentials_src"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := tc.l.validate()
			if tc.want == "" {
				if len(errs) != 0 {
					t.Fatalf("errors = %v", errs)
				}
				return
			}
			if len(errs) == 0 || !strings.Contains(errs[0].Path, tc.want) {
				t.Fatalf("errors = %v, want %s", errs, tc.want)
			}
		})
	}
}
