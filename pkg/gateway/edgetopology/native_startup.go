package edgetopology

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

// NativeStartupReview selects one public startup and the exact main process
// holding its endpoint. This is selected inventory, not a complete fencing scope.
type NativeStartupReview struct {
	Scope   NativeScopeReview `json:"scope"`
	Binding Binding           `json:"binding"`
}

// NativeStartupSnapshot is the stored observation format. Decoding this DTO
// does not authenticate it or reconstruct a NativeStartupObservation.
type NativeStartupSnapshot struct {
	Review    NativeStartupReview      `json:"review"`
	Native    []NativeScopeObservation `json:"native"`
	Proof     json.RawMessage          `json:"proof"`
	CheckedAt time.Time                `json:"checked_at"`
}

// NativeStartupObservation can only be produced by fresh native reconciliation.
// It is not an enrollment, permanent host attribution or termination receipt.
type NativeStartupObservation struct {
	startup  ingress.NativePublicStartup
	envelope []byte
}

func (o NativeStartupObservation) Startup() ingress.NativePublicStartup { return o.startup }
func (o NativeStartupObservation) Envelope() []byte                     { return slices.Clone(o.envelope) }

type NativeStartupProbe struct {
	token string
	open  func(context.Context, NativeScopeReview) (nativeScopeSession, error)
}

// NewNativeStartupProbe uses only the retained local Linux collector. It does
// not accept caller-provided facts, paths, HTTP clients or process readers.
func NewNativeStartupProbe(token string) (*NativeStartupProbe, error) {
	if ingress.ValidateToken(token) != nil {
		return nil, ErrNativeUnverified
	}
	return &NativeStartupProbe{token: token, open: newNativeScopeSession}, nil
}

// CanonicalNativeStartupReview validates and owns the administrative selection;
// it supplies no observation or authentication authority.
func CanonicalNativeStartupReview(review NativeStartupReview) (NativeStartupReview, ingress.NativePublicStartup, error) {
	scope, err := freezeNativeScope(review.Scope)
	if err != nil || len(scope.Services) != 1 {
		return NativeStartupReview{}, ingress.NativePublicStartup{}, ErrNativeUnverified
	}
	bindings, err := reviewedBindings([]Binding{review.Binding})
	if err != nil || !slices.Contains(scope.Services[0].TCPListeners, review.Binding.Address) {
		return NativeStartupReview{}, ingress.NativePublicStartup{}, ErrNativeUnverified
	}
	host, service, id := scope.Host, scope.Services[0], bindings[0].Edge
	startup := ingress.NativePublicStartup{SlotID: id.SlotID, SessionID: id.SessionID, ConfigSHA256: id.ConfigSHA256, Epoch: ingress.NativeProcessEpoch{
		MachineID: host.MachineID, BootID: host.BootID, PID: service.PID, StartTicks: service.StartTicks, PIDNamespace: host.PIDNamespace, NetNamespace: host.NetNamespace,
	}}
	if ingress.ValidateNativePublicStartup(startup) != nil {
		return NativeStartupReview{}, ingress.NativePublicStartup{}, ErrNativeUnverified
	}
	return NativeStartupReview{Scope: scope, Binding: bindings[0]}, startup, nil
}

func (p *NativeStartupProbe) Observe(ctx context.Context, review NativeStartupReview) (NativeStartupObservation, error) {
	if p == nil || p.open == nil || ctx == nil || ctx.Err() != nil {
		return NativeStartupObservation{}, ErrNativeUnverified
	}
	frozen, expected, err := CanonicalNativeStartupReview(review)
	if err != nil {
		return NativeStartupObservation{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeNativeStartupTimeout)
	defer cancel()
	session, err := p.open(ctx, frozen.Scope)
	if err != nil || session == nil {
		return NativeStartupObservation{}, ErrNativeUnverified
	}
	defer func() { _ = session.Close() }()
	before, err := session.Capture(ctx, frozen.Scope)
	if err != nil {
		return NativeStartupObservation{}, ErrNativeUnverified
	}
	proof, err := ingress.ProbeNativePublicStartup(ctx, frozen.Binding.Address, p.token, expected)
	if err != nil {
		return NativeStartupObservation{}, ErrNativeUnverified
	}
	after, err := session.Capture(ctx, frozen.Scope)
	if err != nil || !sameNativeScope(before, after) || ctx.Err() != nil {
		return NativeStartupObservation{}, ErrNativeUnverified
	}
	record := NativeStartupSnapshot{Review: frozen, Native: []NativeScopeObservation{before, after}, Proof: proof.Envelope(), CheckedAt: time.Now().UTC()}
	raw, err := json.Marshal(record)
	if err != nil || ValidateNativeStartupRecord(raw, frozen) != nil || ctx.Err() != nil {
		return NativeStartupObservation{}, ErrNativeUnverified
	}
	return NativeStartupObservation{startup: proof.Startup(), envelope: raw}, nil
}
