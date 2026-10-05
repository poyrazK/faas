// Package runtimeadmission defines the private schedd-to-vmmd boot contract.
// A receipt acknowledges a bound native boot, never consumer convergence.
package runtimeadmission

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

const ProtocolVersion = 1

var (
	ErrInvalid     = errors.New("runtime admission: invalid binding or receipt")
	ErrStale       = errors.New("runtime admission: stale inputs or process identity")
	ErrExpired     = errors.New("runtime admission: grant expired")
	ErrReplay      = errors.New("runtime admission: grant or instance already used")
	ErrUnavailable = errors.New("runtime admission: native capability unavailable")
	ErrCapacity    = errors.New("runtime admission: replay window at capacity")
)

type Identity struct {
	ProtocolVersion        uint32
	NodeID, Incarnation    string
	SnapshotRestoreVersion uint32 `json:"SnapshotRestoreVersion,omitempty"`
}

func (i Identity) Validate() error {
	if !supportedProtocol(i.ProtocolVersion) || !canonicalUUID(i.NodeID) || !canonicalUUID(i.Incarnation) || i.SnapshotRestoreVersion > SnapshotRestoreVersion || i.SnapshotRestoreVersion != 0 && i.ProtocolVersion != ArtifactProtocolVersion {
		return ErrUnavailable
	}
	return nil
}

// Binding is comparable so all grant fields must match an acknowledgment.
type Binding struct {
	ProtocolVersion      uint32 `json:"protocol_version"`
	Token                string `json:"token"`
	InstanceID           string `json:"instance_id"`
	AppID                string `json:"app_id"`
	DeploymentID         string `json:"deployment_id"`
	AccountID            string `json:"account_id"`
	NodeID               string `json:"node_id"`
	Incarnation          string `json:"incarnation"`
	DesiredRevision      int64  `json:"desired_revision"`
	EffectiveHash        string `json:"effective_hash"`
	CapturedInputHash    string `json:"captured_input_hash"`
	PayloadHash          string `json:"payload_hash"`
	EgressRevision       int64  `json:"egress_revision"`
	IssuedAtUnixNano     int64  `json:"issued_at_unix_nano"`
	ExpiresAtUnixNano    int64  `json:"expires_at_unix_nano"`
	ArtifactSourcesHash  string `json:"artifact_sources_hash,omitempty"`
	SnapshotCaptureToken string `json:"snapshot_capture_token,omitempty"`
	SnapshotEvidenceHash string `json:"snapshot_evidence_hash,omitempty"`
}

func (b Binding) Validate(now time.Time) error {
	if !supportedProtocol(b.ProtocolVersion) || b.DesiredRevision <= 0 || b.EgressRevision <= 0 || !b.validArtifactProtocol() || !b.validSnapshotBinding() {
		return ErrInvalid
	}
	for _, id := range []string{b.Token, b.InstanceID, b.AppID, b.DeploymentID, b.AccountID, b.NodeID, b.Incarnation} {
		if !canonicalUUID(id) {
			return ErrInvalid
		}
	}
	for _, hash := range []string{b.EffectiveHash, b.CapturedInputHash, b.PayloadHash} {
		if !ValidHash(hash) {
			return ErrInvalid
		}
	}
	issued, expires := time.Unix(0, b.IssuedAtUnixNano), time.Unix(0, b.ExpiresAtUnixNano)
	if b.IssuedAtUnixNano <= 0 || !expires.After(issued) || expires.Sub(issued) > api.ApplicationStandardRuntimeAdmissionTTL || issued.After(now.Add(api.ApplicationStandardRuntimeAdmissionClockSkew)) {
		return ErrInvalid
	}
	if !now.Before(expires) {
		return ErrExpired
	}
	return nil
}

func supportedProtocol(version uint32) bool {
	return version == ProtocolVersion || version == ArtifactProtocolVersion
}

func (b Binding) validArtifactProtocol() bool {
	return b.ProtocolVersion == ProtocolVersion && b.ArtifactSourcesHash == "" || b.ProtocolVersion == ArtifactProtocolVersion && ValidHash(b.ArtifactSourcesHash)
}

func canonicalUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func ValidHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

func (b Binding) ToProto() *vmmdpb.RuntimeBootBinding {
	return &vmmdpb.RuntimeBootBinding{ProtocolVersion: b.ProtocolVersion, Token: b.Token, InstanceId: b.InstanceID, AppId: b.AppID, DeploymentId: b.DeploymentID, AccountId: b.AccountID, NodeId: b.NodeID, Incarnation: b.Incarnation, DesiredRevision: b.DesiredRevision, EffectiveHash: b.EffectiveHash, CapturedInputHash: b.CapturedInputHash, PayloadHash: b.PayloadHash, EgressRevision: b.EgressRevision, IssuedAtUnixNano: b.IssuedAtUnixNano, ExpiresAtUnixNano: b.ExpiresAtUnixNano, ArtifactSourcesHash: b.ArtifactSourcesHash, SnapshotCaptureToken: b.SnapshotCaptureToken, SnapshotEvidenceHash: b.SnapshotEvidenceHash}
}

func BindingFromProto(p *vmmdpb.RuntimeBootBinding) (Binding, error) {
	if p == nil || RejectUnknown(p) != nil {
		return Binding{}, ErrInvalid
	}
	return Binding{ProtocolVersion: p.ProtocolVersion, Token: p.Token, InstanceID: p.InstanceId, AppID: p.AppId, DeploymentID: p.DeploymentId, AccountID: p.AccountId, NodeID: p.NodeId, Incarnation: p.Incarnation, DesiredRevision: p.DesiredRevision, EffectiveHash: p.EffectiveHash, CapturedInputHash: p.CapturedInputHash, PayloadHash: p.PayloadHash, EgressRevision: p.EgressRevision, IssuedAtUnixNano: p.IssuedAtUnixNano, ExpiresAtUnixNano: p.ExpiresAtUnixNano, ArtifactSourcesHash: p.ArtifactSourcesHash, SnapshotCaptureToken: p.SnapshotCaptureToken, SnapshotEvidenceHash: p.SnapshotEvidenceHash}, nil
}

type Receipt struct {
	Binding             Binding             `json:"binding"`
	NativeInputHash     string              `json:"native_input_hash"`
	Netns               string              `json:"netns"`
	HostIP              string              `json:"host_ip"`
	LeaseUID            int32               `json:"lease_uid"`
	Method              vmmdpb.WakeMethod   `json:"method"`
	Paused              bool                `json:"paused"`
	CompletedAtUnixNano int64               `json:"completed_at_unix_nano"`
	ArtifactConsumption ArtifactConsumption `json:"artifact_consumption,omitzero"`
	SnapshotConsumption SnapshotConsumption `json:"snapshot_consumption,omitzero"`
}

func (r Receipt) Check(binding Binding, now time.Time) error {
	if err := binding.Validate(now); err != nil {
		return err
	}
	if r.Binding != binding || !ValidHash(r.NativeInputHash) || r.Netns == "" || r.LeaseUID <= 0 || (r.Method != vmmdpb.WakeMethod_WAKE_COLD_BOOT && r.Method != vmmdpb.WakeMethod_WAKE_RESTORE) {
		return ErrInvalid
	}
	ip, err := netip.ParseAddr(r.HostIP)
	if err != nil || !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() {
		return ErrInvalid
	}
	completed := time.Unix(0, r.CompletedAtUnixNano)
	if completed.Before(time.Unix(0, binding.IssuedAtUnixNano).Add(-api.ApplicationStandardRuntimeAdmissionClockSkew)) || completed.After(now.Add(api.ApplicationStandardRuntimeAdmissionClockSkew)) || r.CompletedAtUnixNano >= binding.ExpiresAtUnixNano {
		return ErrInvalid
	}
	return r.checkArtifactProtocol()
}

func (r Receipt) checkArtifactProtocol() error {
	if r.Binding.ProtocolVersion == ProtocolVersion {
		if !r.ArtifactConsumption.IsZero() || !r.SnapshotConsumption.IsZero() {
			return ErrInvalid
		}
		return nil
	}
	// Paused restore remains unavailable until the native and durable
	// promotion path carries complete load and resume lineage.
	if r.Paused {
		return ErrUnavailable
	}
	if r.Method == vmmdpb.WakeMethod_WAKE_RESTORE {
		if r.SnapshotConsumption.IsZero() {
			return ErrUnavailable
		}
		return r.SnapshotConsumption.Check(r.Binding, r.ArtifactConsumption, false)
	}
	if !r.SnapshotConsumption.IsZero() {
		return ErrInvalid
	}
	return r.ArtifactConsumption.Check(r.Binding.ArtifactSourcesHash)
}

func (r Receipt) Equal(other Receipt) bool {
	return r.Binding == other.Binding && r.NativeInputHash == other.NativeInputHash && r.Netns == other.Netns && r.HostIP == other.HostIP && r.LeaseUID == other.LeaseUID && r.Method == other.Method && r.Paused == other.Paused && r.CompletedAtUnixNano == other.CompletedAtUnixNano && r.ArtifactConsumption.Equal(other.ArtifactConsumption) && r.SnapshotConsumption == other.SnapshotConsumption
}

func (r Receipt) Clone() Receipt {
	r.ArtifactConsumption = r.ArtifactConsumption.Clone()
	return r
}

func (r Receipt) ToProto() *vmmdpb.RuntimeBootReceipt {
	return &vmmdpb.RuntimeBootReceipt{Binding: r.Binding.ToProto(), NativeInputHash: r.NativeInputHash, Netns: r.Netns, HostIp: r.HostIP, LeaseUid: r.LeaseUID, Method: r.Method, Paused: r.Paused, CompletedAtUnixNano: r.CompletedAtUnixNano, ArtifactConsumption: r.ArtifactConsumption.ToProto(), SnapshotConsumption: r.SnapshotConsumption.ToProto()}
}

func ReceiptFromProto(p *vmmdpb.RuntimeBootReceipt) (Receipt, error) {
	if p == nil || RejectUnknown(p) != nil {
		return Receipt{}, ErrInvalid
	}
	b, err := BindingFromProto(p.Binding)
	if err != nil {
		return Receipt{}, err
	}
	consumption, err := artifactConsumptionFromProto(p.ArtifactConsumption)
	if err != nil {
		return Receipt{}, err
	}
	snapshot, err := snapshotConsumptionFromProto(p.SnapshotConsumption)
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{Binding: b, NativeInputHash: p.NativeInputHash, Netns: p.Netns, HostIP: p.HostIp, LeaseUID: p.LeaseUid, Method: p.Method, Paused: p.Paused, CompletedAtUnixNano: p.CompletedAtUnixNano, ArtifactConsumption: consumption, SnapshotConsumption: snapshot}, nil
}
