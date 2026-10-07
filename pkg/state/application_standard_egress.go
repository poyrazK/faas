package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"slices"
	"time"
)

// These private facts describe current serving nodes, never fleet membership or
// whole-application observation. Missing process capability remains pending.
type ApplicationStandardEgressTarget struct {
	OrgID           string                        `json:"org_id"`
	AppID           string                        `json:"app_id"`
	DesiredRevision int64                         `json:"desired_revision"`
	EffectiveHash   string                        `json:"effective_hash"`
	Identity        runtimeadmission.Identity     `json:"identity"`
	Policy          runtimeadmission.EgressPolicy `json:"policy"`
	PolicyHash      string                        `json:"policy_hash"`
}

type ApplicationStandardEgressObservation struct {
	Target     ApplicationStandardEgressTarget `json:"target"`
	Receipt    runtimeadmission.EgressReceipt  `json:"receipt"`
	ObservedAt time.Time                       `json:"observed_at"`
}

type ApplicationStandardEgressStore interface {
	ListPendingApplicationStandardEgress(context.Context, string) ([]ApplicationStandardEgressTarget, error)
	RecordApplicationStandardEgress(context.Context, ApplicationStandardEgressTarget, runtimeadmission.EgressReceipt) (ApplicationStandardEgressObservation, error)
	ListApplicationStandardEgress(context.Context, string, string) ([]ApplicationStandardEgressObservation, error)
}

func (t ApplicationStandardEgressTarget) valid() bool {
	hash, err := t.Policy.Hash()
	return validStandardResourceRead(t.OrgID, t.AppID) && t.DesiredRevision > 0 && runtimeadmission.ValidHash(t.EffectiveHash) && t.Identity.Validate() == nil && t.Identity.ProtocolVersion == runtimeadmission.ArtifactProtocolVersion && t.Policy.AppID == t.AppID && err == nil && hash == t.PolicyHash
}

func (t ApplicationStandardEgressTarget) clone() ApplicationStandardEgressTarget {
	t.Policy = t.Policy.Clone()
	return t
}

func sameStandardEgress(a, b ApplicationStandardEgressTarget) bool {
	return a.OrgID == b.OrgID && a.AppID == b.AppID && a.DesiredRevision == b.DesiredRevision && a.EffectiveHash == b.EffectiveHash && a.Identity == b.Identity && a.PolicyHash == b.PolicyHash && a.Policy.AppID == b.Policy.AppID && a.Policy.Revision == b.Policy.Revision && slices.Equal(a.Policy.Allowlist, b.Policy.Allowlist) && slices.Equal(a.Policy.Ports, b.Policy.Ports)
}
