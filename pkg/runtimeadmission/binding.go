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
	ProtocolVersion     uint32
	NodeID, Incarnation string
}

func (i Identity) Validate() error {
	if i.ProtocolVersion != ProtocolVersion || !canonicalUUID(i.NodeID) || !canonicalUUID(i.Incarnation) {
		return ErrUnavailable
	}
	return nil
}

// Binding is comparable so all grant fields must match an acknowledgment.
type Binding struct {
	ProtocolVersion                                   uint32
	Token, InstanceID, AppID, DeploymentID, AccountID string
	NodeID, Incarnation                               string
	DesiredRevision                                   int64
	EffectiveHash, CapturedInputHash, PayloadHash     string
	EgressRevision                                    int64
	IssuedAtUnixNano, ExpiresAtUnixNano               int64
}

func (b Binding) Validate(now time.Time) error {
	if b.ProtocolVersion != ProtocolVersion || b.DesiredRevision <= 0 || b.EgressRevision <= 0 {
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
	return &vmmdpb.RuntimeBootBinding{ProtocolVersion: b.ProtocolVersion, Token: b.Token, InstanceId: b.InstanceID, AppId: b.AppID, DeploymentId: b.DeploymentID, AccountId: b.AccountID, NodeId: b.NodeID, Incarnation: b.Incarnation, DesiredRevision: b.DesiredRevision, EffectiveHash: b.EffectiveHash, CapturedInputHash: b.CapturedInputHash, PayloadHash: b.PayloadHash, EgressRevision: b.EgressRevision, IssuedAtUnixNano: b.IssuedAtUnixNano, ExpiresAtUnixNano: b.ExpiresAtUnixNano}
}

func BindingFromProto(p *vmmdpb.RuntimeBootBinding) (Binding, error) {
	if p == nil {
		return Binding{}, ErrInvalid
	}
	return Binding{ProtocolVersion: p.ProtocolVersion, Token: p.Token, InstanceID: p.InstanceId, AppID: p.AppId, DeploymentID: p.DeploymentId, AccountID: p.AccountId, NodeID: p.NodeId, Incarnation: p.Incarnation, DesiredRevision: p.DesiredRevision, EffectiveHash: p.EffectiveHash, CapturedInputHash: p.CapturedInputHash, PayloadHash: p.PayloadHash, EgressRevision: p.EgressRevision, IssuedAtUnixNano: p.IssuedAtUnixNano, ExpiresAtUnixNano: p.ExpiresAtUnixNano}, nil
}

type Receipt struct {
	Binding             Binding
	NativeInputHash     string
	Netns, HostIP       string
	LeaseUID            int32
	Method              vmmdpb.WakeMethod
	Paused              bool
	CompletedAtUnixNano int64
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
	return nil
}

func (r Receipt) ToProto() *vmmdpb.RuntimeBootReceipt {
	return &vmmdpb.RuntimeBootReceipt{Binding: r.Binding.ToProto(), NativeInputHash: r.NativeInputHash, Netns: r.Netns, HostIp: r.HostIP, LeaseUid: r.LeaseUID, Method: r.Method, Paused: r.Paused, CompletedAtUnixNano: r.CompletedAtUnixNano}
}

func ReceiptFromProto(p *vmmdpb.RuntimeBootReceipt) (Receipt, error) {
	if p == nil {
		return Receipt{}, ErrInvalid
	}
	b, err := BindingFromProto(p.Binding)
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{Binding: b, NativeInputHash: p.NativeInputHash, Netns: p.Netns, HostIP: p.HostIp, LeaseUID: p.LeaseUid, Method: p.Method, Paused: p.Paused, CompletedAtUnixNano: p.CompletedAtUnixNano}, nil
}
