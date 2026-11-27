package manifest

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// LogArchive is the off-host bucket every control-plane and compute host
// archives customer logs to (pkg/logarchive). The log_archive role writes it
// into /etc/faas/secrets/storage-box/archive-creds.json. Absent leaves each
// host's envelope as the operator provisioned it.
type LogArchive struct {
	Bucket string `yaml:"bucket,omitempty"`
	// Endpoint is the provider's S3-compatible https URL
	// (https://storage.googleapis.com for GCS).
	Endpoint string `yaml:"endpoint,omitempty"`
	// Region is the SigV4 signing region; empty means "auto".
	Region string `yaml:"region,omitempty"`
	// Auth is "gcp_metadata" (the host's attached service account, no keys)
	// or "static" (an S3 key pair, OVH, R2, MinIO).
	Auth string `yaml:"auth,omitempty"`
	// CredentialsSrc is the controller-local JSON file holding the static key
	// pair, {"key_id": …, "secret": …}. Keys never go in the manifest.
	CredentialsSrc string `yaml:"credentials_src,omitempty"`
	// ComputeWriteOnly gives compute-only hosts create-only archive access
	// (faas_log_archive_write_only, ADR-20261110210000 slice 3l): their
	// envelope says "access": "write-only", vmmd ships create-only parts and
	// gatewayd-internal skips read-back there. Credentials that can read the
	// bucket let a rooted compute node read every tenant's archived logs.
	ComputeWriteOnly bool `yaml:"compute_write_only,omitempty"`
	// ComputeCredentialsSrc is the controller-local key pair compute-only
	// hosts stage instead of CredentialsSrc with static auth: a key allowed
	// s3:PutObject on the bucket and nothing else. Required with static auth
	// and ComputeWriteOnly (Observe audit 2026-11-21, finding 7).
	ComputeCredentialsSrc string `yaml:"compute_credentials_src,omitempty"`
}

// Configured reports whether the manifest names an archive bucket.
func (l LogArchive) Configured() bool { return l != LogArchive{} }

func (l LogArchive) validate() Errors {
	if !l.Configured() {
		return nil
	}
	var errs Errors
	if strings.TrimSpace(l.Bucket) == "" {
		errs = append(errs, Error{"log_archive.bucket", "is required when log_archive is set"})
	}
	if u, err := url.Parse(l.Endpoint); err != nil || u.Scheme != "https" || u.Host == "" {
		errs = append(errs, Error{"log_archive.endpoint", fmt.Sprintf("%q must be an https URL", l.Endpoint)})
	}
	switch l.Auth {
	case "gcp_metadata":
		if l.CredentialsSrc != "" {
			errs = append(errs, Error{"log_archive.credentials_src", "must be empty with gcp_metadata auth"})
		}
	case "static":
		if !filepath.IsAbs(l.CredentialsSrc) {
			errs = append(errs, Error{"log_archive.credentials_src", "must be an absolute controller path with static auth"})
		}
	default:
		errs = append(errs, Error{"log_archive.auth", fmt.Sprintf("%q must be gcp_metadata or static", l.Auth)})
	}
	switch {
	case l.ComputeCredentialsSrc != "" && l.Auth != "static":
		errs = append(errs, Error{"log_archive.compute_credentials_src", "is for static auth; gcp_metadata compute hosts use their service account's create-only grant"})
	case l.ComputeCredentialsSrc != "" && !l.ComputeWriteOnly:
		errs = append(errs, Error{"log_archive.compute_credentials_src", "needs compute_write_only: a create-only key cannot serve read-back"})
	case l.ComputeCredentialsSrc != "" && !filepath.IsAbs(l.ComputeCredentialsSrc):
		errs = append(errs, Error{"log_archive.compute_credentials_src", "must be an absolute controller path"})
	case l.ComputeWriteOnly && l.Auth == "static" && l.ComputeCredentialsSrc == "":
		errs = append(errs, Error{"log_archive.compute_credentials_src", "is required with compute_write_only and static auth: compute hosts would otherwise stage the read-write key"})
	}
	return errs
}
