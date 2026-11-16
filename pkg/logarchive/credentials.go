package logarchive

import (
	"encoding/json"
	"fmt"
	"os"
)

// Credentials is the unsealed JSON shape used by the log archive
// shipper and the gateway read-back handler. The file is delivered through
// systemd LoadCredential= at runtime; it is never committed to the repo.
//
// Bucket is optional for backwards compatibility with the first envelope
// format. Operators may keep the non-secret bucket in FAAS_LOG_ARCHIVE_BUCKET
// while rotating only the access credentials.
type Credentials struct {
	Endpoint string `json:"endpoint,omitempty"`
	Region   string `json:"region,omitempty"`
	Bucket   string `json:"bucket,omitempty"`
	AuthMode string `json:"auth_mode,omitempty"`
	KeyID    string `json:"key_id,omitempty"`
	Secret   string `json:"secret,omitempty"`
	// Access is "" or "read-write" (the default), or "write-only" on a
	// compute node whose credentials may only create objects
	// (ADR-20261110210000 slice 3l): the daemon then ships part objects,
	// never reads the bucket back, and leaves read-back to the control plane.
	Access string `json:"access,omitempty"`
}

// AccessWriteOnly marks an envelope whose credentials may only create
// objects.
const AccessWriteOnly = "write-only"

// ReadCredentials reads and parses an unsealed archive credential file.
// Missing files are intentionally returned unchanged as os.ErrNotExist so
// callers can keep the archive feature disabled on an unconfigured host.
func ReadCredentials(path string) (Credentials, error) {
	var creds Credentials
	data, err := os.ReadFile(path)
	if err != nil {
		return creds, err
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return creds, fmt.Errorf("logarchive: parse %s: %w", path, err)
	}
	switch creds.Access {
	case "", "read-write", AccessWriteOnly:
	default:
		return creds, fmt.Errorf("logarchive: %s: access %q: want read-write or write-only", path, creds.Access)
	}
	return creds, nil
}

// WithCredentials fills unset configuration fields from creds. Explicit
// FAAS_LOG_ARCHIVE_* values win over the envelope. The region flag preserves
// that precedence even though ConfigFromEnv supplies a default region.
func (c Config) WithCredentials(creds Credentials) Config {
	if c.Endpoint == "" {
		c.Endpoint = creds.Endpoint
	}
	if (c.Region == "" || (!c.regionExplicit && c.Region == "us-east-1")) && creds.Region != "" {
		c.Region = creds.Region
	}
	if c.Bucket == "" {
		c.Bucket = creds.Bucket
	}
	if c.AuthMode == "" {
		c.AuthMode = creds.AuthMode
	}
	if c.KeyID == "" {
		c.KeyID = creds.KeyID
	}
	if c.Secret == "" {
		c.Secret = creds.Secret
	}
	// A write-only envelope cannot read the day object a whole-day rebuild
	// starts from, so it always ships part objects.
	if creds.Access == AccessWriteOnly {
		c.WriteOnly = true
		c.PartObjects = true
	}
	return c
}
