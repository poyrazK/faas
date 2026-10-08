package runtimequota

// adr: 784
import "github.com/onebox-faas/faas/pkg/api"

const ingressKind = "global-ingress"

// IngressSnapshot is a short-lived directory projection. Healthy/Eligible are
// publisher observations, never permission to launch, bill or release a runtime.
type IngressSnapshot struct {
	DirectoryID                     string
	Generation                      int64
	IssuedAtMicros, ExpiresAtMicros int64
	RoutingPolicy                   string
	Routes                          []IngressRoute
}

type IngressRoute struct {
	GlobalAppID string
	Targets     []IngressTarget
}

type IngressTarget struct {
	Binding           Binding
	Origin            string // Regional app HTTPS origin, distinct from its management API.
	Healthy, Eligible bool
}

func (s IngressSnapshot) Validate() error {
	if !validID(s.DirectoryID) || s.Generation < 1 || s.Generation > api.GlobalRuntimeQuotaGenerationMax ||
		s.IssuedAtMicros <= 0 || s.ExpiresAtMicros <= s.IssuedAtMicros || s.ExpiresAtMicros-s.IssuedAtMicros > api.GlobalIngressLeaseDuration.Microseconds() ||
		s.RoutingPolicy != api.GlobalRoutingPolicyAllSelected || len(s.Routes) > api.GlobalIngressRoutesMax {
		return ErrInvalid
	}
	apps, bindings := map[string]bool{}, map[string]bool{}
	for _, route := range s.Routes {
		if !validID(route.GlobalAppID) || apps[route.GlobalAppID] || len(route.Targets) > api.GlobalRuntimeQuotaTargetsMax {
			return ErrBinding
		}
		apps[route.GlobalAppID] = true
		regions, cells := map[string]bool{}, map[string]bool{}
		for _, target := range route.Targets {
			b := target.Binding
			identity := b.CellID + "/" + b.RegionalAppID
			if b.Validate() != nil || !api.ValidGlobalDirectoryAPIBase(target.Origin) || regions[b.Region] || cells[b.CellID] || bindings[identity] {
				return ErrBinding
			}
			regions[b.Region], cells[b.CellID], bindings[identity] = true, true, true
		}
	}
	return nil
}

func (s Signer) SignIngressSnapshot(snapshot IngressSnapshot) (Envelope, error) {
	if snapshot.Validate() != nil || snapshot.DirectoryID != s.issuerID {
		return Envelope{}, ErrInvalid
	}
	return signBounded(s, ingressKind, snapshot, api.GlobalIngressPayloadMaxBytes)
}

func DecodeIngressSnapshot(raw []byte) (Envelope, error) {
	return decodeBounded(raw, api.GlobalIngressEnvelopeMaxBytes)
}

func (t Trust) VerifyIngressSnapshot(envelope Envelope, directoryID string) (IngressSnapshot, error) {
	m, err := verifyBounded[IngressSnapshot](t, envelope, ingressKind, Directory, api.GlobalIngressPayloadMaxBytes, api.GlobalIngressEnvelopeMaxBytes)
	if err != nil {
		return IngressSnapshot{}, err
	}
	if m.Claims.Validate() != nil || m.Claims.DirectoryID != m.IssuerID || m.IssuerID != directoryID {
		return IngressSnapshot{}, ErrBinding
	}
	return m.Claims, nil
}
