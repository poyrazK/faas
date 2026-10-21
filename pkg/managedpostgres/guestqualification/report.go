// Package guestqualification verifies operator-owned guest evidence without
// contacting SQL, a provider, or a VM daemon. Digests bind evidence; they are
// not signatures or remote attestation of an untrusted test host.
package guestqualification

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"regexp"
	"time"
)

const Version = 4
const MaxTTL = 7 * 24 * time.Hour
const IdentityPrefix = "GREGALE_POSTGRES_GUEST_IDENTITY "

//go:embed contract.json
var contractJSON []byte

type Contract struct {
	Version   int      `json:"version"`
	Package   string   `json:"package"`
	Root      string   `json:"root"`
	Workloads []string `json:"workloads"`
	Phases    []string `json:"phases"`
	Artifacts []string `json:"artifacts"`
}

func SuiteContract() Contract {
	var result Contract
	if err := json.Unmarshal(contractJSON, &result); err != nil {
		panic("invalid embedded guest qualification contract")
	}
	return result
}

// RuntimeManifest identifies the qualification bundle, including test-tagged
// daemons. Its source revision must match the intended release; its daemon
// hashes are not hashes of the differently tagged production executables.
type RuntimeManifest struct {
	Version        int               `json:"version"`
	SourceRevision string            `json:"source_revision"`
	Artifacts      map[string]string `json:"artifacts"`
}

type Identity struct {
	Runtime       RuntimeManifest `json:"runtime"`
	PostgresMajor int             `json:"postgres_major"`
}

type PhaseCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

type Report struct {
	Version         int             `json:"version"`
	SuiteVersion    int             `json:"suite_version"`
	RootTest        string          `json:"root_test"`
	HostClass       string          `json:"host_class"`
	HostOS          string          `json:"host_os"`
	HostArch        string          `json:"host_arch"`
	SourceClean     bool            `json:"source_clean"`
	PostgresMajor   int             `json:"postgres_major"`
	StartedAt       time.Time       `json:"started_at"`
	CompletedAt     time.Time       `json:"completed_at"`
	ExpiresAt       time.Time       `json:"expires_at"`
	Runtime         RuntimeManifest `json:"runtime"`
	Checks          []PhaseCheck    `json:"checks"`
	LeakCheckBefore bool            `json:"leakcheck_before"`
	LeakCheckAfter  bool            `json:"leakcheck_after"`
	ResultsSHA256   string          `json:"results_sha256"`
	EvidenceSHA256  string          `json:"evidence_sha256"`
}

type Readiness struct {
	Ready   bool     `json:"ready"`
	Reasons []string `json:"reasons,omitempty"`
}

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (m RuntimeManifest) Valid() bool {
	c := SuiteContract()
	if m.Version != Version || !revisionPattern.MatchString(m.SourceRevision) || len(m.Artifacts) != len(c.Artifacts) {
		return false
	}
	for _, name := range c.Artifacts {
		if !digestPattern.MatchString(m.Artifacts[name]) {
			return false
		}
	}
	applications, sources := map[string]bool{}, map[string]bool{}
	for _, workload := range c.Workloads {
		app, source := m.Artifacts[workload+"_app_layer"], m.Artifacts[workload+"_source_archive"]
		if applications[app] || sources[source] {
			return false
		}
		applications[app], sources[source] = true, true
	}
	return true
}

// EvidenceDigest uses sorted JSON object keys shared with the Python producer.
// No free-form test output, paths, SQL, or credentials enter this document.
func EvidenceDigest(report Report) string {
	encoded, err := json.Marshal(report)
	if err != nil {
		return ""
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return ""
	}
	delete(object, "evidence_sha256")
	canonical, err := json.Marshal(object)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:])
}

func Evaluate(report *Report, expected *RuntimeManifest, postgresMajor int, now time.Time) Readiness {
	result := Readiness{}
	add := func(reason string) { result.Reasons = append(result.Reasons, reason) }
	if expected == nil {
		add("runtime_manifest_missing")
	} else if !expected.Valid() {
		add("runtime_manifest_invalid")
	}
	if report == nil {
		add("guest_report_missing")
		return result
	}
	c := SuiteContract()
	if report.Version != Version || report.SuiteVersion != c.Version || report.RootTest != c.Root {
		add("guest_contract_invalid")
	}
	if report.HostClass != "native" || report.HostOS != "linux" || report.HostArch != "amd64" {
		add("guest_host_not_native")
	}
	if !report.SourceClean {
		add("guest_source_dirty")
	}
	if !report.Runtime.Valid() {
		add("guest_runtime_invalid")
	} else if expected != nil && expected.Valid() && !reflect.DeepEqual(report.Runtime, *expected) {
		add("guest_runtime_mismatch")
	}
	if postgresMajor < 14 || postgresMajor > 18 || report.PostgresMajor != postgresMajor {
		add("guest_postgres_major_mismatch")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if report.StartedAt.IsZero() || !report.CompletedAt.After(report.StartedAt) || report.CompletedAt.After(now) || !report.ExpiresAt.After(report.CompletedAt) || report.ExpiresAt.Sub(report.CompletedAt) > MaxTTL || report.CompletedAt.Sub(report.StartedAt) > 2*time.Hour {
		add("guest_window_invalid")
	} else if !report.ExpiresAt.After(now) {
		add("guest_report_expired")
	}
	checks := make(map[string]bool)
	validChecks := len(report.Checks) == len(c.Phases)
	for index, check := range report.Checks {
		if index >= len(c.Phases) || check.Name != c.Phases[index] {
			validChecks = false
		}
		if _, duplicate := checks[check.Name]; duplicate || !check.Passed {
			validChecks = false
		}
		checks[check.Name] = check.Passed
	}
	for _, phase := range c.Phases {
		if !checks[phase] {
			validChecks = false
		}
	}
	if !validChecks {
		add("guest_phases_incomplete")
	}
	if !report.LeakCheckBefore || !report.LeakCheckAfter {
		add("guest_host_cleanup_unverified")
	}
	if !digestPattern.MatchString(report.ResultsSHA256) || !digestPattern.MatchString(report.EvidenceSHA256) || report.EvidenceSHA256 != EvidenceDigest(*report) {
		add("guest_evidence_digest_mismatch")
	}
	result.Ready = len(result.Reasons) == 0
	return result
}

// Load rejects unknown fields, oversized documents and trailing JSON. The
// operator controls these files; error details never need to cross an API.
func Load[T any](path string) (T, error) {
	var result T
	pathInfo, err := os.Stat(path)
	if err != nil || !pathInfo.Mode().IsRegular() {
		return result, errors.New("invalid qualification document")
	}
	// This offline loader reads operator-selected evidence, never request paths.
	f, err := os.Open(path) //nolint:forbidigo
	if err != nil {
		return result, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return result, errors.New("invalid qualification document")
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return result, errors.New("invalid qualification document")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&result); err != nil {
		return result, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return result, errors.New("trailing qualification data")
	}
	return result, nil
}
