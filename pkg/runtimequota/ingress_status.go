package runtimequota

// adr: 786
import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

const ingressProbeKind = "ingress-status-probe"

// ServingProbe requests read-only observations of an original quota stream.
// Its directory signature conveys no admission, billing or retirement authority.
type ServingProbe struct {
	Nonce, ConsentID                string
	Grant                           Grant
	Origin                          string
	IssuedAtMicros, ExpiresAtMicros int64
}

func (p ServingProbe) Validate() error {
	if !validID(p.Nonce) || !validID(p.ConsentID) || p.Grant.Validate() != nil ||
		!api.ValidGlobalDirectoryAPIBase(p.Origin) || p.IssuedAtMicros <= 0 ||
		p.ExpiresAtMicros <= p.IssuedAtMicros || p.ExpiresAtMicros-p.IssuedAtMicros > api.GlobalIngressLeaseDuration.Microseconds() {
		return ErrInvalid
	}
	return nil
}

func (s Signer) SignServingProbe(p ServingProbe) (Envelope, error) {
	if p.Validate() != nil || p.Grant.IssuerID != s.IssuerID() {
		return Envelope{}, ErrBinding
	}
	return signBounded(s, ingressProbeKind, p, api.GlobalIngressProbeMaxBytes)
}

func DecodeServingProbe(raw []byte) (Envelope, error) {
	return decodeBounded(raw, api.GlobalIngressProbeMaxBytes)
}

func (t Trust) VerifyServingProbe(e Envelope) (ServingProbe, error) {
	m, err := verifyBounded[ServingProbe](t, e, ingressProbeKind, Directory, api.GlobalIngressProbeMaxBytes, api.GlobalIngressProbeMaxBytes)
	if err != nil {
		return ServingProbe{}, err
	}
	if m.Claims.Validate() != nil || m.Claims.Grant.IssuerID != m.IssuerID {
		return ServingProbe{}, ErrBinding
	}
	return m.Claims, nil
}

// ServingStatus is an HTTPS-authenticated publisher observation. Echoing the
// whole challenge fences origin, stream, nonce and freshness independently of
// receipt acceptance. The native admission guard remains the execution owner.
type ServingStatus struct {
	Probe                             ServingProbe
	ObservedAtMicros, ExpiresAtMicros int64
	Healthy, Eligible                 bool
	Code                              string
}

func (s ServingStatus) Validate(p ServingProbe) error {
	if p.Validate() != nil || s.Probe != p || s.ObservedAtMicros < p.IssuedAtMicros ||
		s.ExpiresAtMicros <= s.ObservedAtMicros || s.ExpiresAtMicros > p.ExpiresAtMicros ||
		(s.Code != "ready" && s.Code != "admission_frozen" && s.Code != "unavailable") ||
		s.Eligible != (s.Code == "ready") {
		return ErrBinding
	}
	return nil
}

func DecodeServingStatus(raw []byte, p ServingProbe) (ServingStatus, error) {
	var s ServingStatus
	if len(raw) > api.GlobalIngressProbeMaxBytes {
		return s, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&s) != nil || d.Decode(&struct{}{}) != io.EOF {
		return ServingStatus{}, ErrInvalid
	}
	canonical, err := json.Marshal(s)
	if err != nil || !bytes.Equal(canonical, raw) || s.Validate(p) != nil {
		return ServingStatus{}, ErrBinding
	}
	return s, nil
}
