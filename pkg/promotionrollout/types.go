// Package promotionrollout gates new verified promotions using operator trust.
// It never authorizes reconciliation, publication, rollback or recovery.
package promotionrollout

import (
	"crypto/ed25519"
	"encoding/json"
	"slices"
	"time"
)

type Capability string

const (
	Secrets         Capability = "secrets"
	Health          Capability = "health"
	ServiceGraph    Capability = "service_graph"
	Workers         Capability = "workers"
	Migrations      Capability = "migrations"
	WorkerSet       Capability = "worker_set"
	Coordinated     Capability = "coordinated"
	Version                    = 1
	signatureDomain            = "gregale.promotion-rollout.v1\n"
)

func Capabilities() []Capability {
	return []Capability{Health, ServiceGraph, Workers, Migrations, WorkerSet, Coordinated, Secrets}
}

// RequiredGates need reviewed, digest-bound receipts from a trusted signer.
// The publication partition supplies bounded crash evidence for review; other
// variants and the remaining gates are not implied by that CI partition.
func RequiredGates(capability Capability) []string {
	gates := []string{"http-failure-variants", "operator-intent-races", "publication-crash-boundaries", "retention-failures"}
	switch capability {
	case Secrets:
		gates = append(gates, "promotion-secret-cutover", "promotion-secret-rollback", "promotion-secret-recovery", "promotion-secret-key-rotation")
	case ServiceGraph:
		gates = append(gates, "service-graph-routing", "service-graph-recovery", "service-graph-isolation")
	case Workers:
		gates = append(gates, "worker-intent-races", "worker-baseline-variants", "worker-retention-expiry", "worker-replacement-resolve")
	case Migrations:
		gates = append(gates, "migration-service-graphs", "migration-resolve", "migration-input-drift", "migration-stale-dispatch", "migration-vmmd-outage", "migration-lane-races", "migration-deadline-exhaustion")
	case WorkerSet:
		for _, name := range RequiredGates(Workers) {
			if !slices.Contains(gates, name) {
				gates = append(gates, name)
			}
		}
		gates = append(gates, "worker-set-cutover", "worker-set-rollback", "worker-set-recovery")
	case Coordinated:
		for _, dependency := range []Capability{Workers, Migrations} {
			for _, name := range RequiredGates(dependency) {
				if !slices.Contains(gates, name) {
					gates = append(gates, name)
				}
			}
		}
		gates = append(gates, "coordinated-migration-workers")
	}
	return gates
}

type Target struct {
	AccountID string `json:"account_id"`
	ProjectID string `json:"project_id"`
}

// Policy can only narrow the signed approval. Atomic replacement takes effect
// at the next admission check; APID does not cache a last-known-good policy.
type Policy struct {
	SchemaVersion int          `json:"schema_version"`
	Paused        bool         `json:"paused"`
	ApprovalPath  string       `json:"approval_path"`
	Targets       []Target     `json:"targets"`
	Capabilities  []Capability `json:"capabilities"`
}

type GateReceipt struct {
	Outcome        string `json:"outcome"`
	EvidenceSHA256 string `json:"evidence_sha256"`
}

type Approval struct {
	SchemaVersion     int                            `json:"schema_version"`
	SourceSHA         string                         `json:"source_sha"`
	IssuedAt          time.Time                      `json:"issued_at"`
	ExpiresAt         time.Time                      `json:"expires_at"`
	Targets           []Target                       `json:"targets"`
	Capabilities      []Capability                   `json:"capabilities"`
	NativeReport      json.RawMessage                `json:"native_report"`
	CapabilityReports map[Capability]json.RawMessage `json:"capability_reports,omitempty"`
	Gates             map[string]GateReceipt         `json:"gates"`
}

type Envelope struct {
	SchemaVersion int    `json:"schema_version"`
	KeyID         string `json:"key_id"`
	Payload       string `json:"payload"`
	Signature     string `json:"signature"`
}

type CapabilityReadiness struct {
	Capability Capability `json:"capability"`
	Ready      bool       `json:"ready"`
	Reasons    []string   `json:"reasons"`
}

type Readiness struct {
	SchemaVersion int                   `json:"schema_version"`
	SourceSHA     string                `json:"source_sha"`
	Mode          string                `json:"mode"`
	Target        Target                `json:"target"`
	Capabilities  []CapabilityReadiness `json:"capabilities"`
}

type Config struct {
	PolicyPath string
	PublicKey  ed25519.PublicKey
	SourceSHA  string
	Enabled    map[Capability]bool
	Now        func() time.Time
}

type Gate struct {
	config          Config
	bootstrapReason string
	qualification   bool
}
