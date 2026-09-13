package main

import (
	"fmt"
	"sync"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	guestReceiverEvents   = "events"
	guestReceiverIdentity = "workload_identity"
)

var guestReceiverFailureKinds = []string{"prepare", "accept", "read", "write", "protocol", "overload", "unknown"}

type guestVsockReceiverHealth struct {
	mu      sync.Mutex
	signals map[string]*wire.ReadySignal
	up      *prometheus.GaugeVec
	errors  *prometheus.CounterVec
}

func newGuestVsockReceiverHealth(reg prometheus.Registerer) *guestVsockReceiverHealth {
	h := &guestVsockReceiverHealth{
		signals: map[string]*wire.ReadySignal{},
		up: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "vmmd_guest_vsock_receiver_up",
			Help: "Whether a required Firecracker guest-initiated receiver is available (1) or unavailable (0).",
		}, []string{"receiver"}),
		errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vmmd_guest_vsock_receiver_errors_total",
			Help: "Guest receiver failures by receiver and bounded failure kind.",
		}, []string{"receiver", "kind"}),
	}
	for _, receiver := range []string{guestReceiverEvents, guestReceiverIdentity} {
		sig := &wire.ReadySignal{}
		sig.Set(false, receiver+" receiver not registered")
		h.signals[receiver] = sig
		h.up.WithLabelValues(receiver).Set(0)
		for _, kind := range guestReceiverFailureKinds {
			h.errors.WithLabelValues(receiver, kind)
		}
	}
	if reg != nil {
		reg.MustRegister(h.up, h.errors)
	}
	return h
}

func (h *guestVsockReceiverHealth) Signal(receiver string) *wire.ReadySignal {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.signals[receiver]
}

func (h *guestVsockReceiverHealth) Observe(port uint32, failureKind string, err error) {
	if h == nil {
		return
	}
	receiver, ok := receiverForGuestVsockPort(port)
	if !ok {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		h.up.WithLabelValues(receiver).Set(1)
		h.signals[receiver].Set(true, "")
		return
	}
	kind := boundedGuestReceiverFailureKind(failureKind)
	h.errors.WithLabelValues(receiver, kind).Inc()
	if !guestReceiverFailureDegradesReadiness(kind) {
		return
	}
	h.up.WithLabelValues(receiver).Set(0)
	h.signals[receiver].Set(false, fmt.Sprintf("%s receiver %s failed: %v", receiver, kind, err))
}

func receiverForGuestVsockPort(port uint32) (string, bool) {
	switch port {
	case fcvm.VsockGuestEventHostPort:
		return guestReceiverEvents, true
	case fcvm.VsockWorkloadIdentityHostPort:
		return guestReceiverIdentity, true
	default:
		return "", false
	}
}

func boundedGuestReceiverFailureKind(kind string) string {
	for _, candidate := range guestReceiverFailureKinds {
		if kind == candidate {
			return kind
		}
	}
	return "unknown"
}

func guestReceiverFailureDegradesReadiness(kind string) bool {
	switch kind {
	case "read", "write", "protocol", "overload":
		return false
	default:
		return true
	}
}
