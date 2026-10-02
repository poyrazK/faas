// adr: 430 — bind an ordinary native boot to an exact, single-use grant.
package fcvm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type AdmittedWakeRequest struct {
	Request         WakeRequest
	Binding         runtimeadmission.Binding
	NativeInputHash string
}

type runtimeAdmissionFlight struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// WithRuntimeAdmissionNodeID is startup wiring. Once the native identity is
// exposed it cannot be changed within this process, including by gRPC callers.
func (m *Manager) WithRuntimeAdmissionNodeID(nodeID string) *Manager {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtimeAdmissionIncarnation == "" {
		m.runtimeAdmissionNodeID = strings.TrimSpace(nodeID)
	}
	return m
}

func (m *Manager) RuntimeAdmissionIdentity() (runtimeadmission.Identity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtimeAdmissionIncarnation == "" {
		m.runtimeAdmissionIncarnation = uuid.NewString()
	}
	identity := runtimeadmission.Identity{ProtocolVersion: runtimeadmission.ProtocolVersion, NodeID: m.runtimeAdmissionNodeID, Incarnation: m.runtimeAdmissionIncarnation}
	if err := identity.Validate(); err != nil {
		return runtimeadmission.Identity{}, err
	}
	return identity, nil
}

// NativeWakeInputHash covers every exported native boot input, including sealed
// ciphertext, sidecars and snapshot locators. Bytes are transient and never
// emitted in a receipt, error or log. Private prepared plaintext is excluded.
func NativeWakeInputHash(request WakeRequest) (string, error) {
	raw, err := marshalNativeWakeInputs(request)
	if err != nil {
		return "", runtimeadmission.ErrInvalid
	}
	hash := sha256.Sum256(append([]byte("gregale.native-boot.v1\x00"), raw...))
	return hex.EncodeToString(hash[:]), nil
}

func cloneAdmittedWake(request WakeRequest) (WakeRequest, error) {
	// The wire adapter supplies exported inputs only. Clone before entering a
	// flight so a caller cannot change an aliased sidecar, env or snapshot.
	raw, err := marshalNativeWakeInputs(request)
	if err != nil {
		return WakeRequest{}, runtimeadmission.ErrInvalid
	}
	var copy WakeRequest
	if json.Unmarshal(raw, &copy) != nil {
		return WakeRequest{}, runtimeadmission.ErrInvalid
	}
	return copy, nil
}

func marshalNativeWakeInputs(request WakeRequest) ([]byte, error) {
	// Existing wire adapters represent an absent optional probe as an empty
	// RawMessage. It has the same runtime meaning as nil, but is invalid JSON.
	if len(request.ReadinessProbe) == 0 {
		request.ReadinessProbe = nil
	}
	if len(request.LivenessProbe) == 0 {
		request.LivenessProbe = nil
	}
	return json.Marshal(request)
}

func (m *Manager) WakeAdmitted(ctx context.Context, request AdmittedWakeRequest, hook WakeNetworkReadyHook) (*Instance, runtimeadmission.Receipt, error) {
	binding := request.Binding
	if err := binding.Validate(time.Now()); err != nil {
		return nil, runtimeadmission.Receipt{}, err
	}
	identity, err := m.RuntimeAdmissionIdentity()
	if err != nil {
		return nil, runtimeadmission.Receipt{}, err
	}
	if binding.NodeID != identity.NodeID || binding.Incarnation != identity.Incarnation {
		return nil, runtimeadmission.Receipt{}, runtimeadmission.ErrStale
	}
	req, err := cloneAdmittedWake(request.Request)
	if err != nil {
		return nil, runtimeadmission.Receipt{}, err
	}
	hash, err := NativeWakeInputHash(req)
	if err != nil || hash != request.NativeInputHash || req.Instance != binding.InstanceID || req.AppID != binding.AppID || req.DeploymentID != binding.DeploymentID || req.AccountID != binding.AccountID || req.ExecutionOnly || req.AppTaskOnly || req.ExportDir != "" || req.BuildTimeoutSec != 0 || req.KeepPaused && req.Snapshot == nil {
		return nil, runtimeadmission.Receipt{}, runtimeadmission.ErrInvalid
	}
	flightCtx, cancel := context.WithDeadline(ctx, time.Unix(0, binding.ExpiresAtUnixNano))
	defer cancel()
	flight := &runtimeAdmissionFlight{ctx: flightCtx, cancel: cancel, done: make(chan struct{})}
	if err := m.consumeRuntimeAdmission(binding, flight); err != nil {
		return nil, runtimeadmission.Receipt{}, err
	}
	defer m.finishRuntimeAdmissionFlight(req.Instance, flight)
	req.admission = &binding
	unlockPolicy, err := m.lockAppEgressPolicyForWake(flightCtx, req.AppID)
	if err != nil {
		return nil, runtimeadmission.Receipt{}, err
	}
	defer unlockPolicy()
	inst, err := m.wake(flightCtx, req, hook)
	if err != nil {
		return nil, runtimeadmission.Receipt{}, err
	}
	// Cancellation/expiry after readiness still refuses a receipt and destroys
	// the VM. Finish the flight first so cleanup cannot wait on its own caller.
	m.mu.Lock()
	completedAt := time.Now()
	err = binding.Validate(completedAt)
	if err == nil {
		err = flightCtx.Err()
	}
	if err == nil && m.live[req.Instance] != inst {
		err = runtimeadmission.ErrStale
	}
	if err != nil {
		m.mu.Unlock()
		m.finishRuntimeAdmissionFlight(req.Instance, flight)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), api.ApplicationStandardRuntimeCleanupTimeout)
		defer cleanupCancel()
		return nil, runtimeadmission.Receipt{}, errors.Join(err, m.Destroy(cleanupCtx, req.Instance))
	}
	method := vmmdpb.WakeMethod_WAKE_COLD_BOOT
	if inst.Method == WakeRestore {
		method = vmmdpb.WakeMethod_WAKE_RESTORE
	}
	receipt := runtimeadmission.Receipt{Binding: binding, NativeInputHash: hash, Netns: inst.Net.Netns, HostIP: inst.Lease.HostIP.String(), LeaseUID: int32(inst.Lease.UID), Method: method, Paused: inst.Paused, CompletedAtUnixNano: completedAt.UnixNano()}
	inst.runtimeAdmissionReceipt = receipt
	inst.runtimeAdmissionEgress = WakeRequest{AppID: req.AppID, Plan: req.Plan, EgressAllowlist: slices.Clone(req.EgressAllowlist), EgressPorts: slices.Clone(req.EgressPorts)}
	m.finishRuntimeAdmissionFlightLocked(req.Instance, flight)
	m.mu.Unlock()
	return inst, receipt, nil
}

func (m *Manager) consumeRuntimeAdmission(binding runtimeadmission.Binding, flight *runtimeAdmissionFlight) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if err := binding.Validate(now); err != nil {
		return err
	}
	for token, expiry := range m.runtimeAdmissionTokens {
		if !now.Before(expiry) {
			delete(m.runtimeAdmissionTokens, token)
		}
	}
	for instance, expiry := range m.runtimeAdmissionInstances {
		if !now.Before(expiry) {
			delete(m.runtimeAdmissionInstances, instance)
		}
	}
	_, tokenUsed := m.runtimeAdmissionTokens[binding.Token]
	_, instanceUsed := m.runtimeAdmissionInstances[binding.InstanceID]
	_, waking := m.waking[binding.InstanceID]
	if tokenUsed || instanceUsed || waking || m.live[binding.InstanceID] != nil || m.runtimeAdmissionFlights[binding.InstanceID] != nil {
		return runtimeadmission.ErrReplay
	}
	if len(m.runtimeAdmissionTokens) >= api.ApplicationStandardRuntimeAdmissionReplayLimit {
		return runtimeadmission.ErrCapacity
	}
	if m.runtimeAdmissionTokens == nil {
		m.runtimeAdmissionTokens = map[string]time.Time{}
	}
	if m.runtimeAdmissionInstances == nil {
		m.runtimeAdmissionInstances = map[string]time.Time{}
	}
	if m.runtimeAdmissionFlights == nil {
		m.runtimeAdmissionFlights = map[string]*runtimeAdmissionFlight{}
	}
	m.runtimeAdmissionTokens[binding.Token] = time.Unix(0, binding.ExpiresAtUnixNano)
	m.runtimeAdmissionInstances[binding.InstanceID] = time.Unix(0, binding.ExpiresAtUnixNano)
	m.runtimeAdmissionFlights[binding.InstanceID] = flight
	return nil
}

// Called with mu and the per-app read gate held, before any native allocation.
func (m *Manager) checkAdmittedEgressLocked(req WakeRequest) error {
	if err := req.admission.Validate(time.Now()); err != nil {
		return err
	}
	current, exists := m.appEgressPolicies[req.AppID]
	if !exists || current.revision != req.admission.EgressRevision {
		return runtimeadmission.ErrStale
	}
	if err := validateAppEgressPolicyPlan(req.Plan, current); err != nil {
		return err
	}
	prefixes := make([]netip.Prefix, len(req.EgressAllowlist))
	for i, cidr := range req.EgressAllowlist {
		p, err := netip.ParsePrefix(cidr)
		if err != nil {
			return runtimeadmission.ErrInvalid
		}
		prefixes[i] = p
	}
	expected, err := normalizeAppEgressPolicy(req.admission.EgressRevision, prefixes, req.EgressPorts)
	if err != nil {
		return runtimeadmission.ErrInvalid
	}
	if !slices.Equal(current.allowlist, expected.allowlist) || !slices.Equal(current.ports, expected.ports) {
		return runtimeadmission.ErrStale
	}
	return nil
}

func (m *Manager) finishRuntimeAdmissionFlight(instance string, flight *runtimeAdmissionFlight) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finishRuntimeAdmissionFlightLocked(instance, flight)
}

func (m *Manager) finishRuntimeAdmissionFlightLocked(instance string, flight *runtimeAdmissionFlight) {
	if m.runtimeAdmissionFlights[instance] == flight {
		delete(m.runtimeAdmissionFlights, instance)
	}
	flight.once.Do(func() { close(flight.done) })
}

func (m *Manager) cancelRuntimeAdmissionFlight(ctx context.Context, instance string) error {
	m.mu.Lock()
	flight := m.runtimeAdmissionFlights[instance]
	if flight != nil {
		flight.cancel()
	}
	m.mu.Unlock()
	if flight == nil {
		return nil
	}
	select {
	case <-flight.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
