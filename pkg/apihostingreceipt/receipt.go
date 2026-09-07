// Package apihostingreceipt defines the durable, machine-readable evidence
// captured when an API deployment becomes ready.
package apihostingreceipt

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/frameworkprofile"
)

const SchemaVersion = 1

const (
	SmokeVerified = "verified"
	SmokeFailed   = "failed"
	SmokeSkipped  = "skipped"
)

// Source contains non-sensitive provenance. It intentionally excludes source
// paths, environment values, and customer code.
type Source struct {
	Kind        string `json:"kind,omitempty"`
	URL         string `json:"url,omitempty"`
	CommitSHA   string `json:"commit_sha,omitempty"`
	ImageDigest string `json:"image_digest,omitempty"`
}

type Artifact struct {
	RootfsKey   string `json:"rootfs_key,omitempty"`
	RootfsBytes int64  `json:"rootfs_bytes,omitempty"`
}

type SmokeResult struct {
	Status     string    `json:"status"`
	Path       string    `json:"path,omitempty"`
	StatusCode int       `json:"status_code,omitempty"`
	LatencyMS  int64     `json:"latency_ms,omitempty"`
	VerifiedAt time.Time `json:"verified_at,omitempty"`
	RequestID  string    `json:"request_id,omitempty"`
	ErrorCode  string    `json:"error_code,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type Receipt struct {
	SchemaVersion int                      `json:"schema_version"`
	DeploymentID  string                   `json:"deployment_id"`
	AppID         string                   `json:"app_id"`
	AppURL        string                   `json:"app_url,omitempty"`
	Source        Source                   `json:"source"`
	Profile       frameworkprofile.Profile `json:"profile"`
	Artifact      Artifact                 `json:"artifact"`
	Smoke         SmokeResult              `json:"smoke"`
}

func (r Receipt) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", r.SchemaVersion)
	}
	if strings.TrimSpace(r.DeploymentID) == "" {
		return errors.New("deployment_id is required")
	}
	if strings.TrimSpace(r.AppID) == "" {
		return errors.New("app_id is required")
	}
	if r.Profile.Version == "" {
		return errors.New("profile.version is required")
	}
	if !strings.HasPrefix(r.Profile.HealthPath, "/") {
		return errors.New("profile.health_path must start with /")
	}
	switch r.Smoke.Status {
	case SmokeVerified, SmokeFailed, SmokeSkipped:
	default:
		return fmt.Errorf("invalid smoke status %q", r.Smoke.Status)
	}
	return nil
}

func Encode(r Receipt) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(r)
}

func Decode(data []byte) (Receipt, error) {
	var r Receipt
	if err := json.Unmarshal(data, &r); err != nil {
		return Receipt{}, err
	}
	if err := r.Validate(); err != nil {
		return Receipt{}, err
	}
	return r, nil
}
