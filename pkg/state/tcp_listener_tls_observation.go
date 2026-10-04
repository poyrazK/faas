package state

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// TCPListenerTLSObservation is public-edge evidence, never customer intent.
// It deliberately excludes private keys, bundle paths and provider errors.
type TCPListenerTLSObservation struct {
	ListenerID      string
	EdgeID          string
	Hostname        string
	IntentUpdatedAt time.Time
	ObservedAt      time.Time
	Ready           bool
	NotAfter        time.Time
}

var ErrInvalidTCPListenerTLSObservation = errors.New("state: invalid TCP listener TLS observation")

func (o TCPListenerTLSObservation) Validate() error {
	policy, err := (api.TCPListenerTLSConfig{Mode: api.TCPListenerTLSTerminate, Hostname: o.Hostname}).Normalize()
	if err != nil || policy.Hostname != o.Hostname || o.ListenerID == "" || o.IntentUpdatedAt.IsZero() || o.ObservedAt.IsZero() || o.IntentUpdatedAt.After(o.ObservedAt) {
		return ErrInvalidTCPListenerTLSObservation
	}
	if err := ValidateTCPListenerTLSEdgeID(o.EdgeID); err != nil {
		return err
	}
	if (o.Ready && !o.NotAfter.After(o.ObservedAt)) || (!o.Ready && !o.NotAfter.IsZero()) {
		return ErrInvalidTCPListenerTLSObservation
	}
	return nil
}

func ValidateTCPListenerTLSEdgeID(edgeID string) error {
	if edgeID == "" || !utf8.ValidString(edgeID) || len(edgeID) > api.TCPListenerTLSObservationEdgeIDMaxBytes || strings.TrimSpace(edgeID) != edgeID || strings.ContainsFunc(edgeID, unicode.IsControl) {
		return ErrInvalidTCPListenerTLSObservation
	}
	return nil
}

// Status reports this edge's certificate evidence only, never fleet coverage,
// public routing, client trust, certificate issuance or guest availability.
func (o TCPListenerTLSObservation) Status(listener TCPListener, now time.Time) string {
	if o.Validate() != nil || o.ListenerID != listener.ID || !listener.Enabled || listener.TLSMode != api.TCPListenerTLSTerminate || o.Hostname != listener.TLSHostname || !o.IntentUpdatedAt.Equal(listener.UpdatedAt) || o.ObservedAt.After(now) || now.Sub(o.ObservedAt) >= api.TCPListenerTLSObservationMaxAge {
		return "unknown"
	}
	if !o.Ready || !o.NotAfter.After(now) {
		return "not_ready"
	}
	return "ready"
}
