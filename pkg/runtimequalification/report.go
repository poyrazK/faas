// Package runtimequalification verifies private operator-native evidence before
// writing an ADR-599 receipt. Trust anchors are supplied separately from evidence.
package runtimequalification

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	Version           = 1
	SignatureDomain   = "gregale-runtime-qualification/v1\n"
	NativeTest        = "TestMetalRuntimeReleaseColdBootReady"
	NativePackage     = "github.com/onebox-faas/faas/pkg/fcvm"
	ObservationMarker = "GREGALE_RUNTIME_QUALIFICATION="
	LeakcheckSuccess  = "leakcheck OK — no leaked netns/taps/jails/cgroups/processes/mounts/native-loops"
)

var ErrEvidence = errors.New("runtime qualification evidence rejected")

// Trust is preselected by the operator, never loaded from the submitted bundle.
// RunID pins one acceptance attempt; source and host pins prevent borrowing a
// valid signature from a different checkout, native host or target.
type Trust struct {
	PublicKey                              ed25519.PublicKey
	ReleaseID, HostID, SourceCommit, RunID string
}

type Envelope struct {
	Version   int    `json:"version"`
	KeyID     string `json:"key_id"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// Observation is emitted only after the exact two-drive artifact cold boot,
// HTTP readiness/content probe and confirmed native retirement.
type Observation struct {
	RunID             string `json:"run_id"`
	ReleaseID         string `json:"release_id"`
	HostID            string `json:"host_id"`
	KernelBootID      string `json:"kernel_boot_id"`
	SourceCommit      string `json:"source_commit"`
	DeploymentID      string `json:"deployment_id"`
	LayerKey          string `json:"layer_key"`
	BaseSHA256        string `json:"base_sha256"`
	GuestInitSHA256   string `json:"guest_init_sha256"`
	LayerSHA256       string `json:"layer_sha256"`
	KernelSHA256      string `json:"kernel_sha256"`
	FirecrackerSHA256 string `json:"firecracker_sha256"`
	OS                string `json:"os"`
	Architecture      string `json:"architecture"`
	Virtualization    string `json:"virtualization"`
	KVM               bool   `json:"kvm"`
	ColdBoot          bool   `json:"cold_boot"`
	Ready             bool   `json:"ready"`
	Retired           bool   `json:"retired"`
}

type Report struct {
	Version           int         `json:"version"`
	Profile           string      `json:"profile"`
	StartedAt         time.Time   `json:"started_at"`
	CompletedAt       time.Time   `json:"completed_at"`
	Native            Observation `json:"native"`
	TestMetalSHA256   string      `json:"test_metal_sha256"`
	LeakcheckSHA256   string      `json:"leakcheck_sha256"`
	TestMetalExitCode *int        `json:"test_metal_exit_code"`
	LeakcheckExitCode *int        `json:"leakcheck_exit_code"`
}

// Fixture is input to the native test, not a qualification result. The native
// test measures host identity and successful lifecycle outcomes independently.
type Fixture struct {
	Target            state.RuntimeRelease `json:"target"`
	RunID             string               `json:"run_id"`
	HostID            string               `json:"host_id"`
	SourceCommit      string               `json:"source_commit"`
	DeploymentID      string               `json:"deployment_id"`
	LayerKey          string               `json:"layer_key"`
	LayerSHA256       string               `json:"layer_sha256"`
	KernelSHA256      string               `json:"kernel_sha256"`
	FirecrackerSHA256 string               `json:"firecracker_sha256"`
}

func SHA256(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func validHex(s string, length int) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(s) == length && hex.EncodeToString(b) == s
}
func validUUID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id != uuid.Nil && id.String() == s
}
func validCommit(s string) bool { return validHex(s, 40) || validHex(s, 64) }

func (t Trust) validate() error {
	if len(t.PublicKey) != ed25519.PublicKeySize || !validHex(t.ReleaseID, 64) || !validUUID(t.HostID) || !validUUID(t.RunID) || !validCommit(t.SourceCommit) {
		return fmt.Errorf("incomplete operator trust pins: %w", ErrEvidence)
	}
	return nil
}

func DecodeFixture(raw []byte) (Fixture, error) {
	var f Fixture
	if err := decodeStrict(raw, &f); err != nil {
		return f, err
	}
	if f.Target.Validate() != nil || f.Target.Architecture != "amd64" || !validUUID(f.RunID) || !validUUID(f.HostID) || !validCommit(f.SourceCommit) || !validUUID(f.DeploymentID) ||
		f.LayerKey == "" || len(f.LayerKey) > api.RuntimeReleaseArtifactKeyMaxBytes || !validHex(f.LayerSHA256, 64) || !validHex(f.KernelSHA256, 64) || !validHex(f.FirecrackerSHA256, 64) {
		return Fixture{}, fmt.Errorf("native fixture is incomplete: %w", ErrEvidence)
	}
	return f, nil
}

func VerifyEnvelope(raw []byte, trust Trust, now time.Time) (Report, []byte, error) {
	var report Report
	if err := trust.validate(); err != nil {
		return report, nil, err
	}
	var envelope Envelope
	if err := decodeStrict(raw, &envelope); err != nil {
		return report, nil, err
	}
	if envelope.Version != Version || envelope.KeyID != SHA256(trust.PublicKey) {
		return report, nil, fmt.Errorf("untrusted signer or envelope version: %w", ErrEvidence)
	}
	payload, err := base64.StdEncoding.DecodeString(envelope.Payload)
	if err != nil || base64.StdEncoding.EncodeToString(payload) != envelope.Payload {
		return report, nil, fmt.Errorf("invalid report encoding: %w", ErrEvidence)
	}
	sig, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize || base64.StdEncoding.EncodeToString(sig) != envelope.Signature || !ed25519.Verify(trust.PublicKey, append([]byte(SignatureDomain), payload...), sig) {
		return report, nil, fmt.Errorf("invalid native report signature: %w", ErrEvidence)
	}
	if err := decodeStrict(payload, &report); err != nil {
		return report, nil, err
	}
	n := report.Native
	if report.Version != Version || report.Profile != state.RuntimeQualificationProfile || report.StartedAt.IsZero() || !report.CompletedAt.After(report.StartedAt) || report.CompletedAt.After(now) ||
		report.TestMetalExitCode == nil || *report.TestMetalExitCode != 0 || report.LeakcheckExitCode == nil || *report.LeakcheckExitCode != 0 || n.OS != "linux" || n.Architecture != "amd64" || n.Virtualization != "none" || !n.KVM || !n.ColdBoot || !n.Ready || !n.Retired ||
		n.ReleaseID != trust.ReleaseID || n.HostID != trust.HostID || n.SourceCommit != trust.SourceCommit || n.RunID != trust.RunID || !validUUID(n.KernelBootID) || !validUUID(n.DeploymentID) ||
		n.LayerKey == "" || len(n.LayerKey) > api.RuntimeReleaseArtifactKeyMaxBytes {
		return Report{}, nil, fmt.Errorf("native report profile or attempt differs from operator pins: %w", ErrEvidence)
	}
	for _, s := range []string{n.BaseSHA256, n.GuestInitSHA256, n.LayerSHA256, n.KernelSHA256, n.FirecrackerSHA256, report.TestMetalSHA256, report.LeakcheckSHA256} {
		if !validHex(s, 64) {
			return Report{}, nil, fmt.Errorf("native evidence digest is incomplete: %w", ErrEvidence)
		}
	}
	canonical, err := json.Marshal(envelope)
	return report, canonical, err
}

// ReadBounded captures one immutable verification input. Callers archive the
// same bytes they verified, never reopen a mutable path after validation.
func ReadBounded(r io.Reader, maxBytes int) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, int64(maxBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("read native evidence: %w", err)
	}
	if len(b) > maxBytes {
		return nil, fmt.Errorf("native evidence exceeds %d bytes: %w", maxBytes, ErrEvidence)
	}
	return b, nil
}

func decodeStrict(raw []byte, dst any) error {
	return decodeStrictBound(raw, dst, api.RuntimeQualificationReportMaxBytes)
}

func decodeStrictBound(raw []byte, dst any, maxBytes int) error {
	if len(raw) > maxBytes || !utf8.Valid(raw) {
		return fmt.Errorf("native JSON exceeds bound or is not UTF-8: %w", ErrEvidence)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("decode native JSON: %w: %w", ErrEvidence, err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing native JSON: %w", ErrEvidence)
	}
	// encoding/json accepts duplicate keys; reject them even in signed input so
	// different consumers cannot interpret the same signature differently.
	d = json.NewDecoder(bytes.NewReader(raw))
	return uniqueJSON(d, 0)
}

func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > api.RuntimeQualificationJSONMaxDepth {
		return fmt.Errorf("native JSON nesting exceeds bound: %w", ErrEvidence)
	}
	token, err := d.Token()
	if err != nil {
		return fmt.Errorf("read native JSON token: %w", err)
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for d.More() {
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			s = strings.ToLower(s)
			if !ok || seen[s] {
				return fmt.Errorf("duplicate native JSON key: %w", ErrEvidence)
			}
			seen[s] = true
		}
		if err := uniqueJSON(d, depth+1); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}

func signatureInput(payload []byte) []byte { return append([]byte(SignatureDomain), payload...) }

// EncodeEnvelope is reserved for a trusted native acceptance owner after it has
// verified process exits and physical outcomes. The importer never has this key.
func EncodeEnvelope(report Report, metal, leak []byte, key ed25519.PrivateKey) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid native signer key: %w", ErrEvidence)
	}
	payload, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	pub, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		return nil, ErrEvidence
	}
	e := Envelope{Version: Version, KeyID: SHA256(pub), Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, signatureInput(payload)))}
	raw, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	n := report.Native
	if _, _, err := VerifyEnvelope(raw, Trust{PublicKey: pub, ReleaseID: n.ReleaseID, HostID: n.HostID, SourceCommit: n.SourceCommit, RunID: n.RunID}, time.Now().UTC()); err != nil {
		return nil, err
	}
	if err := VerifyLogs(report, metal, leak); err != nil {
		return nil, err
	}
	return raw, nil
}

func DecodeObservation(raw string) (Observation, error) {
	var n Observation
	err := decodeStrict([]byte(strings.TrimSpace(raw)), &n)
	return n, err
}
