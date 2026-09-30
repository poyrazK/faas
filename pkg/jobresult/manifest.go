// Package jobresult defines the bounded metadata a job can publish when its
// command exits. Artifact bytes stay in object storage, including Gregale
// managed buckets when the manifest uses an obj:// URI.
package jobresult

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
)

const (
	Version      = 1
	MaxBytes     = 4096
	MaxArtifacts = 16
	GuestPath    = "/tmp/gregale-output-manifest.json"
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type Artifact struct {
	Name      string `json:"name"`
	URI       string `json:"uri"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

type Manifest struct {
	Version     int        `json:"version"`
	Artifacts   []Artifact `json:"artifacts"`
	OutcomeCode string     `json:"outcome_code,omitempty"`
}

// Validate parses and checks a result before it crosses the guest exit
// channel or is committed to a task row. It does not fetch artifact bytes.
func Validate(raw []byte) (Manifest, error) {
	var manifest Manifest
	if len(raw) == 0 || len(raw) > MaxBytes {
		return manifest, fmt.Errorf("output manifest must be 1..%d bytes", MaxBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode output manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, fmt.Errorf("output manifest has trailing content")
	}
	if manifest.Version != Version || len(manifest.Artifacts) > MaxArtifacts {
		return Manifest{}, fmt.Errorf("output manifest needs version %d and at most %d artifacts", Version, MaxArtifacts)
	}
	if err := ValidateOutcomeCode(manifest.OutcomeCode); err != nil {
		return Manifest{}, err
	}
	seen := make(map[string]struct{}, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		if artifact.Name == "" || len(artifact.Name) > 128 || strings.ContainsAny(artifact.Name, "/\\\x00") {
			return Manifest{}, fmt.Errorf("invalid output artifact name")
		}
		if _, exists := seen[artifact.Name]; exists {
			return Manifest{}, fmt.Errorf("duplicate output artifact name")
		}
		seen[artifact.Name] = struct{}{}
		uri, err := url.Parse(artifact.URI)
		if err != nil || len(artifact.URI) > 2048 || uri == nil || uri.Host == "" || uri.Path == "" || uri.RawQuery != "" || uri.Fragment != "" || uri.User != nil ||
			(uri.Scheme != "s3" && uri.Scheme != "gs" && uri.Scheme != "obj") {
			return Manifest{}, fmt.Errorf("invalid output artifact URI")
		}
		if artifact.SizeBytes < 0 || !digestPattern.MatchString(artifact.SHA256) {
			return Manifest{}, fmt.Errorf("invalid output artifact size or checksum")
		}
	}
	return manifest, nil
}

// ValidateOutcomeCode checks the bounded token written by a job to describe a
// confirmed application result. Empty means the workload did not report one.
func ValidateOutcomeCode(code string) error {
	if code == "" {
		return nil
	}
	if len(code) > 64 || strings.TrimSpace(code) != code {
		return fmt.Errorf("outcome_code must be a token of at most 64 bytes")
	}
	for _, c := range code {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return fmt.Errorf("outcome_code must use lowercase letters, digits, underscore, dash, or dot")
		}
	}
	return nil
}
