package runtimeadmission

import (
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

// EgressPolicy binds the complete requested projection, including base ports.
// Hashing uses masked address bytes so PostgreSQL and Go share IPv6 semantics.
type EgressPolicy struct {
	AppID     string         `json:"app_id"`
	Revision  int64          `json:"revision"`
	Allowlist []netip.Prefix `json:"allowlist"`
	Ports     []int          `json:"ports"`
}

type EgressReceipt struct {
	Identity   Identity `json:"identity"`
	AppID      string   `json:"app_id"`
	Revision   int64    `json:"revision"`
	PolicyHash string   `json:"policy_hash"`
}

func (p EgressPolicy) Clone() EgressPolicy {
	p.Allowlist = append([]netip.Prefix{}, p.Allowlist...)
	p.Ports = append([]int{}, p.Ports...)
	return p
}

func (p EgressPolicy) Hash() (string, error) {
	if !canonicalUUID(p.AppID) || p.Revision <= 0 {
		return "", ErrInvalid
	}
	cidrs := make([]string, len(p.Allowlist))
	for n, c := range p.Allowlist {
		if !c.IsValid() || c.Bits() == 0 {
			return "", ErrInvalid
		}
		c = c.Masked()
		cidrs[n] = hex.EncodeToString(c.Addr().AsSlice()) + "/" + strconv.Itoa(c.Bits())
	}
	slices.Sort(cidrs)
	cidrs = slices.Compact(cidrs)
	ports := slices.Clone(p.Ports)
	for _, port := range ports {
		if _, bad := api.TenantEgressForbiddenPort(port); port < 1 || port > 65535 || bad {
			return "", ErrInvalid
		}
	}
	slices.Sort(ports)
	ports = slices.Compact(ports)
	values := make([]string, len(ports))
	for n, port := range ports {
		values[n] = strconv.Itoa(port)
	}
	raw := "gregale.egress-policy.v1\n" + p.AppID + "\n" + strconv.FormatInt(p.Revision, 10) + "\n" + strings.Join(cidrs, ",") + "\n" + strings.Join(values, ",")
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:]), nil
}

func (r EgressReceipt) Check(identity Identity, p EgressPolicy) error {
	if identity.Validate() != nil || identity.ProtocolVersion != ArtifactProtocolVersion {
		return ErrUnavailable
	}
	hash, err := p.Hash()
	if err != nil {
		return err
	}
	if r.Identity != identity || r.AppID != p.AppID || r.Revision != p.Revision || r.PolicyHash != hash {
		return ErrInvalid
	}
	return nil
}

func EgressRequest(identity Identity, p EgressPolicy) (*vmmdpb.UpdateAdmittedAppEgressPolicyRequest, error) {
	if identity.Validate() != nil || identity.ProtocolVersion != ArtifactProtocolVersion {
		return nil, ErrUnavailable
	}
	if _, err := p.Hash(); err != nil {
		return nil, err
	}
	req := &vmmdpb.UpdateAppEgressPolicyRequest{AppId: p.AppID, Revision: p.Revision}
	for _, c := range p.Allowlist {
		req.EgressAllowlist = append(req.EgressAllowlist, c.String())
	}
	for _, port := range p.Ports {
		req.EgressPorts = append(req.EgressPorts, uint32(port))
	}
	return &vmmdpb.UpdateAdmittedAppEgressPolicyRequest{Identity: &vmmdpb.RuntimeAdmissionIdentityResponse{ProtocolVersion: identity.ProtocolVersion, NodeId: identity.NodeID, Incarnation: identity.Incarnation}, Policy: req}, nil
}

func EgressFromProto(req *vmmdpb.UpdateAdmittedAppEgressPolicyRequest) (Identity, EgressPolicy, error) {
	if req == nil || req.Identity == nil || req.Policy == nil || RejectUnknown(req) != nil {
		return Identity{}, EgressPolicy{}, ErrInvalid
	}
	i := Identity{ProtocolVersion: req.Identity.ProtocolVersion, NodeID: req.Identity.NodeId, Incarnation: req.Identity.Incarnation}
	p := EgressPolicy{AppID: req.Policy.AppId, Revision: req.Policy.Revision, Allowlist: []netip.Prefix{}, Ports: []int{}}
	for _, raw := range req.Policy.EgressAllowlist {
		c, err := netip.ParsePrefix(raw)
		if err != nil {
			return i, p, ErrInvalid
		}
		p.Allowlist = append(p.Allowlist, c)
	}
	for _, port := range req.Policy.EgressPorts {
		p.Ports = append(p.Ports, int(port))
	}
	_, err := EgressRequest(i, p)
	return i, p, err
}

func (r EgressReceipt) ToProto() *vmmdpb.UpdateAdmittedAppEgressPolicyAck {
	return &vmmdpb.UpdateAdmittedAppEgressPolicyAck{Identity: &vmmdpb.RuntimeAdmissionIdentityResponse{ProtocolVersion: r.Identity.ProtocolVersion, NodeId: r.Identity.NodeID, Incarnation: r.Identity.Incarnation}, AppId: r.AppID, Revision: r.Revision, PolicyHash: r.PolicyHash}
}

func EgressReceiptFromProto(p *vmmdpb.UpdateAdmittedAppEgressPolicyAck) (EgressReceipt, error) {
	if p == nil || p.Identity == nil || RejectUnknown(p) != nil {
		return EgressReceipt{}, ErrInvalid
	}
	return EgressReceipt{Identity: Identity{ProtocolVersion: p.Identity.ProtocolVersion, NodeID: p.Identity.NodeId, Incarnation: p.Identity.Incarnation}, AppID: p.AppId, Revision: p.Revision, PolicyHash: p.PolicyHash}, nil
}
