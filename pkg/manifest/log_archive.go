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
	return errs
}
