package runtimeadmission

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/protobuf/proto"
)

// Promotion has a distinct payload domain and a fresh token. The parent receipt
// is historical lease identity, so validate it at completion, not at today's
// clock. A healthy resident VM does not expire with its initial boot grant.
type Promotion struct {
	Binding Binding `json:"binding"`
	Parent  Receipt `json:"parent"`
}

func (p Promotion) Equal(other Promotion) bool {
	return p.Binding == other.Binding && p.Parent.Equal(other.Parent)
}

func (p Promotion) Clone() Promotion {
	p.Parent = p.Parent.Clone()
	return p
}

func HashPromotionPayload(req *vmmdpb.PromoteAdmittedRuntimeRequest) (string, error) {
	if req == nil || req.Parent == nil || RejectUnknown(req) != nil {
		return "", ErrInvalid
	}
	copy := proto.Clone(req).(*vmmdpb.PromoteAdmittedRuntimeRequest)
	copy.Binding = nil
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(copy)
	if err != nil {
		return "", ErrInvalid
	}
	hash := sha256.Sum256(append([]byte("gregale.runtime-promotion.v1\x00"), raw...))
	return hex.EncodeToString(hash[:]), nil
}

func (p Promotion) Validate(now time.Time) error {
	if err := p.Binding.Validate(now); err != nil {
		return err
	}
	if !p.Parent.Paused || p.Parent.Check(p.Parent.Binding, time.Unix(0, p.Parent.CompletedAtUnixNano)) != nil || p.Parent.CompletedAtUnixNano > now.Add(api.ApplicationStandardRuntimeAdmissionClockSkew).UnixNano() {
		return ErrInvalid
	}
	return p.checkFreshPromotionBinding()
}

func (p Promotion) checkFreshPromotionBinding() error {
	old, fresh := p.Parent.Binding, p.Binding
	if fresh.Token == old.Token {
		return ErrReplay
	}
	old.Token, old.PayloadHash, old.IssuedAtUnixNano, old.ExpiresAtUnixNano = fresh.Token, fresh.PayloadHash, fresh.IssuedAtUnixNano, fresh.ExpiresAtUnixNano
	if old != fresh {
		return ErrStale
	}
	hash, err := HashPromotionPayload(p.ToProto())
	if err != nil || fresh.PayloadHash != hash {
		return ErrInvalid
	}
	return nil
}

func (p Promotion) CheckReceipt(r Receipt, now time.Time) error {
	if err := p.Validate(now); err != nil {
		return err
	}
	if err := r.Check(p.Binding, now); err != nil {
		return err
	}
	if r.Paused || r.NativeInputHash != p.Parent.NativeInputHash || r.Netns != p.Parent.Netns || r.HostIP != p.Parent.HostIP || r.LeaseUID != p.Parent.LeaseUID || r.Method != p.Parent.Method || r.CompletedAtUnixNano < p.Parent.CompletedAtUnixNano {
		return ErrInvalid
	}
	return nil
}

func (p Promotion) ToProto() *vmmdpb.PromoteAdmittedRuntimeRequest {
	return &vmmdpb.PromoteAdmittedRuntimeRequest{Binding: p.Binding.ToProto(), Parent: p.Parent.ToProto()}
}

func PromotionFromProto(req *vmmdpb.PromoteAdmittedRuntimeRequest) (Promotion, error) {
	if RejectUnknown(req) != nil {
		return Promotion{}, ErrInvalid
	}
	b, err := BindingFromProto(req.Binding)
	if err != nil {
		return Promotion{}, err
	}
	r, err := ReceiptFromProto(req.Parent)
	return Promotion{Binding: b, Parent: r}, err
}
