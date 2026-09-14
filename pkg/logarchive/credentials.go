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
}

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
	return c
}
